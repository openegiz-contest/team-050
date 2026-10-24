package web

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	mqtt "github.com/eclipse/paho.mqtt.golang"
	"github.com/openegiz-contest/team-050/team/mine/domain"
	"github.com/openegiz-contest/team-050/team/mine/persistence"
	"github.com/openegiz-contest/team-050/team/mine/transport"
)

type Frame struct {
	RunID    string                     `json:"run_id"`
	Seq      uint64                     `json:"snapshot_seq"`
	Time     float64                    `json:"sim_time_s"`
	Entities map[string]domain.Features `json:"entities"`
	Received time.Time                  `json:"received_at"`
}
type RequestRecord struct {
	Command domain.Command `json:"command"`
	Result  domain.Result  `json:"result"`
	Created time.Time      `json:"created_at"`
	Sent    time.Time      `json:"sent_at"`
}
type Server struct {
	mu        sync.Mutex
	Config    domain.Config
	Platform  *transport.Platform
	Store     *persistence.Store
	MQTT      mqtt.Client
	Current   *Frame
	Frames    map[string]*Frame
	Order     []string
	Topology  map[string]any
	Requests  map[string]*RequestRecord
	Listeners map[chan []byte]bool
	Sessions  map[string]time.Time
	Expected  int
}

func New(c domain.Config, store *persistence.Store) *Server {
	return &Server{Config: c, Platform: transport.NewPlatform(), Store: store, Frames: map[string]*Frame{}, Requests: map[string]*RequestRecord{}, Listeners: map[chan []byte]bool{}, Sessions: map[string]time.Time{}, Expected: len(domain.Snapshot(c, domain.NewState(c)))}
}
func value(f domain.Features, k string) any { return f[k].Properties.Value }
func number(v any) float64 {
	switch n := v.(type) {
	case float64:
		return n
	case int:
		return float64(n)
	case uint64:
		return float64(n)
	}
	return 0
}
func (s *Server) BroadcastLocked(kind string, v any) {
	b, _ := json.Marshal(map[string]any{"type": kind, "data": v})
	for ch := range s.Listeners {
		select {
		case ch <- b:
		default: /* slow clients receive the next full frame */
		}
	}
}
func (s *Server) Accept(t domain.Thing, rest bool) {
	if !strings.HasPrefix(t.ID, domain.Namespace+":") {
		return
	}
	run, ok := value(t.Features, "idSimulationRun").(string)
	if !ok || run == "" {
		return
	}
	seq := uint64(number(value(t.Features, "snapshot_seq")))
	key := run + ":" + strconv.FormatUint(seq, 10)
	var report json.RawMessage
	s.mu.Lock()
	if rest && s.Current != nil && s.Current.RunID != run {
		s.mu.Unlock()
		return
	}
	if domain.Name(t.ID) == "roads-01" && t.Attributes != nil {
		if top, ok := t.Attributes["topology"].(map[string]any); ok {
			s.Topology = top
		}
	}
	if s.Current != nil && s.Current.RunID == run && s.Current.Seq >= seq {
		s.mu.Unlock()
		return
	}
	f := s.Frames[key]
	if f == nil {
		f = &Frame{RunID: run, Seq: seq, Time: number(value(t.Features, "sim_time_s")), Entities: map[string]domain.Features{}}
		s.Frames[key] = f
		s.Order = append(s.Order, key)
		for len(s.Order) > 8 {
			delete(s.Frames, s.Order[0])
			s.Order = s.Order[1:]
		}
	}
	f.Entities[domain.Name(t.ID)] = t.Features
	if len(f.Entities) == s.Expected {
		f.Received = time.Now().UTC()
		s.Current = f
		s.BroadcastLocked("state", f)
		if raw, ok := value(f.Entities["mine-01"], "run_report_json").(string); ok && raw != "" && json.Valid([]byte(raw)) {
			report = json.RawMessage(raw)
		}
	}
	s.mu.Unlock()
	if report != nil {
		if e := s.Store.Archive(run, report); e != nil {
			log.Printf("archive: %v", e)
		}
	}
}
func (s *Server) Subscribe(client mqtt.Client) {
	client.Subscribe("team050/ditto/twin/events/"+domain.Namespace+"/+", 1, func(_ mqtt.Client, m mqtt.Message) {
		var env transport.Envelope
		if json.Unmarshal(m.Payload(), &env) != nil {
			return
		}
		name := m.Topic()[strings.LastIndex(m.Topic(), "/")+1:]
		t := domain.Thing{ID: domain.ThingID(name)}
		if env.Path == "/features" {
			if json.Unmarshal(env.Value, &t.Features) != nil {
				return
			}
		} else if env.Path == "/" {
			if json.Unmarshal(env.Value, &t) != nil {
				return
			}
		} else {
			return
		}
		s.Accept(t, false)
	})
	client.Subscribe("team050/ditto/live/messages/"+domain.Namespace+"/control-01", 1, func(_ mqtt.Client, m mqtt.Message) {
		var env transport.Envelope
		if json.Unmarshal(m.Payload(), &env) != nil || env.Path != "/outbox/messages/command_result" {
			return
		}
		var r domain.Result
		if json.Unmarshal(env.Value, &r) != nil {
			return
		}
		s.mu.Lock()
		record := s.Requests[r.ID]
		if record != nil && record.Command.RunID == r.RunID {
			record.Result = r
			_ = s.persistRequest(record)
			s.BroadcastLocked("command", record)
		}
		s.mu.Unlock()
	})
}
func (s *Server) Hydrate(ctx context.Context) {
	names := []string{}
	for _, t := range domain.Snapshot(s.Config, domain.NewState(s.Config)) {
		names = append(names, domain.Name(t.ID))
	}
	sem := make(chan struct{}, 4)
	var wg sync.WaitGroup
	for _, name := range names {
		sem <- struct{}{}
		wg.Add(1)
		go func(name string) {
			defer wg.Done()
			defer func() { <-sem }()
			t, e := s.Platform.Get(ctx, name)
			if e == nil {
				s.Accept(t, true)
			}
		}(name)
	}
	wg.Wait()
}
func (s *Server) Run(ctx context.Context) error {
	client, e := transport.Connect("team050-web", s.Subscribe)
	if e != nil {
		return e
	}
	s.MQTT = client
	go func() {
		ticker := time.NewTicker(3 * time.Second)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				s.mu.Lock()
				need := s.Topology == nil || s.Current == nil || time.Since(s.Current.Received) > 5*time.Second
				s.mu.Unlock()
				if need {
					s.Hydrate(ctx)
				}
			}
		}
	}()
	go s.retryCommands(ctx)
	return nil
}
func (s *Server) persistRequest(r *RequestRecord) error {
	b, e := json.Marshal(r)
	if e != nil {
		return e
	}
	dir := filepath.Join(s.Store.Dir, "outbox")
	if e = os.MkdirAll(dir, 0700); e != nil {
		return e
	}
	h := sha256.Sum256([]byte(r.Command.ID))
	path := filepath.Join(dir, hex.EncodeToString(h[:])+".json")
	tmp := path + ".tmp"
	if e = os.WriteFile(tmp, b, 0600); e != nil {
		return e
	}
	return os.Rename(tmp, path)
}
func (s *Server) retryCommands(ctx context.Context) {
	dir := filepath.Join(s.Store.Dir, "outbox")
	entries, _ := os.ReadDir(dir)
	s.mu.Lock()
	for _, e := range entries {
		if strings.HasSuffix(e.Name(), ".json") {
			b, err := os.ReadFile(filepath.Join(dir, e.Name()))
			if err != nil {
				continue
			}
			var r RequestRecord
			if json.Unmarshal(b, &r) == nil {
				s.Requests[r.Command.ID] = &r
			}
		}
	}
	s.mu.Unlock()
	ticker := time.NewTicker(time.Second)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			s.mu.Lock()
			pending := []RequestRecord{}
			for _, r := range s.Requests {
				if (r.Result.Status == "received" || r.Result.Status == "scheduled") && time.Since(r.Sent) > 4*time.Second && time.Since(r.Created) < 24*time.Hour {
					pending = append(pending, *r)
					r.Sent = time.Now()
				}
			}
			s.mu.Unlock()
			sort.Slice(pending, func(i, j int) bool {
				if pending[i].Created.Equal(pending[j].Created) {
					return pending[i].Command.ID < pending[j].Command.ID
				}
				return pending[i].Created.Before(pending[j].Created)
			})
			for _, record := range pending {
				cmd := record.Command
				_, _, e := s.Platform.Request(ctx, "POST", "/api/2/things/"+domain.ThingID("control-01")+"/inbox/messages/dispatch?timeout=0", cmd)
				if e != nil {
					log.Printf("dispatch %s: %v", cmd.ID, e)
				}
			}
		}
	}
}
func jsonResponse(w http.ResponseWriter, code int, v any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(code)
	_ = json.NewEncoder(w).Encode(v)
}
func (s *Server) authorized(r *http.Request) bool {
	origin := r.Header.Get("Origin")
	if origin != "" {
		u, e := url.Parse(origin)
		if e != nil || u.Host != r.Host {
			return false
		}
	}
	cookie, e := r.Cookie("mine_session")
	if e != nil {
		return false
	}
	s.mu.Lock()
	expiry, ok := s.Sessions[cookie.Value]
	s.mu.Unlock()
	return ok && time.Now().Before(expiry) && r.Header.Get("X-Mine-Session") == cookie.Value
}
func (s *Server) Handler(uiPath string) http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /api/session", func(w http.ResponseWriter, r *http.Request) {
		b := make([]byte, 24)
		if _, e := rand.Read(b); e != nil {
			jsonResponse(w, 500, map[string]string{"error": "session unavailable"})
			return
		}
		token := hex.EncodeToString(b)
		s.mu.Lock()
		for k, e := range s.Sessions {
			if time.Now().After(e) {
				delete(s.Sessions, k)
			}
		}
		s.Sessions[token] = time.Now().Add(24 * time.Hour)
		s.mu.Unlock()
		http.SetCookie(w, &http.Cookie{Name: "mine_session", Value: token, Path: "/", HttpOnly: true, SameSite: http.SameSiteStrictMode, MaxAge: 86400})
		jsonResponse(w, 200, map[string]string{"token": token})
	})
	mux.HandleFunc("GET /api/topology", func(w http.ResponseWriter, r *http.Request) {
		s.mu.Lock()
		v := s.Topology
		s.mu.Unlock()
		if v == nil {
			jsonResponse(w, 503, map[string]string{"error": "Ожидание карты из Ditto"})
			return
		}
		jsonResponse(w, 200, v)
	})
	mux.HandleFunc("GET /api/state", func(w http.ResponseWriter, r *http.Request) {
		s.mu.Lock()
		f := s.Current
		s.mu.Unlock()
		jsonResponse(w, 200, map[string]any{"frame": f, "stale": f == nil || time.Since(f.Received) > 5*time.Second})
	})
	mux.HandleFunc("GET /api/stream", func(w http.ResponseWriter, r *http.Request) {
		flusher, ok := w.(http.Flusher)
		if !ok {
			http.Error(w, "SSE unavailable", 500)
			return
		}
		w.Header().Set("Content-Type", "text/event-stream")
		w.Header().Set("Cache-Control", "no-cache")
		w.Header().Set("X-Accel-Buffering", "no")
		ch := make(chan []byte, 4)
		s.mu.Lock()
		s.Listeners[ch] = true
		f := s.Current
		s.mu.Unlock()
		defer func() { s.mu.Lock(); delete(s.Listeners, ch); s.mu.Unlock() }()
		if f != nil {
			b, _ := json.Marshal(map[string]any{"type": "state", "data": f})
			fmt.Fprintf(w, "data: %s\n\n", b)
			flusher.Flush()
		}
		heartbeat := time.NewTicker(2 * time.Second)
		defer heartbeat.Stop()
		for {
			select {
			case <-r.Context().Done():
				return
			case b := <-ch:
				fmt.Fprintf(w, "data: %s\n\n", b)
				flusher.Flush()
			case <-heartbeat.C:
				fmt.Fprint(w, ": heartbeat\n\n")
				flusher.Flush()
			}
		}
	})
	mux.HandleFunc("POST /api/commands", func(w http.ResponseWriter, r *http.Request) {
		if !s.authorized(r) {
			jsonResponse(w, 403, map[string]string{"error": "Обновите сессию страницы"})
			return
		}
		var cmd domain.Command
		if e := json.NewDecoder(http.MaxBytesReader(w, r.Body, 16384)).Decode(&cmd); e != nil || cmd.ID == "" || cmd.RunID == "" || cmd.Schema != 1 {
			jsonResponse(w, 400, map[string]string{"error": "Некорректная команда"})
			return
		}
		s.mu.Lock()
		defer s.mu.Unlock()
		if existing := s.Requests[cmd.ID]; existing != nil {
			if domain.JSON(existing.Command) != domain.JSON(cmd) {
				jsonResponse(w, 409, map[string]string{"error": "ID команды уже используется"})
				return
			}
			jsonResponse(w, 202, existing)
			return
		}
		if len(s.Requests) > 10000 {
			jsonResponse(w, 429, map[string]string{"error": "Очередь команд заполнена"})
			return
		}
		record := &RequestRecord{Command: cmd, Created: time.Now(), Result: domain.Result{ID: cmd.ID, RunID: cmd.RunID, Status: "received", Message: "Отправка через Ditto"}}
		if e := s.persistRequest(record); e != nil {
			jsonResponse(w, 500, map[string]string{"error": "Не удалось сохранить команду"})
			return
		}
		s.Requests[cmd.ID] = record
		s.BroadcastLocked("command", record)
		jsonResponse(w, 202, record)
	})
	mux.HandleFunc("GET /api/commands/{id}", func(w http.ResponseWriter, r *http.Request) {
		s.mu.Lock()
		record := s.Requests[r.PathValue("id")]
		if record == nil {
			s.mu.Unlock()
			jsonResponse(w, 404, map[string]string{"error": "Команда не найдена"})
			return
		}
		copy := *record
		s.mu.Unlock()
		jsonResponse(w, 200, copy)
	})
	mux.HandleFunc("POST /api/routes/preview", func(w http.ResponseWriter, r *http.Request) {
		if !s.authorized(r) {
			jsonResponse(w, 403, map[string]string{"error": "Нет сессии"})
			return
		}
		var cmd domain.Command
		if json.NewDecoder(http.MaxBytesReader(w, r.Body, 16384)).Decode(&cmd) != nil {
			jsonResponse(w, 400, map[string]string{"error": "Некорректный запрос"})
			return
		}
		s.mu.Lock()
		top := s.Topology
		frame := s.Current
		s.mu.Unlock()
		if top == nil || frame == nil {
			jsonResponse(w, 503, map[string]string{"error": "Ожидание OpenEgiz"})
			return
		}
		var config domain.Config
		b, _ := json.Marshal(top)
		if json.Unmarshal(b, &config) != nil {
			jsonResponse(w, 500, map[string]string{"error": "Некорректная карта"})
			return
		}
		plan, e := config.ResolvePlan(cmd)
		if e != nil {
			jsonResponse(w, 400, map[string]string{"error": e.Error()})
			return
		}
		f := frame.Entities[domain.Name(cmd.Target)]
		if f == nil {
			jsonResponse(w, 400, map[string]string{"error": "Выберите самосвал"})
			return
		}
		anchor, _ := value(f, "node_id").(string)
		if edge, ok := value(f, "edge_id").(string); ok && edge != "" {
			parts := strings.Split(edge, ">")
			anchor = parts[len(parts)-1]
		}
		dest := plan.Nodes[0]
		if number(value(f, "payload_t")) > 0 {
			dest = plan.Nodes[len(plan.Nodes)-1]
		}
		transition := config.ShortestPath(anchor, dest)
		if transition == nil {
			jsonResponse(w, 400, map[string]string{"error": "Нет пути к назначению"})
			return
		}
		d, _ := config.ValidatePath(plan.Nodes)
		jsonResponse(w, 200, map[string]any{"plan": plan, "distance_km": d, "loaded_minutes": d / config.SpeedLoaded * 60, "empty_minutes": d / config.SpeedEmpty * 60, "transition_path": transition, "route_revision": number(value(f, "route_revision")), "topology_version": config.TopologyVersion})
	})
	mux.HandleFunc("GET /api/runs", func(w http.ResponseWriter, r *http.Request) {
		v, e := s.Store.Runs()
		if e != nil {
			jsonResponse(w, 500, map[string]string{"error": "Не удалось прочитать отчёты"})
			return
		}
		jsonResponse(w, 200, v)
	})
	mux.HandleFunc("GET /healthz", func(w http.ResponseWriter, r *http.Request) {
		jsonResponse(w, 200, map[string]string{"status": "alive"})
	})
	mux.HandleFunc("GET /readyz", func(w http.ResponseWriter, r *http.Request) {
		s.mu.Lock()
		ready := s.Topology != nil && s.Current != nil && time.Since(s.Current.Received) < 10*time.Second
		s.mu.Unlock()
		if !ready {
			jsonResponse(w, 503, map[string]string{"status": "waiting-for-platform"})
			return
		}
		jsonResponse(w, 200, map[string]string{"status": "ready"})
	})
	fs := http.FileServer(http.Dir(uiPath))
	mux.HandleFunc("GET /", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("X-Content-Type-Options", "nosniff")
		w.Header().Set("Referrer-Policy", "same-origin")
		if strings.HasPrefix(r.URL.Path, "/api/") {
			http.NotFound(w, r)
			return
		}
		fs.ServeHTTP(w, r)
	})
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		defer func() {
			if v := recover(); v != nil {
				log.Printf("HTTP panic: %v", v)
			}
		}()
		mux.ServeHTTP(w, r)
	})
}
