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
	configureTeamStand(ctx, admin)
	things := domain.Snapshot(c, domain.NewState(c))
	for _, t := range things {
		for attempt := 0; ; attempt++ {
			_, status, e := p.Request(ctx, "GET", "/api/2/things/"+t.ID, nil)
			if status == 200 && e == nil {
				if domain.Name(t.ID) == "mine-01" {
					_, _, e = p.Request(ctx, "PUT", "/api/2/things/"+t.ID+"/attributes/manifest", t.Attributes["manifest"])
					if e != nil {
						log.Fatal(e)
					}
				}
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

// The team's own target carries the complete state and live commands. The
// generic target performs redundant extraFields lookups and can saturate a
// small local stand. Keep it available through an explicit opt-in for shared use.
func configureTeamStand(ctx context.Context, admin *transport.Platform) {
	if _, _, err := admin.Request(ctx, "PUT", "/devops/logging", map[string]string{"logger": "org.eclipse.ditto", "level": "warn"}); err != nil {
		log.Printf("configure stand logging: %v", err)
	}
	raw, status, err := admin.Request(ctx, "GET", "/api/2/connections/mosquitto-target-connection", nil)
	if status == 404 {
		return
	}
	if err != nil {
		log.Printf("read legacy MQTT target: %v", err)
		return
	}
	var config map[string]any
	if err := json.Unmarshal(raw, &config); err != nil {
		log.Printf("decode legacy MQTT target: %v", err)
		return
	}
	desired := "closed"
	if transport.Env("TEAM_KEEP_LEGACY_TARGET", "0") == "1" {
		desired = "open"
	}
	if config["connectionStatus"] == desired {
		return
	}
	config["connectionStatus"] = desired
	if _, _, err := admin.Request(ctx, "PUT", "/api/2/connections/mosquitto-target-connection", config); err != nil {
		log.Printf("configure legacy MQTT target: %v", err)
	} else {
		log.Printf("legacy MQTT target: %s (team050-target remains open)", desired)
	}
}
