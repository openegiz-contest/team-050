package domain

import (
	"math"
)

const FieldWorkSeconds = 900.0
const HeavyWorkSeconds = 9000.0
const ServiceSpeed = 30.0
const TowSpeed = 20.0
const HitchSeconds = 300.0

// A vehicle follows actual road edges. The final edge may end at a stopped
// truck's fractional position. One service car shares each site's single crew.
type ServiceVehicle struct {
	ID          string   `json:"id"`
	Kind        string   `json:"kind"`
	Home        string   `json:"home_node"`
	Node        string   `json:"node_id"`
	State       string   `json:"state"`
	Target      string   `json:"target_truck_id"`
	Destination string   `json:"repair_node"`
	Path        []string `json:"travel_path"`
	Index       int      `json:"travel_index"`
	Progress    float64  `json:"edge_progress"`
	End         float64  `json:"end_progress"`
	Timer       float64  `json:"timer_s"`
	Completed   int      `json:"jobs_completed"`
	Busy        float64  `json:"busy_s"`
}

func (s *State) ensureVehicles() {
	if len(s.Vehicles) == 0 {
		s.Vehicles = []*ServiceVehicle{
			{ID: "service-01", Kind: "service", Home: "R1", Node: "R1", State: "idle"},
			{ID: "service-02", Kind: "service", Home: "R2", Node: "R2", State: "idle"},
			{ID: "tow-01", Kind: "tow", Home: "R1", Node: "R1", State: "idle"},
		}
	}
}
func (s *State) crewAway(node string) bool {
	for _, v := range s.Vehicles {
		if v.Kind == "service" && v.Home == node && v.State != "idle" {
			return true
		}
	}
	return false
}
func vehicleMoving(v *ServiceVehicle) bool {
	return (v.State == "outbound" || v.State == "towing" || v.State == "returning_base") && len(v.Path) > 1 && v.Index < len(v.Path)-1
}
func vehicleSpeed(v *ServiceVehicle) float64 {
	if v.State == "towing" {
		return RepairTravelSpeed
	}
	if v.Kind == "tow" {
		return TowSpeed
	}
	return ServiceSpeed
}
func vehicleEnd(v *ServiceVehicle) float64 {
	if v.Index == len(v.Path)-2 {
		return v.End
	}
	return 1
}
func pathDistance(c Config, p []string) float64 {
	d := 0.0
	for i := 1; i < len(p); i++ {
		d += c.EdgeLength(p[i-1], p[i])
	}
	return d
}
func approach(c Config, start string, t *Truck) ([]string, float64, float64) {
	p := c.ShortestPath(start, t.Node)
	end := 1.0
	if len(p) == 0 {
		return nil, 0, 0
	}
	distance := pathDistance(c, p)
	if onEdge(t) && t.Progress > 1e-8 {
		p = append(p, t.Path[t.Index+1])
		end = t.Progress
		distance += c.EdgeLength(t.Node, t.Path[t.Index+1]) * end
	}
	return p, end, distance
}
func setVehiclePath(v *ServiceVehicle, p []string, progress, end float64) {
	v.Path = p
	v.Index = 0
	v.Progress = progress
	v.End = end
}
func (s *State) dispatchVehicle(c Config, cmd Command, t *Truck) (string, string) {
	kind := "service"
	switch cmd.Type {
	case "dispatch_tow":
		kind = "tow"
		if t.State != "failed" || t.Failure != "heavy" {
			return "NOT_HEAVY_FAILURE", "Эвакуатор нужен для тяжёлой поломки"
		}
	case "dispatch_service":
		if t.State != "failed" || t.Failure != "light" {
			return "NOT_LIGHT_FAILURE", "Выездной ремонт доступен при лёгкой поломке"
		}
	case "preventive_service":
		if t.Failure != "" || t.Maintenance {
			return "TRUCK_UNAVAILABLE", "Машина уже на обслуживании или неисправна"
		}
	}
	var chosen *ServiceVehicle
	var path []string
	end := 1.0
	best := math.Inf(1)
	for _, v := range s.Vehicles {
		if v.Kind != kind || v.State != "idle" || (cmd.Parameters.VehicleID != "" && v.ID != cmd.Parameters.VehicleID) {
			continue
		}
		if kind == "service" {
			r := s.repairAt(v.Home)
			if r == nil || r.Current != "" || len(r.Queue) > 0 {
				continue
			}
		}
		p, e, d := approach(c, v.Node, t)
		if len(p) > 0 && d < best {
			chosen = v
			path = p
			end = e
			best = d
		}
	}
	if chosen == nil {
		return "NO_AVAILABLE_VEHICLE", "Нет свободной машины с бригадой. Дождитесь завершения работы или выберите другую"
	}
	destination := ""
	if kind == "tow" {
		best = math.Inf(1)
		for _, r := range s.Repairs {
			if cmd.Parameters.RepairNode != "" && r.Node != cmd.Parameters.RepairNode {
				continue
			}
			p, d := repairPath(c, t, r.Node)
			if len(p) > 0 && d < best {
				destination = r.Node
				best = d
			}
		}
		if destination == "" {
			return "NO_REPAIR_PATH", "Нет пути к ремонтной площадке"
		}
	}
	if cmd.Type == "preventive_service" {
		t.ResumeState = t.State
		s.releaseProduction(t)
		t.Maintenance = true
		t.State = "waiting_service"
	} else if kind == "service" {
		t.State = "waiting_service"
	} else {
		t.State = "waiting_tow"
		t.RepairNode = destination
	}
	chosen.State = "outbound"
	chosen.Target = t.ID
	chosen.Destination = destination
	setVehiclePath(chosen, path, 0, end)
	s.Log(chosen.ID + ": выезд к " + t.ID)
	// A dispatch at the same node still enters the work stage without a zero-time loop.
	s.finishVehicleEvents(c)
	return "", ""
}
func (s *State) vehicleBoundary(c Config) float64 {
	h := math.Inf(1)
	for _, v := range s.Vehicles {
		if vehicleMoving(v) {
			h = math.Min(h, math.Max(0, vehicleEnd(v)-v.Progress)*c.EdgeLength(v.Path[v.Index], v.Path[v.Index+1])*3600/vehicleSpeed(v))
		} else if v.Timer > 0 {
			h = math.Min(h, v.Timer)
		}
	}
	return h
}
func (s *State) advanceVehicles(c Config, h float64) {
	for _, v := range s.Vehicles {
		if v.State != "idle" {
			v.Busy += h
		}
		if vehicleMoving(v) {
			v.Progress += vehicleSpeed(v) * h / 3600 / c.EdgeLength(v.Path[v.Index], v.Path[v.Index+1])
		} else if v.Timer > 0 {
			v.Timer -= h
		}
		if v.State == "towing" {
			s.followTow(v)
		}
	}
}
func (s *State) followTow(v *ServiceVehicle) {
	if t := s.Truck(v.Target); t != nil {
		t.Node = v.Node
		t.Path = append([]string{}, v.Path...)
		t.Index = v.Index
		t.Progress = v.Progress
	}
}
func (s *State) returnVehicle(c Config, v *ServiceVehicle) {
	// Use the same remaining-edge rule as the trucks, preserving its exact position.
	location := &Truck{Node: v.Node, Path: v.Path, Index: v.Index, Progress: v.Progress}
	p, _ := repairPath(c, location, v.Home)
	progress := 0.0
	if onEdge(location) && location.Progress > 1e-8 {
		progress = location.Progress
	}
	v.State = "returning_base"
	v.Target = ""
	v.Destination = ""
	v.Timer = 0
	setVehiclePath(v, p, progress, 1)
	if len(p) <= 1 {
		v.Node = v.Home
		v.State = "idle"
		v.Path = nil
		v.Progress = 0
	}
}
func (s *State) resumeFieldTruck(c Config, t *Truck) {
	if t.Maintenance {
		t.PreventiveCompleted++
	} else {
		t.RepairsCompleted++
	}
	t.Maintenance = false
	t.Failure = ""
	t.Timer = 0
	if onEdge(t) {
		if t.ResumeState == "hauling" {
			t.State = "hauling"
		} else {
			t.State = "returning"
		}
	} else if t.ResumeState == "loading" || t.ResumeState == "positioning_loading" || t.ResumeState == "queued_loading" {
		t.Path = nil
		t.State = "queued_loading"
		for _, e := range s.Loaders {
			if e.Node == t.Node {
				e.Queue = append(e.Queue, t.ID)
			}
		}
	} else if t.ResumeState == "unloading" || t.ResumeState == "positioning_unloading" || t.ResumeState == "queued_unloading" {
		s.Arrive(c, t)
	} else {
		s.Travel(c, t, t.Payload > 0)
	}
	t.ResumeState = ""
	s.Log(t.ID + ": выездное обслуживание завершено, работа возобновлена на маршруте")
}
func (s *State) finishVehicleEvents(c Config) {
	for _, v := range s.Vehicles {
		if v.State == "idle" {
			continue
		}
		if vehicleMoving(v) {
			if v.Progress < vehicleEnd(v)-1e-8 {
				continue
			}
			v.Progress = vehicleEnd(v)
			if v.Progress >= 1-1e-8 {
				v.Index++
				v.Node = v.Path[v.Index]
				v.Progress = 0
			}
			// Reaching the fractional final point keeps the edge for return travel.
			if vehicleMoving(v) && !(v.Index == len(v.Path)-2 && v.Progress >= v.End-1e-8) {
				if v.State == "towing" {
					s.followTow(v)
				}
				continue
			}
		}
		if v.Timer > 1e-8 {
			continue
		}
		t := s.Truck(v.Target)
		switch v.State {
		case "outbound":
			if t == nil {
				s.returnVehicle(c, v)
				continue
			}
			// Arrival at a partial edge is a stationary vehicle; retain its location separately
			// by using Timer stages before considering that edge for movement again.
			if v.Kind == "tow" {
				v.State = "hitching"
				v.Timer = HitchSeconds
				t.State = "hitching"
			} else {
				v.State = "field_diagnosis"
				v.Timer = RepairDiagnosisSeconds
				t.State = "field_diagnosis"
			}
			s.Log(v.ID + ": прибыл к " + t.ID)
		case "field_diagnosis":
			v.State = "field_work"
			v.Timer = FieldWorkSeconds
			if t != nil {
				t.State = "field_work"
			}
		case "field_work":
			if t != nil {
				s.resumeFieldTruck(c, t)
			}
			v.Completed++
			s.returnVehicle(c, v)
		case "hitching":
			if t == nil {
				s.returnVehicle(c, v)
				continue
			}
			p, _ := repairPath(c, t, v.Destination)
			progress := 0.0
			if onEdge(t) {
				progress = t.Progress
			}
			setVehiclePath(v, p, progress, 1)
			v.State = "towing"
			t.State = "being_towed"
			s.followTow(v)
			if len(p) <= 1 {
				v.State = "unhitching"
				v.Timer = HitchSeconds
				t.State = "unhitching"
			}
		case "towing":
			s.followTow(v)
			v.State = "unhitching"
			v.Timer = HitchSeconds
			if t != nil {
				t.State = "unhitching"
			}
		case "unhitching":
			if t != nil {
				t.Node = v.Node
				s.arriveRepair(t)
			}
			v.Completed++
			s.returnVehicle(c, v)
		case "returning_base":
			v.Node = v.Home
			v.Path = nil
			v.Index = 0
			v.Progress = 0
			v.State = "idle"
			s.Log(v.ID + ": вернулся на базу")
		}
	}
}
