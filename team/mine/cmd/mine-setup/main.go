package main

import (
	"context"
	"log"
	"time"

	"github.com/openegiz-contest/team-050/team/mine/domain"
	"github.com/openegiz-contest/team-050/team/mine/transport"
)

func main() {
	c, e := domain.LoadConfig(transport.Env("SITE_CONFIG", "config/site.json"))
	if e != nil {
		log.Fatal(e)
	}
	p := transport.NewPlatform()
	ctx := context.Background()
	things := domain.Snapshot(c, domain.NewState(c))
	for _, t := range things {
		for attempt := 0; ; attempt++ {
			_, status, e := p.Request(ctx, "GET", "/api/2/things/"+t.ID, nil)
			if status == 200 && e == nil {
				log.Printf("exists %s", t.ID)
				break
			}
			if status == 404 {
				_, _, e = p.Request(ctx, "PUT", "/api/2/things/"+t.ID, t)
				if e == nil {
					log.Printf("created %s", t.ID)
					break
				}
			}
			if attempt >= 30 {
				log.Fatalf("setup %s: %v", t.ID, e)
			}
			time.Sleep(2 * time.Second)
		}
	}
	log.Printf("%d twins ready", len(things))
}
