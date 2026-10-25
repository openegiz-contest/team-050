package domain

import (
	"fmt"
	"math"
	"sort"
)

// Initial modelling assumptions, in model seconds and km/h.
const RepairDiagnosisSeconds = 300.0
const RepairWorkSeconds = 3000.0
const RepairTravelSpeed = 10.0

type FailureEvent struct {
	Random   bool    `json:"random,omitempty"`
	Time     float64 `json:"sim_time_s"`
	TruckID  string  `json:"truck_id"`
	Severity string  `json:"severity,omitempty"`
}

type RepairSite struct {
	FuelCurrent   string   `json:"fuel_current_truck_id"`
	FuelQueue     []string `json:"fuel_queue"`
	FuelCompleted int      `json:"refuels_completed"`
	ID            string   `json:"id"`
	Node          string   `json:"node_id"`
	Current       string   `json:"current_truck_id"`
	Queue         []string `json:"queue"`
	Busy          float64  `json:"busy_s"`
	Idle          float64  `json:"idle_s"`
	Completed     int      `json:"repairs_completed"`
}

func (s *State) EnsureRepairs() {
	if len(s.Repairs) == 0 {
		s.Repairs = []*RepairSite{
			{ID: "repair-01", Node: "R1", Queue: []string{}},
			{ID: "repair-02", Node: "R2", Queue: []string{}},
		}
	}
	s.ensureVehicles()
}

func onEdge(t *Truck) bool { return len(t.Path) > 1 && t.Index < len(t.Path)-1 }
func truckSpeed(c Config, t *Truck) float64 {
	if t.State == "to_repair" {
		return RepairTravelSpeed
	}
	if t.Payload > 0 {
		return c.SpeedLoaded
	}
	return c.SpeedEmpty
}
func (s *State) repairAt(node string) *RepairSite {
	for _, r := range s.Repairs {
		if r.Node == node {
			return r
		}
	}
	return nil
}
func (s *State) releaseProduction(t *Truck) {
	s.RemoveFromQueues(t.ID)
	for _, l := range s.Loaders {
		if l.Current == t.ID {
			l.Current = ""
		}
	}
	for _, c := range s.Crushers {
		if c.Current == t.ID {
			c.Current = ""
			c.Reserved = 0
		}
	}
	t.Timer = 0
}

func (s *State) failTruck(t *Truck, severity string) {
	if severity == "" {
		severity = "medium"
	}
	t.ResumeState = t.State
	s.releaseProduction(t)
	t.Failure = severity
	t.FailureAt = s.Time
	t.Failures++
	t.State = "failed"
	name := map[string]string{"light": "лёгкая", "medium": "средняя", "heavy": "тяжёлая"}[severity]
	s.Log(t.ID + ": " + name + " неисправность; перевозка остановлена, груз сохранён")
}
func (s *State) replayFailures() {
	if !s.ReplayFailures {
		return
	}
	for s.FailureCursor < len(s.FailureScenario) && s.FailureScenario[s.FailureCursor].Time <= s.Time+1e-8 {
		event := s.FailureScenario[s.FailureCursor]
		s.FailureCursor++
		t := s.Truck(event.TruckID)
		if t != nil && t.Failure == "" && !t.Maintenance {
			s.failTruck(t, event.Severity)
		} else {
			s.Log(event.TruckID + ": повторный отказ пропущен — машина уже недоступна")
		}
	}
}

func repairPath(c Config, t *Truck, destination string) ([]string, float64) {
	if onEdge(t) && t.Progress > 1e-8 {
		from, to := t.Path[t.Index], t.Path[t.Index+1]
		tail := c.ShortestPath(to, destination)
		if len(tail) == 0 {
			return nil, 0
		}
		path := append([]string{from}, tail...)
		distance := c.EdgeLength(from, to) * (1 - t.Progress)
		for i := 1; i < len(tail); i++ {
			distance += c.EdgeLength(tail[i-1], tail[i])
		}
		return path, distance
	}
	path := c.ShortestPath(t.Node, destination)
	distance := 0.0
	for i := 1; i < len(path); i++ {
		distance += c.EdgeLength(path[i-1], path[i])
	}
	return path, distance
}

func (s *State) handleRepairCommand(c Config, cmd Command) ([]Result, bool) {
	if cmd.Type != "inject_failure" && cmd.Type != "send_to_repair" && cmd.Type != "prioritize_repair" && cmd.Type != "dispatch_service" && cmd.Type != "preventive_service" && cmd.Type != "dispatch_tow" {
		return nil, false
	}
	reply := func(status, reason, message string) ([]Result, bool) {
		return []Result{result(s, cmd, status, reason, message)}, true
	}
	if s.Mode == "finished" {
		return reply("rejected", "SHIFT_FINISHED", "Смена завершена")
	}
	if s.Mode == "ready" {
		return reply("rejected", "SHIFT_NOT_STARTED", "Сначала начните смену")
	}
	t := s.Truck(cmd.Target)
	if t == nil {
		return reply("rejected", "UNKNOWN_TRUCK", "Выберите самосвал")
	}
	s.EnsureRepairs()
	s.replayFailures()
	switch cmd.Type {
	case "inject_failure":
		if t.Failure != "" || t.Maintenance || fuelState(t) || recoveryState(t) {
			return reply("rejected", "ALREADY_FAILED", "Машина уже неисправна")
		}
		severity := cmd.Parameters.Severity
		if severity == "" {
			severity = "medium"
		}
		if severity != "light" && severity != "medium" && severity != "heavy" {
			return reply("rejected", "INVALID_SEVERITY", "Выберите лёгкую, среднюю или тяжёлую поломку")
		}
		s.failTruck(t, severity)
		s.FailureScenario = append(s.FailureScenario, FailureEvent{Time: s.Time, TruckID: t.ID, Severity: severity})
		sort.SliceStable(s.FailureScenario, func(i, j int) bool { return s.FailureScenario[i].Time < s.FailureScenario[j].Time })
		if s.ReplayFailures {
			s.FailureCursor++
		}
	case "dispatch_service", "preventive_service", "dispatch_tow":
		reason, message := s.dispatchVehicle(c, cmd, t)
		if reason != "" {
			return reply("rejected", reason, message)
		}
	case "send_to_repair":
		if t.State != "failed" || t.Failure != "medium" {
			return reply("rejected", "NOT_WAITING_REPAIR", "Направить можно машину, ожидающую решения механика")
		}
		var chosen *RepairSite
		var path []string
		distance := math.Inf(1)
		for _, r := range s.Repairs {
			if cmd.Parameters.RepairNode != "" && r.Node != cmd.Parameters.RepairNode {
				continue
			}
			candidate, d := repairPath(c, t, r.Node)
			if len(candidate) > 0 && d < distance {
				chosen = r
				path = candidate
				distance = d
			}
		}
		if chosen == nil {
			return reply("rejected", "NO_REPAIR_PATH", "Нет пути к выбранной ремонтной площадке")
		}
		progress := 0.0
		if onEdge(t) && t.Progress > 1e-8 {
			progress = t.Progress
		}
		t.RepairNode = chosen.Node
		t.Path = path
		t.Index = 0
		t.Progress = progress
		t.State = "to_repair"
		if len(path) <= 1 {
			s.arriveRepair(t)
		}
		s.Log(t.ID + ": механик направил на " + chosen.Node + " (" + fmt.Sprintf("%.2f км", distance) + ")")
	case "prioritize_repair":
		if t.State != "queued_repair" {
			return reply("rejected", "NOT_QUEUED_REPAIR", "Приоритет меняется только в очереди ремонта")
		}
		r := s.repairAt(t.RepairNode)
		if r == nil {
			return reply("rejected", "UNKNOWN_REPAIR_SITE", "Ремонтная площадка не найдена")
		}
		r.Queue = append([]string{t.ID}, remove(r.Queue, t.ID)...)
		s.Log(t.ID + ": механик повысил приоритет в очереди " + r.Node)
	}
	return reply("applied", "", "Команда выполнена")
}

func (s *State) arriveRepair(t *Truck) {
	t.Path = nil
	t.Index = 0
	t.Progress = 0
	t.State = "queued_repair"
	if r := s.repairAt(t.RepairNode); r != nil {
		r.Queue = append(r.Queue, t.ID)
	}
	s.Log(t.ID + ": прибыл на " + t.RepairNode + ", ожидает бригаду")
}
func (s *State) startRepairs() {
	for _, r := range s.Repairs {
		if r.Current != "" || s.crewAway(r.Node) || len(r.Queue) == 0 {
			continue
		}
		t := s.Truck(r.Queue[0])
		r.Queue = r.Queue[1:]
		if t == nil || t.State != "queued_repair" {
			continue
		}
		r.Current = t.ID
		t.State = "diagnosing"
		t.Timer = RepairDiagnosisSeconds
		s.Log(t.ID + ": диагностика на " + r.Node + " — 5 минут")
	}
}
func (s *State) finishRepair(c Config, t *Truck) []Result {
	if r := s.repairAt(t.RepairNode); r != nil {
		r.Current = ""
		r.Completed++
	}
	t.Failure = ""
	t.RepairNode = ""
	t.RepairsCompleted++
	t.Timer = 0
	s.Log(t.ID + ": ремонт завершён, возвращается к перевозке")
	if t.Pending != nil {
		return s.applyPending(c, t)
	}
	s.Travel(c, t, t.Payload > 0)
	return nil
}
