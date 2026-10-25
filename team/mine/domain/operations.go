package domain

import (
	"crypto/sha256"
	"encoding/binary"
	"fmt"
	"math"
	"sort"
)

// These are game-model assumptions, not manufacturer consumption or safety limits.
type OperationsConfig struct {
	LoadSpread    float64 `json:"load_spread_t"`
	Tank          float64 `json:"tank_l"`
	LoadedRate    float64 `json:"loaded_lph"`
	EmptyRate     float64 `json:"empty_lph"`
	IdleRate      float64 `json:"idle_lph"`
	WorkRate      float64 `json:"work_lph"`
	PumpRate      float64 `json:"pump_lpm"`
	FuelThreshold float64 `json:"refuel_fraction"`
	FailureMean   float64 `json:"failure_mean_seconds"`
	Protection    float64 `json:"prevention_protection_seconds"`
}

func defaultOperations() OperationsConfig {
	return OperationsConfig{10, 1800, 180, 120, 18, 70, 150, .25, 43200, 7200}
}

func (s *State) draw(key string) float64 {
	h := sha256.Sum256([]byte(s.RandomSeed + "/" + key))
	return (float64(binary.BigEndian.Uint64(h[:8])>>11) + .5) / 9007199254740992
}

// Also migrates old saves once, without replacing the run, cargo or repairs.
func (s *State) EnsureOperations(c Config) {
	if s.RandomSeed == "" {
		s.RandomSeed = s.RunID
	}
	for _, t := range s.Trucks {
		if !t.FuelInitialized {
			t.Fuel = c.Operations.Tank * (.55 + .2*s.draw(t.ID+"/initial-fuel"))
			t.InitialFuel = t.Fuel
			t.FuelInitialized = true
		}
		if t.LoadTarget == 0 && (t.State == "loading" || t.State == "positioning_loading") {
			t.LoadTarget = math.Max(t.Payload, s.loadTarget(c, t))
		}
	}
	if s.OperationsVersion == 0 {
		for _, t := range s.Trucks {
			at := 1800.0
			for i := 0; ; i++ {
				at += -math.Log(s.draw(fmt.Sprintf("%s/failure-time/%d", t.ID, i))) * c.Operations.FailureMean
				if at >= c.ShiftSeconds {
					break
				}
				if at <= s.Time {
					continue
				}
				u := s.draw(fmt.Sprintf("%s/failure-kind/%d", t.ID, i))
				severity := "light"
				if u >= .9 {
					severity = "heavy"
				} else if u >= .65 {
					severity = "medium"
				}
				s.RandomFailures = append(s.RandomFailures, FailureEvent{Time: at, TruckID: t.ID, Severity: severity, Random: true})
			}
		}
		sort.SliceStable(s.RandomFailures, func(i, j int) bool { return s.RandomFailures[i].Time < s.RandomFailures[j].Time })
		s.OperationsVersion = 1
	}
}

func (s *State) loadTarget(c Config, t *Truck) float64 {
	// Integer tonnes make the range clear to the operator; partial last bucket is metered.
	spread := int(c.Operations.LoadSpread)
	return math.Min(c.Body*c.Density, c.Capacity+float64(int(s.draw(fmt.Sprintf("%s/load/%d", t.ID, t.Trips))*float64(2*spread+1))-spread))
}

func (s *State) randomFailures(c Config) {
	for s.RandomCursor < len(s.RandomFailures) && s.RandomFailures[s.RandomCursor].Time <= s.Time+1e-8 {
		e := s.RandomFailures[s.RandomCursor]
		s.RandomCursor++
		t := s.Truck(e.TruckID)
		if t == nil || t.Failure != "" || t.Maintenance || fuelState(t) || t.State == "idle" {
			continue
		}
		if t.ProtectedUntil > s.Time {
			s.Log(t.ID + ": профилактика предотвратила случайный отказ")
			continue
		}
		s.failTruck(t, e.Severity)
		s.FailureScenario = append(s.FailureScenario, e)
		sort.SliceStable(s.FailureScenario, func(i, j int) bool { return s.FailureScenario[i].Time < s.FailureScenario[j].Time })
		if s.ReplayFailures {
			s.FailureCursor++
		}
	}
}

func fuelState(t *Truck) bool {
	return t.State == "to_refuel" || t.State == "queued_refuel" || t.State == "refueling" || t.State == "out_of_fuel"
}

func fuelRate(c Config, t *Truck) float64 {
	if (t.Failure != "" && t.State != "to_repair") || t.Maintenance || fuelState(t) && t.State != "to_refuel" || t.State == "idle" {
		return 0
	}
	if moving(t) {
		if t.Payload > 0 {
			return c.Operations.LoadedRate
		}
		return c.Operations.EmptyRate
	}
	if operating(t) {
		return c.Operations.WorkRate
	}
	return c.Operations.IdleRate
}

func (s *State) beginRefuel(c Config, t *Truck, site string) bool {
	if t.Failure != "" || t.Maintenance || fuelState(t) {
		return false
	}
	var best *RepairSite
	var path []string
	distance := math.Inf(1)
	for _, r := range s.Repairs {
		if site != "" && site != r.Node {
			continue
		}
		p, d := repairPath(c, t, r.Node)
		if len(p) > 0 && d < distance {
			best = r
			path = p
			distance = d
		}
	}
	if best == nil {
		return false
	}
	progress := 0.0
	if onEdge(t) {
		progress = t.Progress
	}
	s.releaseProduction(t)
	t.FuelNode = best.Node
	t.FuelRequested = false
	t.State = "to_refuel"
	t.Path = path
	t.Index = 0
	t.Progress = progress
	if len(path) <= 1 {
		s.arriveFuel(t)
	}
	s.Log(t.ID + ": направлен на заправку " + best.Node)
	return true
}

func (s *State) arriveFuel(t *Truck) {
	t.Path = nil
	t.Index = 0
	t.Progress = 0
	t.State = "queued_refuel"
	if r := s.repairAt(t.FuelNode); r != nil {
		r.FuelQueue = append(r.FuelQueue, t.ID)
	}
}

func (s *State) startFuel(c Config) {
	for _, r := range s.Repairs {
		if r.FuelCurrent != "" || len(r.FuelQueue) == 0 {
			continue
		}
		t := s.Truck(r.FuelQueue[0])
		r.FuelQueue = r.FuelQueue[1:]
		if t == nil || t.State != "queued_refuel" {
			continue
		}
		r.FuelCurrent = t.ID
		t.State = "refueling"
		t.Timer = (c.Operations.Tank - t.Fuel) / c.Operations.PumpRate * 60
	}
}

func (s *State) finishFuel(c Config, t *Truck) []Result {
	if r := s.repairAt(t.FuelNode); r != nil {
		r.FuelCurrent = ""
		r.FuelCompleted++
	}
	t.Refuels++
	t.Timer = 0
	t.FuelNode = ""
	s.Log(t.ID + ": заправлен, возвращается на маршрут")
	if t.Pending != nil {
		return s.applyPending(c, t)
	}
	s.Travel(c, t, t.Payload > 0)
	return nil
}

func (s *State) handleFuel(c Config, cmd Command) ([]Result, bool) {
	if cmd.Type != "send_to_refuel" {
		return nil, false
	}
	t := s.Truck(cmd.Target)
	reject := func(text string) ([]Result, bool) {
		return []Result{result(s, cmd, "rejected", "REFUEL_UNAVAILABLE", text)}, true
	}
	if t == nil || s.Mode == "ready" || s.Mode == "finished" {
		return reject("Выберите самосвал в начатой смене")
	}
	if t.Failure != "" || t.Maintenance || fuelState(t) {
		return reject("Машина уже недоступна или заправляется")
	}
	if cmd.Parameters.RepairNode != "" && s.repairAt(cmd.Parameters.RepairNode) == nil {
		return reject("Неизвестная заправочная площадка")
	}
	// Finish the current bucket/dump before leaving production equipment.
	if moving(t) || operating(t) {
		t.FuelRequested = true
		t.FuelNode = cmd.Parameters.RepairNode
		return []Result{result(s, cmd, "applied", "", "Заправка назначена после завершения операции или на ближайшем узле")}, true
	}
	if !s.beginRefuel(c, t, cmd.Parameters.RepairNode) {
		return reject("Нет пути к заправке")
	}
	return []Result{result(s, cmd, "applied", "", "Машина направлена на заправку")}, true
}

// Separate units from duration metrics; never label tonnes as seconds in reports.
type LoadStats struct {
	Count int     `json:"count"`
	Total float64 `json:"total_t"`
	Min   float64 `json:"min_t"`
	Max   float64 `json:"max_t"`
}

func (v *LoadStats) Add(m float64) {
	if v.Count == 0 || m < v.Min {
		v.Min = m
	}
	if m > v.Max {
		v.Max = m
	}
	v.Count++
	v.Total += m
}
func (s *State) LoadingSummary() map[string]any {
	v := LoadStats{}
	for _, t := range s.Trucks {
		a := t.LoadMass
		if a.Count == 0 {
			continue
		}
		if v.Count == 0 || a.Min < v.Min {
			v.Min = a.Min
		}
		if a.Max > v.Max {
			v.Max = a.Max
		}
		v.Count += a.Count
		v.Total += a.Total
	}
	mean := 0.0
	if v.Count > 0 {
		mean = v.Total / float64(v.Count)
	}
	return map[string]any{"count": v.Count, "total_t": v.Total, "min_t": v.Min, "max_t": v.Max, "mean_t": mean}
}
func (s *State) FuelSummary() map[string]float64 {
	initial, added, used, remaining, wait, stops := 0.0, 0.0, 0.0, 0.0, 0.0, 0.0
	for _, t := range s.Trucks {
		initial += t.InitialFuel
		added += t.FuelAdded
		used += t.FuelConsumed
		remaining += t.Fuel
		wait += t.FuelWait
		stops += t.FuelDowntime
	}
	return map[string]float64{"initial_l": initial, "added_l": added, "consumed_l": used, "remaining_l": remaining, "wait_s": wait, "downtime_s": stops}
}
