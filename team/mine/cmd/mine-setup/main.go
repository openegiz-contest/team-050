package main

import (
	"context"
	"encoding/json"
	"log"
	"os"
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
	// Dedicated outbound channel needs no extraFields lookup. Every feature
	// event already carries the complete frame emitted by our engine.
	admin := transport.NewPlatform()
	admin.User = "devops"
	admin.Password = os.Getenv("DITTO_DEVOPS_PASSWORD")
	if admin.Password == "" {
		log.Fatal("DITTO_DEVOPS_PASSWORD is required to configure the team MQTT target")
	}
	b, e := os.ReadFile(transport.Env("MQTT_TARGET_CONFIG", "config/mqtt-target.json"))
	if e != nil {
		log.Fatal(e)
	}
	var connection any
	if e = json.Unmarshal(b, &connection); e != nil {
		log.Fatal(e)
	}
	for attempt := 0; ; attempt++ {
		_, _, e = admin.Request(ctx, "PUT", "/api/2/connections/team050-target", connection)
		if e == nil {
			break
		}
		if attempt >= 30 {
			log.Fatalf("team MQTT target: %v", e)
		}
		time.Sleep(2 * time.Second)
	}
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
