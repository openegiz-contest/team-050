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
	for k, v := range map[string]float64{"loaded_t": loaded, "delivered_t": delivered, "processed_t": processed, "truck_payload_total_t": payload, "buffer_total_t": buffer, "plan_t": s.PlanTonnes} {
		f.Set(k, v, "t")
	}
	f.Set("loading_summary_json", JSON(s.LoadingSummary()), "")
	f.Set("fuel_summary_json", JSON(s.FuelSummary()), "")
	f.Set("plan_defined", s.PlanTonnes > 0, "")
	completion := 0.0
	if s.PlanTonnes > 0 {
		completion = processed / s.PlanTonnes * 100
	}
	f.Set("plan_completion_pct", completion, "%")
	f.Set("active_trucks", len(s.Trucks), "")
	unavailable := 0
	downtime := 0.0
	for _, t := range s.Trucks {
		if t.Failure != "" || t.Maintenance || fuelState(t) {
			unavailable++
		}
		downtime += t.Downtime
	}
	failed := 0
	for _, t := range s.Trucks {
		if t.Failure != "" {
			failed++
		}
	}
	f.Set("failed_trucks", failed, "")
	f.Set("available_trucks", len(s.Trucks)-unavailable, "")
	f.Set("failure_downtime_s", downtime, "s")
	f.Set("failure_scenario_json", JSON(s.FailureScenario), "")
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
	for _, r := range s.Repairs {
		ids = append(ids, ThingID(r.ID))
	}
	for _, v := range s.Vehicles {
		ids = append(ids, ThingID(v.ID))
	}
	add("mine-01", "mine", map[string]any{"manifest": ids, "config_hash": c.Hash}, f)
	f = Features{}
	f.Set("state", s.Mode, "")
	f.Set("speedup", s.Speed, "")
	f.Set("shift_duration_s", c.ShiftSeconds, "s")
	f.Set("config_hash", c.Hash, "")
	f.Set("last_command_id", s.LastCommand, "")
	f.Set("last_command_status", s.LastStatus, "")
	f.Set("roles_json", JSON([]string{"dispatcher", "mechanic"}), "")
	f.Set("model_version", "4-fuel-random-load", "")
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
		f.Set("load_target_t", t.LoadTarget, "t")
		f.Set("load_stats_json", JSON(t.LoadMass), "")
		f.Set("fuel_l", t.Fuel, "l")
		f.Set("fuel_capacity_l", c.Operations.Tank, "l")
		f.Set("fuel_consumed_l", t.FuelConsumed, "l")
		f.Set("fuel_added_l", t.FuelAdded, "l")
		f.Set("fuel_wait_s", t.FuelWait, "s")
		f.Set("fuel_downtime_s", t.FuelDowntime, "s")
		f.Set("refuels_total", t.Refuels, "")
		f.Set("fuel_requested", t.FuelRequested, "")
		f.Set("fuel_node", t.FuelNode, "")
		f.Set("protected_until_s", t.ProtectedUntil, "s")
		edge := ""
		speed := 0.0
		if onEdge(t) {
			edge = t.Path[t.Index] + ">" + t.Path[t.Index+1]
		}
		if t.State == "being_towed" {
			speed = RepairTravelSpeed
		}
		if moving(t) {
			speed = truckSpeed(c, t)
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
		remaining := t.Timer
		for _, v := range s.Vehicles {
			if v.Target == t.ID && v.Timer > 0 {
				remaining = v.Timer
			}
		}
		f.Set("operation_elapsed_s", remaining, "s")
		f.Set("failure_severity", t.Failure, "")
		f.Set("failure_at_s", t.FailureAt, "s")
		f.Set("repair_node", t.RepairNode, "")
		f.Set("failures_total", t.Failures, "")
		f.Set("repairs_completed", t.RepairsCompleted, "")
		f.Set("failure_downtime_s", t.Downtime, "s")
		f.Set("repair_wait_s", t.RepairWait, "s")
		f.Set("maintenance", t.Maintenance, "")
		f.Set("preventive_completed", t.PreventiveCompleted, "")
		f.Set("maintenance_downtime_s", t.MaintenanceDowntime, "s")
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
	for _, r := range s.Repairs {
		f = Features{}
		state := "idle"
		if t := s.Truck(r.Current); t != nil {
			state = t.State
		}
		if s.crewAway(r.Node) {
			state = "crew_away"
		}
		f.Set("state", state, "")
		f.Set("current_truck_id", r.Current, "")
		f.Set("queue_json", JSON(r.Queue), "")
		f.Set("queue_length", len(r.Queue), "")
		f.Set("busy_s", r.Busy, "s")
		f.Set("idle_s", r.Idle, "s")
		f.Set("repairs_completed", r.Completed, "")
		f.Set("fuel_current_truck_id", r.FuelCurrent, "")
		f.Set("fuel_queue_json", JSON(r.FuelQueue), "")
		f.Set("fuel_queue_length", len(r.FuelQueue), "")
		f.Set("refuels_completed", r.FuelCompleted, "")
		add(r.ID, "repair-site", map[string]any{"node_id": r.Node, "crews": 1, "diagnosis_s": RepairDiagnosisSeconds, "repair_s": RepairWorkSeconds, "travel_speed_kmh": RepairTravelSpeed}, f)
	}
	for _, v := range s.Vehicles {
		f = Features{}
		f.Set("state", v.State, "")
		f.Set("node_id", v.Node, "")
		f.Set("target_truck_id", v.Target, "")
		edge := ""
		if len(v.Path) > 1 && v.Index < len(v.Path)-1 {
			edge = v.Path[v.Index] + ">" + v.Path[v.Index+1]
		}
		f.Set("edge_id", edge, "")
		f.Set("edge_progress", v.Progress, "")
		speed := 0.0
		if vehicleMoving(v) && s.Mode == "running" {
			speed = vehicleSpeed(v)
		}
		f.Set("speed_kmh", speed, "km/h")
		f.Set("operation_remaining_s", v.Timer, "s")
		f.Set("jobs_completed", v.Completed, "")
		f.Set("busy_s", v.Busy, "s")
		f.Set("repair_node", v.Destination, "")
		add(v.ID, v.Kind+"-vehicle", map[string]any{"home_node": v.Home, "crew_shared_with_site": v.Kind == "service"}, f)
	}
	return out
}
