package web

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/openegiz-contest/team-050/team/mine/domain"
	"github.com/openegiz-contest/team-050/team/mine/persistence"
)

func TestOldRunRequestsCannotBlockNewCommands(t *testing.T) {
	now := time.Now()
	s := &Server{Current: &Frame{RunID: "current"}, Store: &persistence.Store{Dir: t.TempDir()}, Requests: map[string]*RequestRecord{}}
	for i := 0; i < 79; i++ {
		id := fmt.Sprintf("old-%d", i)
		s.Requests[id] = &RequestRecord{Command: domain.Command{ID: id, RunID: "previous"}, Result: domain.Result{Status: "received"}, Created: now.Add(-time.Hour)}
	}
	for i := 0; i < 5; i++ {
		id := fmt.Sprintf("new-%d", i)
		s.Requests[id] = &RequestRecord{Command: domain.Command{ID: id, RunID: "current"}, Result: domain.Result{Status: "received"}, Created: now.Add(-time.Minute + time.Duration(i)*time.Second)}
	}
	batch := s.dueCommands(now)
	if len(batch) != 4 {
		t.Fatalf("unbounded batch: %d", len(batch))
	}
	for i, r := range batch {
		if r.Command.ID != fmt.Sprintf("new-%d", i) {
			t.Fatal("old run entered batch")
		}
	}
	for i := 0; i < 79; i++ {
		if s.Requests[fmt.Sprintf("old-%d", i)].Result.Status != "cancelled" {
			t.Fatal("stale request survived")
		}
	}
	if s.Requests["new-4"].Attempts != 0 || !s.Requests["new-4"].Sent.IsZero() {
		t.Fatal("unsent request incorrectly backed off")
	}
	batch = s.dueCommands(now.Add(time.Second))
	if len(batch) != 1 || batch[0].Command.ID != "new-4" {
		t.Fatal("new request starved by retries")
	}
}

func TestMirrorRecoveryDoesNotFloodActiveStream(t *testing.T) {
	for _, active := range []bool{true, false} {
		t.Run(fmt.Sprintf("active=%v", active), func(t *testing.T) {
			c, err := domain.LoadConfig("../config/site.json")
			if err != nil {
				t.Fatal(err)
			}
			state := domain.NewState(c)
			state.Seq = 7
			state.Mode = "paused"
			things := map[string]domain.Thing{}
			for _, thing := range domain.Snapshot(c, state) {
				things[thing.ID] = thing
			}
			var reads atomic.Int32
			upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				reads.Add(1)
				thing, ok := things[strings.TrimPrefix(r.URL.Path, "/api/2/things/")]
				if !ok {
					http.NotFound(w, r)
					return
				}
				json.NewEncoder(w).Encode(thing)
			}))
			defer upstream.Close()
			s := New(c, nil)
			s.Platform.Base = upstream.URL
			if active {
				s.LastEvent = time.Now()
			}
			s.repairMirror(context.Background(), time.Now().Add(-time.Minute))
			if s.Topology == nil {
				t.Fatal("topology not recovered from Ditto")
			}
			if active && reads.Load() != 1 {
				t.Fatalf("active stream caused %d REST reads", reads.Load())
			}
			if !active && (s.Current == nil || s.Current.Seq != 7 || len(s.Current.Entities) != 35) {
				t.Fatal("idle stream did not recover a coherent frame")
			}
		})
	}
}
