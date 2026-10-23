package main

import (
	"context"
	"log"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/openegiz-contest/team-050/team/mine/domain"
	"github.com/openegiz-contest/team-050/team/mine/persistence"
	"github.com/openegiz-contest/team-050/team/mine/transport"
	"github.com/openegiz-contest/team-050/team/mine/web"
)

func main() {
	c, e := domain.LoadConfig(transport.Env("SITE_CONFIG", "config/site.json"))
	if e != nil {
		log.Fatal(e)
	}
	store, e := persistence.Open(transport.Env("DATA_DIR", "data/web"), false)
	if e != nil {
		log.Fatal(e)
	}
	defer store.Close()
	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer cancel()
	s := web.New(c, store)
	if e = s.Run(ctx); e != nil {
		log.Fatal(e)
	}
	defer s.MQTT.Disconnect(500)
	server := &http.Server{Addr: transport.Env("HTTP_ADDR", ":8088"), Handler: s.Handler(transport.Env("UI_DIR", "ui/dist")), ReadHeaderTimeout: 5 * time.Second, IdleTimeout: 60 * time.Second}
	go func() {
		<-ctx.Done()
		shutdown, done := context.WithTimeout(context.Background(), 5*time.Second)
		defer done()
		server.Shutdown(shutdown)
	}()
	log.Printf("Dispatcher listening on %s", server.Addr)
	if e = server.ListenAndServe(); e != nil && e != http.ErrServerClosed {
		log.Fatal(e)
	}
}
