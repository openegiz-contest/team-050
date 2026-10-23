package main

import (
	"bytes"
	"context"
	"encoding/json"
	"log"
	"os"
	"os/signal"
	"strings"
	"sync/atomic"
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
	commands := make(chan domain.Command, 256)
	var ack atomic.Int64
	ack.Store(time.Now().UnixNano())
	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer cancel()
	client, e := transport.Connect("team050-engine", func(client mqtt.Client) {
		client.Subscribe("opentwins/live/messages/"+domain.Namespace+"/control-01", 1, func(_ mqtt.Client, m mqtt.Message) {
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
		client.Subscribe("opentwins/twin/events/"+domain.Namespace+"/+", 1, func(_ mqtt.Client, m mqtt.Message) {
			if strings.HasSuffix(m.Topic(), "/control-01") {
				ack.Store(time.Now().UnixNano())
			}
		})
	})
	if e != nil {
		log.Fatal(e)
	}
	defer client.Disconnect(500)
	publish := func() {
		s.Seq++
		if e := store.Save(s, nil, nil); e != nil {
			log.Fatal(e)
		}
		for _, t := range domain.Snapshot(c, s) {
			if e := transport.PublishFeatures(client, t); e != nil {
				log.Printf("publish: %v", e)
				break
			}
		}
	}
	saveResults := func(cmd *domain.Command, rs []domain.Result) {
		if e := store.Save(s, cmd, rs); e != nil {
			log.Fatal(e)
		}
		for _, r := range rs {
			if e := transport.PublishResult(client, r); e != nil {
				log.Printf("result: %v", e)
			}
		}
	}
	publish()
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
		if !client.IsConnectionOpen() || time.Since(time.Unix(0, ack.Load())) > 20*time.Second {
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
					transport.PublishResult(client, r)
				} else {
					transport.PublishResult(client, *prior)
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
		}
	}
}
