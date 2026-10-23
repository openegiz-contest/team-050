package domain

import "encoding/json"

type Properties struct {
	Value any    `json:"value"`
	Unit  string `json:"unit,omitempty"`
}
type Feature struct {
	Properties Properties `json:"properties"`
}
type Features map[string]Feature
type Thing struct {
	ID         string         `json:"thingId"`
	Policy     string         `json:"policyId"`
	Attributes map[string]any `json:"attributes"`
	Features   Features       `json:"features"`
}

func JSON(v any) string                             { b, _ := json.Marshal(v); return string(b) }
func (f Features) Set(k string, v any, unit string) { f[k] = Feature{Properties{v, unit}} }
func Snapshot(c Config, s *State) []Thing {
	out := []Thing{}
	add := func(name, kind string, attrs map[string]any, f Features) {
		if attrs == nil {
			attrs = map[string]any{}
		}
		attrs["name"] = name
		attrs["kind"] = kind
		attrs["_isType"] = false
		if name != "mine-01" {
			attrs["_parents"] = ThingID("mine-01")
		}
		f.Set("idSimulationRun", s.RunID, "")
		f.Set("snapshot_seq", s.Seq, "")
		f.Set("sim_time_s", s.Time, "s")
		out = append(out, Thing{ThingID(name), "default:basic_policy", attrs, f})
	}
	loaded, delivered, processed, payload, buffer := s.Totals()
	f := Features{}
	for k, v := range map[string]float64{"loaded_t": loaded, "delivered_t": delivered, "processed_t": processed, "truck_payload_total_t": payload, "buffer_total_t": buffer, "plan_t": 0} {
		f.Set(k, v, "t")
	}
	f.Set("plan_defined", false, "")
	f.Set("plan_completion_pct", 0, "%")
	f.Set("active_trucks", len(s.Trucks), "")
	f.Set("events_json", JSON(s.Events), "")
	report := ""
	if len(s.Report) > 0 {
		report = string(s.Report)
	}
	f.Set("run_report_json", report, "")
	ids := []string{ThingID("mine-01"), ThingID("control-01"), ThingID("roads-01")}
	for _, t := range s.Trucks {
		ids = append(ids, ThingID(t.ID))
	}
	for _, e := range s.Loaders {
		ids = append(ids, ThingID(e.ID))
	}
	for _, e := range s.Crushers {
		ids = append(ids, ThingID(e.ID))
	}
	add("mine-01", "mine", map[string]any{"manifest": ids, "config_hash": c.Hash}, f)
	f = Features{}
	f.Set("state", s.Mode, "")
	f.Set("speedup", s.Speed, "")
	f.Set("shift_duration_s", c.ShiftSeconds, "s")
	f.Set("config_hash", c.Hash, "")
	f.Set("last_command_id", s.LastCommand, "")
	f.Set("last_command_status", s.LastStatus, "")
	add("control-01", "control", nil, f)
	f = Features{}
	f.Set("topology_version", c.TopologyVersion, "")
	f.Set("closed_edges_json", "[]", "")
	add("roads-01", "roads", map[string]any{"topology": c}, f)
	for _, t := range s.Trucks {
		f = Features{}
		f.Set("state", t.State, "")
		f.Set("node_id", t.Node, "")
		f.Set("payload_t", t.Payload, "t")
		f.Set("volume_m3", t.Volume, "m3")
		edge := ""
		speed := 0.0
		if moving(t) {
			edge = t.Path[t.Index] + ">" + t.Path[t.Index+1]
			speed = c.SpeedEmpty
			if t.Payload > 0 {
				speed = c.SpeedLoaded
			}
		}
		if s.Mode != "running" {
			speed = 0
		}
		f.Set("edge_id", edge, "")
		f.Set("edge_progress", t.Progress, "")
		f.Set("speed_kmh", speed, "km/h")
		route := ""
		if t.Plan != nil {
			route = t.Plan.ID
		}
		f.Set("route_id", route, "")
		f.Set("route_json", JSON(t.Plan), "")
		f.Set("pending_route_json", JSON(t.Pending), "")
		f.Set("route_revision", t.Revision, "")
		f.Set("trips_total", t.Trips, "")
		f.Set("queue_wait_s", t.QueueWait, "s")
		f.Set("operation_elapsed_s", t.Timer, "s")
		f.Set("metrics_json", JSON(map[string]any{"loading": t.Loading, "unloading": t.Unloading, "cycles": t.Cycles}), "")
		add(t.ID, "haul-truck", map[string]any{"model": "CAT 793D", "capacity_t": c.Capacity, "body_m3": c.Body}, f)
	}
	for _, e := range s.Loaders {
		f = Features{}
		state := "idle"
		if e.Current != "" {
			state = "loading"
		}
		f.Set("state", state, "")
		f.Set("current_truck_id", e.Current, "")
		f.Set("queue_json", JSON(e.Queue), "")
		f.Set("queue_length", len(e.Queue), "")
		f.Set("loaded_t", e.Loaded, "t")
		f.Set("passes_total", e.Passes, "")
		f.Set("busy_s", e.Busy, "s")
		f.Set("idle_s", e.Idle, "s")
		add(e.ID, "excavator", map[string]any{"node_id": e.Node, "model": "Hitachi EX5600-6", "bucket_m3": c.Bucket}, f)
	}
	for i, e := range s.Crushers {
		f = Features{}
		state := "starved"
		if e.Buffer > 1e-8 {
			state = "crushing"
		}
		f.Set("state", state, "")
		f.Set("current_truck_id", e.Current, "")
		f.Set("queue_json", JSON(e.Queue), "")
		f.Set("queue_length", len(e.Queue), "")
		f.Set("buffer_t", e.Buffer, "t")
		f.Set("reserved_buffer_t", e.Reserved, "t")
		f.Set("delivered_t", e.Delivered, "t")
		f.Set("processed_t", e.Processed, "t")
		f.Set("capacity_tph", s.Capacity(c, i), "t/h")
		rate := 0.0
		if s.Time > 0 {
			rate = e.Processed * 3600 / s.Time
		}
		f.Set("throughput_tph", rate, "t/h")
		f.Set("busy_s", e.Busy, "s")
		f.Set("starved_s", e.Starved, "s")
		add(e.ID, "crusher", map[string]any{"node_id": e.Node, "buffer_capacity_t": c.BufferCapacity}, f)
	}
	return out
}
