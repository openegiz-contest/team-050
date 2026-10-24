package main

import (
	"bytes"
	"context"
	"encoding/json"
	"log"
	"os"
	"os/signal"
	"strings"
	"sync"
	"syscall"
	"time"

	mqtt "github.com/eclipse/paho.mqtt.golang"
	"github.com/openegiz-contest/team-050/team/mine/domain"
	"github.com/openegiz-contest/team-050/team/mine/persistence"
	"github.com/openegiz-contest/team-050/team/mine/transport"
)

func main() {
	c, e := domain.LoadConfig(transport.Env("SITE_CONFIG", "config/site.json"))
	if e != nil {
		log.Fatal(e)
	}
	store, e := persistence.Open(transport.Env("DATA_DIR", "data/engine"), true)
	if e != nil {
		log.Fatal(e)
	}
	defer store.Close()
	s, e := store.Load()
	if e != nil {
		log.Fatal(e)
	}
	if s == nil {
		s = domain.NewState(c)
	} else {
		if s.ConfigHash != c.Hash {
			log.Fatal("Saved configuration differs: preserve existing data and select a new DATA_DIR for the changed scenario")
		}
		if s.Mode == "running" {
			s.Mode = "paused"
			s.Log("Восстановлено после остановки. Смена на паузе.")
		}
	}
	if s.PlanTonnes == 0 && s.Mode != "finished" {
		s.PlanTonnes = 94000
		s.Log("Принят сменный план: 94 000 т переработанной руды.")
	}
	commands := make(chan domain.Command, 256)
	type receipt struct {
		name string
		run  string
		seq  uint64
	}
	receipts := make(chan receipt, 512)
	lastAck := time.Now()
	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer cancel()
	client, e := transport.Connect("team050-engine", func(client mqtt.Client) {
		client.Subscribe("team050/ditto/live/messages/"+domain.Namespace+"/control-01", 1, func(_ mqtt.Client, m mqtt.Message) {
			var env transport.Envelope
			if json.Unmarshal(m.Payload(), &env) != nil || env.Path != "/inbox/messages/dispatch" || !strings.HasSuffix(env.Topic, "/things/live/messages/dispatch") {
				return
			}
			var cmd domain.Command
			if json.Unmarshal(env.Value, &cmd) != nil {
				return
			}
			select {
			case commands <- cmd:
			case <-ctx.Done():
			}
		})
		client.Subscribe("team050/ditto/twin/events/"+domain.Namespace+"/+", 1, func(_ mqtt.Client, m mqtt.Message) {
			var env transport.Envelope
			var features domain.Features
			if json.Unmarshal(m.Payload(), &env) != nil || env.Path != "/features" || json.Unmarshal(env.Value, &features) != nil {
				return
			}
			run, _ := features["idSimulationRun"].Properties.Value.(string)
			seq, _ := features["snapshot_seq"].Properties.Value.(float64)
			r := receipt{m.Topic()[strings.LastIndex(m.Topic(), "/")+1:], run, uint64(seq)}
			select {
			case receipts <- r:
			case <-ctx.Done():
			}
		})
	})
	if e != nil {
		log.Fatal(e)
	}
	defer client.Disconnect(500)
	// Network waits must not hold the simulation owner or delay commands.
	frameQueue := make(chan []domain.Thing, 1)
	frameDone := make(chan error, 1)
	resultQueue := make(chan domain.Result, 256)
	go func() {
		for {
			select {
			case <-ctx.Done():
				return
			case things := <-frameQueue:
				err := transport.PublishFrame(client, things)
				select {
				case frameDone <- err:
				case <-ctx.Done():
					return
				}
			}
		}
	}()
	go func() {
		for {
			select {
			case <-ctx.Done():
				return
			case r := <-resultQueue:
				if client.IsConnectionOpen() {
					if err := transport.PublishResult(client, r); err != nil {
						log.Printf("result: %v", err)
					}
				}
			}
		}
	}()
	sendResult := func(r domain.Result) {
		select {
		case resultQueue <- r:
		default:
			log.Printf("result %s remains in durable journal; retry will resend", r.ID)
		}
	}
	publishing := false
	// Keep only one immutable frame in flight. A broker PUBACK does not mean
	// Ditto persisted it; the matching twin event is the acknowledgement.
	var flight []domain.Thing
	pending := map[string]bool{}
	dirty := false
	var sentAt, retriedAt time.Time
	platform := transport.NewPlatform()
	lookupDone := make(chan struct{}, 1)
	checking := false
	publish := func() {
		if e := store.Save(s, nil, nil); e != nil {
			log.Fatal(e)
		}
		dirty = true
	}
	flush := func(now time.Time) {
		if publishing {
			return
		}
		retrying := len(pending) > 0
		if len(pending) == 0 {
			if !dirty || now.Sub(sentAt) < time.Second {
				return
			}
			s.Seq++
			if e := store.Save(s, nil, nil); e != nil {
				log.Fatal(e)
			}
			flight = domain.Snapshot(c, s)
			for _, t := range flight {
				pending[domain.Name(t.ID)] = true
			}
			dirty = false
			sentAt = now
		} else if now.Sub(retriedAt) < 10*time.Second {
			return
		}
		retriedAt = now
		if retrying && !checking {
			checking = true
			missing := []string{}
			for name := range pending {
				missing = append(missing, name)
			}
			// An unchanged rewrite may not emit another event. REST readback
			// also proves persistence if the original MQTT event was lost.
			go func() {
				defer func() { lookupDone <- struct{}{} }()
				var wg sync.WaitGroup
				sem := make(chan struct{}, 4)
				for _, name := range missing {
					sem <- struct{}{}
					wg.Add(1)
					go func(name string) {
						defer wg.Done()
						defer func() { <-sem }()
						t, err := platform.Get(ctx, name)
						if err != nil {
							return
						}
						run, _ := t.Features["idSimulationRun"].Properties.Value.(string)
						seq, _ := t.Features["snapshot_seq"].Properties.Value.(float64)
						select {
						case receipts <- receipt{name, run, uint64(seq)}:
						case <-ctx.Done():
						}
					}(name)
				}
				wg.Wait()
			}()
		}
		missingThings := []domain.Thing{}
		for _, t := range flight {
			if !pending[domain.Name(t.ID)] {
				continue
			}
			missingThings = append(missingThings, t)
		}
		frameQueue <- missingThings
		publishing = true
	}
	saveResults := func(cmd *domain.Command, rs []domain.Result) {
		if e := store.Save(s, cmd, rs); e != nil {
			log.Fatal(e)
		}
		for _, r := range rs {
			sendResult(r)
		}
	}
	publish()
	flush(time.Now())
	tick := time.NewTicker(50 * time.Millisecond)
	defer tick.Stop()
	last := time.Now()
	lastPublish := last
	budget := 0.0
	advance := func(now time.Time) {
		delta := now.Sub(last).Seconds()
		last = now
		if s.Mode != "running" {
			budget = 0
			return
		}
		if !client.IsConnectionOpen() || time.Since(lastAck) > 20*time.Second {
			s.Mode = "paused"
			s.Log("Техническая пауза: нет подтверждений от OpenEgiz.")
			publish()
			budget = 0
			return
		}
		if delta > 0 {
			budget += delta * float64(s.Speed)
		}
		steps := 0
		var rs []domain.Result
		for budget >= 1 && s.Mode == "running" && steps < 600 {
			rs = append(rs, s.Step(c, 1)...)
			budget--
			steps++
		}
		if len(rs) > 0 || s.Mode == "finished" {
			for _, r := range rs {
				if r.ID == s.LastCommand {
					s.LastStatus = r.Status
				}
			}
			saveResults(nil, rs)
			publish()
		}
	}
	for {
		select {
		case <-ctx.Done():
			if s.Mode == "running" {
				s.Mode = "paused"
			}
			publish()
			return
		case r := <-receipts:
			if len(flight) > 0 {
				f := flight[0].Features
				if r.run == f["idSimulationRun"].Properties.Value && r.seq == f["snapshot_seq"].Properties.Value.(uint64) {
					delete(pending, r.name)
					lastAck = time.Now()
				}
			}
		case <-lookupDone:
			checking = false
		case err := <-frameDone:
			publishing = false
			if err != nil {
				log.Printf("publish: %v", err)
			}
		case cmd := <-commands:
			advance(time.Now())
			request, _ := json.Marshal(cmd)
			old, prior, e := store.Find(cmd.RunID, cmd.ID)
			if e != nil {
				log.Fatal(e)
			}
			if prior != nil {
				if !bytes.Equal(request, old) {
					r := domain.Result{ID: cmd.ID, RunID: cmd.RunID, Status: "rejected", Reason: "ID_CONFLICT", Message: "Идентификатор использован для другой команды", Time: s.Time, Seq: s.Seq}
					sendResult(r)
				} else {
					sendResult(*prior)
				}
				continue
			}
			beforeRun := s.RunID
			rs, e := s.Handle(c, cmd)
			if e != nil {
				log.Printf("command: %v", e)
				continue
			}
			if len(rs) > 0 {
				s.LastCommand = cmd.ID
				s.LastStatus = rs[len(rs)-1].Status
			}
			saveResults(&cmd, rs)
			publish()
			if s.RunID != beforeRun || s.Mode != "running" {
				budget = 0
			}
		case <-tick.C:
			advance(time.Now())
			if time.Since(lastPublish) >= time.Second {
				publish()
				lastPublish = time.Now()
			}
			flush(time.Now())
		}
	}
}
