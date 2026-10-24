package domain

import (
	"crypto/rand"
	"encoding/binary"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"math"
)

type Metric struct {
	Count int     `json:"count"`
	Total float64 `json:"total_s"`
	Min   float64 `json:"min_s"`
	Max   float64 `json:"max_s"`
}

func (m *Metric) Add(v float64) {
	if m.Count == 0 || v < m.Min {
		m.Min = v
	}
	if v > m.Max {
		m.Max = v
	}
	m.Count++
	m.Total += v
}

type Event struct {
	ID   int     `json:"id"`
	Time float64 `json:"sim_time_s"`
	Text string  `json:"text"`
}
type Plan struct {
	ID    string   `json:"id"`
	Nodes []string `json:"nodes"`
}
type Truck struct {
	ID               string   `json:"id"`
	State            string   `json:"state"`
	Node             string   `json:"node_id"`
	Payload          float64  `json:"payload_t"`
	Volume           float64  `json:"volume_m3"`
	Timer            float64  `json:"timer_s"`
	Plan             *Plan    `json:"plan"`
	Pending          *Plan    `json:"pending"`
	PendingCommand   string   `json:"pending_command"`
	PendingRun       string   `json:"pending_run"`
	Revision         int      `json:"route_revision"`
	Path             []string `json:"travel_path"`
	Index            int      `json:"travel_index"`
	Progress         float64  `json:"edge_progress"`
	Trips            int      `json:"trips_total"`
	QueueWait        float64  `json:"queue_wait_s"`
	LoadStarted      float64  `json:"load_started"`
	UnloadStarted    float64  `json:"unload_started"`
	CycleStart       float64  `json:"cycle_start"`
	HasCycle         bool     `json:"has_cycle"`
	Loading          Metric   `json:"loading"`
	Unloading        Metric   `json:"unloading"`
	Cycles           Metric   `json:"cycles"`
	Failure          string   `json:"failure_severity"`
	FailureAt        float64  `json:"failure_at_s"`
	RepairNode       string   `json:"repair_node"`
	Failures         int      `json:"failures_total"`
	RepairsCompleted int      `json:"repairs_completed"`
	Downtime         float64  `json:"failure_downtime_s"`
	RepairWait       float64  `json:"repair_wait_s"`
}
type Loader struct {
	ID      string   `json:"id"`
	Node    string   `json:"node_id"`
	Current string   `json:"current_truck_id"`
	Queue   []string `json:"queue"`
	Loaded  float64  `json:"loaded_t"`
	Passes  int      `json:"passes_total"`
	Busy    float64  `json:"busy_s"`
	Idle    float64  `json:"idle_s"`
}
type Crusher struct {
	ID        string   `json:"id"`
	Node      string   `json:"node_id"`
	Current   string   `json:"current_truck_id"`
	Queue     []string `json:"queue"`
	Buffer    float64  `json:"buffer_t"`
	Reserved  float64  `json:"reserved_buffer_t"`
	Delivered float64  `json:"delivered_t"`
	Processed float64  `json:"processed_t"`
	Busy      float64  `json:"busy_s"`
	Starved   float64  `json:"starved_s"`
}
type State struct {
	RunID           string           `json:"run_id"`
	ConfigHash      string           `json:"config_hash"`
	Time            float64          `json:"sim_time_s"`
	Seq             uint64           `json:"snapshot_seq"`
	Mode            string           `json:"state"`
	Speed           int              `json:"speedup"`
	PlanTonnes      float64          `json:"plan_t"`
	Trucks          []*Truck         `json:"trucks"`
	Loaders         []*Loader        `json:"loaders"`
	Crushers        []*Crusher       `json:"crushers"`
	Repairs         []*RepairSite    `json:"repair_sites"`
	FailureScenario []FailureEvent   `json:"failure_scenario"`
	ReplayFailures  bool             `json:"replay_failures"`
	FailureCursor   int              `json:"failure_cursor"`
	Events          []Event          `json:"events"`
	EventSeq        int              `json:"event_seq"`
	LastCommand     string           `json:"last_command_id"`
	LastStatus      string           `json:"last_command_status"`
	InitialPlans    map[string]*Plan `json:"initial_plans"`
	Report          json.RawMessage  `json:"report"`
}
type Command struct {
	Schema           int    `json:"schema_version"`
	ID               string `json:"command_id"`
	RunID            string `json:"run_id"`
	Type             string `json:"type"`
	Target           string `json:"target_id"`
	ExpectedRevision int    `json:"expected_route_revision"`
	Topology         string `json:"topology_version"`
	Parameters       struct {
		RouteID    string   `json:"route_id"`
		LoadedPath []string `json:"loaded_path"`
		Speed      int      `json:"speedup"`
		RepairNode string   `json:"repair_node,omitempty"`
	} `json:"parameters"`
	Role string `json:"role,omitempty"`
}
type Result struct {
	ID      string  `json:"command_id"`
	RunID   string  `json:"run_id"`
	Status  string  `json:"status"`
	Reason  string  `json:"reason_code"`
	Message string  `json:"message"`
	Time    float64 `json:"sim_time_s"`
	Seq     uint64  `json:"snapshot_seq"`
}

func NewState(c Config) *State {
	b := make([]byte, 12)
	if _, e := rand.Read(b); e != nil {
		panic(e)
	}
	s := &State{RunID: hex.EncodeToString(b), ConfigHash: c.Hash, Mode: "ready", Speed: 30, PlanTonnes: float64(93000 + binary.BigEndian.Uint32(b[:4])%2001), InitialPlans: map[string]*Plan{}}
	for i := 1; i <= c.TruckCount; i++ {
		s.Trucks = append(s.Trucks, &Truck{ID: fmt.Sprintf("truck-%02d", i), State: "idle", Node: "P"})
	}
	for i := 1; i <= 4; i++ {
		s.Loaders = append(s.Loaders, &Loader{ID: fmt.Sprintf("excavator-%02d", i), Node: fmt.Sprintf("E%d", i), Queue: []string{}})
	}
	for i := 1; i <= 3; i++ {
		s.Crushers = append(s.Crushers, &Crusher{ID: fmt.Sprintf("crusher-%02d", i), Node: fmt.Sprintf("C%d", i), Queue: []string{}})
	}
	s.Log("Смена подготовлена. Назначьте маршруты машинам в гараже.")
	s.EnsureRepairs()
	return s
}
func (s *State) Truck(id string) *Truck {
	for _, t := range s.Trucks {
		if t.ID == Name(id) {
			return t
		}
	}
	return nil
}
func (s *State) Log(text string) {
	s.EventSeq++
	s.Events = append(s.Events, Event{s.EventSeq, s.Time, text})
	if len(s.Events) > 60 {
		s.Events = s.Events[len(s.Events)-60:]
	}
}
func (s *State) Capacity(c Config, index int) float64 {
	switch index {
	case 0:
		return c.CrusherBase * 14 / 15
	case 1:
		return c.CrusherBase * 15.7 / 15
	default:
		if int(s.Time/c.CrusherSwitch)%2 == 0 {
			return c.CrusherBase * .98
		}
		return c.CrusherBase * 1.02
	}
}
func (s *State) Totals() (loaded, delivered, processed, payload, buffer float64) {
	for _, e := range s.Loaders {
		loaded += e.Loaded
	}
	for _, e := range s.Crushers {
		delivered += e.Delivered
		processed += e.Processed
		buffer += e.Buffer
	}
	for _, t := range s.Trucks {
		payload += t.Payload
	}
	return
}
func (s *State) ReportBytes(c Config) []byte {
	l, d, p, t, b := s.Totals()
	out := map[string]any{"schema_version": 1, "run_id": s.RunID, "config_hash": c.Hash, "config": c, "sim_time_s": s.Time, "state": s.Mode, "plan_defined": s.PlanTonnes > 0, "plan_t": s.PlanTonnes, "plan_completion_pct": func() float64 {
		if s.PlanTonnes > 0 {
			return p / s.PlanTonnes * 100
		}
		return 0
	}(), "loaded_t": l, "delivered_t": d, "processed_t": p, "payload_remaining_t": t, "buffer_remaining_t": b, "initial_plans": s.InitialPlans, "trucks": s.Trucks, "loaders": s.Loaders, "crushers": s.Crushers, "repair_sites": s.Repairs, "failure_scenario": s.FailureScenario, "replay_failures": s.ReplayFailures, "repair_assumptions": map[string]float64{"diagnosis_s": RepairDiagnosisSeconds, "repair_s": RepairWorkSeconds, "travel_speed_kmh": RepairTravelSpeed}}
	data, _ := json.Marshal(out)
	return data
}
func (c Config) ResolvePlan(cmd Command) (*Plan, error) {
	p := &Plan{ID: "custom", Nodes: cmd.Parameters.LoadedPath}
	if cmd.Parameters.RouteID != "" {
		p = nil
		for _, r := range c.Routes {
			if r.ID == cmd.Parameters.RouteID {
				p = &Plan{ID: r.ID, Nodes: append([]string{}, r.Nodes...)}
			}
		}
		if p == nil {
			return nil, fmt.Errorf("Неизвестный шаблон")
		}
	}
	if _, e := c.ValidatePath(p.Nodes); e != nil {
		return nil, e
	}
	return p, nil
}
func result(s *State, cmd Command, status, reason, text string) Result {
	return Result{cmd.ID, cmd.RunID, status, reason, text, s.Time, s.Seq + 1}
}
func (s *State) Handle(c Config, cmd Command) ([]Result, error) {
	reject := func(reason, text string) ([]Result, error) {
		return []Result{result(s, cmd, "rejected", reason, text)}, nil
	}
	if cmd.Schema != 1 || len(cmd.ID) < 8 || len(cmd.ID) > 100 {
		return reject("INVALID_COMMAND", "Некорректный идентификатор команды")
	}
	if cmd.RunID != s.RunID {
		return reject("STALE_RUN", "Смена уже изменилась. Обновите страницу.")
	}
	role := cmd.Role
	if role == "" {
		role = "dispatcher"
	}
	required := "dispatcher"
	if cmd.Type == "send_to_repair" || cmd.Type == "prioritize_repair" {
		required = "mechanic"
	}
	if role != required {
		return reject("WRONG_ROLE", "Команда доступна роли: "+required)
	}
	if rs, handled := s.handleRepairCommand(c, cmd); handled {
		return rs, nil
	}
	var extra []Result
	if cmd.Type == "repeat_shift" {
		if s.Mode == "running" {
			return reject("SHIFT_RUNNING", "Сначала поставьте смену на паузу")
		}
		for _, t := range s.Trucks {
			if t.Pending != nil {
				extra = append(extra, Result{t.PendingCommand, t.PendingRun, "cancelled", "REPEATED", "Создан повтор смены", s.Time, s.Seq + 1})
			}
		}
		n := NewState(c)
		n.FailureScenario = append([]FailureEvent{}, s.FailureScenario...)
		n.ReplayFailures = len(n.FailureScenario) > 0
		// A repeat compares dispatching decisions under the same shift target.
		if s.PlanTonnes > 0 {
			n.PlanTonnes = s.PlanTonnes
		}
		for _, t := range n.Trucks {
			if p := s.InitialPlans[t.ID]; p != nil {
				t.Plan = &Plan{ID: p.ID, Nodes: append([]string{}, p.Nodes...)}
				n.InitialPlans[t.ID] = t.Plan
			}
		}
		*s = *n
		s.Log("Повтор: исходные назначения восстановлены, машины в гараже.")
	} else if cmd.Type == "set_speed" {
		v := cmd.Parameters.Speed
		if v != 1 && v != 10 && v != 30 && v != 60 {
			return reject("INVALID_SPEED", "Доступны ×1, ×10, ×30 и ×60")
		}
		s.Speed = v
	} else if cmd.Type == "start_shift" || cmd.Type == "resume_shift" {
		if s.Mode == "finished" {
			return reject("SHIFT_FINISHED", "Смена завершена. Создайте повтор.")
		}
		s.Mode = "running"
		for _, t := range s.Trucks {
			if t.State == "idle" && t.Plan != nil {
				s.Travel(c, t, false)
			}
		}
		s.Log("Смена запущена.")
	} else if cmd.Type == "pause_shift" {
		if s.Mode == "finished" {
			return reject("SHIFT_FINISHED", "Смена завершена")
		}
		s.Mode = "paused"
		s.Log("Смена на паузе.")
	} else if cmd.Type == "assign_route" {
		if s.Mode == "finished" {
			return reject("SHIFT_FINISHED", "Смена завершена")
		}
		t := s.Truck(cmd.Target)
		if t == nil {
			return reject("UNKNOWN_TRUCK", "Самосвал не найден")
		}
		if t.Failure != "" {
			return reject("TRUCK_UNAVAILABLE", "Машина неисправна. Измените маршрут после ремонта.")
		}
		if cmd.Topology != c.TopologyVersion {
			return reject("STALE_MAP", "Карта изменилась")
		}
		if cmd.ExpectedRevision != t.Revision {
			return reject("STALE_ROUTE", "Назначение уже изменилось. Повторите выбор.")
		}
		p, e := c.ResolvePlan(cmd)
		if e != nil {
			return reject("INVALID_PATH", e.Error())
		}
		if t.Pending != nil {
			extra = append(extra, Result{t.PendingCommand, t.PendingRun, "superseded", "NEW_ASSIGNMENT", "Назначен более новый путь", s.Time, s.Seq + 1})
		}
		t.Revision++
		if s.Time == 0 && (s.Mode == "ready" || s.Mode == "paused") {
			s.InitialPlans[t.ID] = p
		}
		if moving(t) || operating(t) {
			t.Pending = p
			t.PendingCommand = cmd.ID
			t.PendingRun = cmd.RunID
			s.Log(t.ID + ": новый путь ожидает безопасной точки")
			return append(extra, result(s, cmd, "scheduled", "", "Маршрут применится в ближайшей допустимой точке")), nil
		}
		s.RemoveFromQueues(t.ID)
		t.Plan = p
		t.Pending = nil
		t.PendingCommand = ""
		if s.Mode != "ready" {
			s.Travel(c, t, t.Payload > 0)
		}
		s.Log(t.ID + ": назначен " + p.ID)
	} else {
		return reject("UNKNOWN_COMMAND", "Команда не поддерживается")
	}
	s.LastCommand = cmd.ID
	s.LastStatus = "applied"
	return append(extra, result(s, cmd, "applied", "", "Команда выполнена")), nil
}
func moving(t *Truck) bool {
	return (t.State == "hauling" || t.State == "returning" || t.State == "to_repair") && onEdge(t)
}
func operating(t *Truck) bool {
	return t.State == "positioning_loading" || t.State == "loading" || t.State == "positioning_unloading" || t.State == "unloading" || t.State == "diagnosing" || t.State == "repairing"
}
func (s *State) RemoveFromQueues(id string) {
	for _, e := range s.Loaders {
		e.Queue = remove(e.Queue, id)
	}
	for _, e := range s.Crushers {
		e.Queue = remove(e.Queue, id)
	}
}
func remove(q []string, id string) []string {
	out := []string{}
	for _, v := range q {
		if v != id {
			out = append(out, v)
		}
	}
	return out
}
func (s *State) Travel(c Config, t *Truck, loaded bool) {
	if t.Plan == nil {
		t.State = "idle"
		return
	}
	t.Path = nil
	t.Index = 0
	t.Progress = 0
	if loaded {
		t.State = "hauling"
		for i, n := range t.Plan.Nodes {
			if n == t.Node {
				t.Path = append([]string{}, t.Plan.Nodes[i:]...)
				break
			}
		}
		if t.Path == nil {
			t.Path = c.ShortestPath(t.Node, t.Plan.Nodes[len(t.Plan.Nodes)-1])
		}
	} else {
		t.State = "returning"
		if t.Node == t.Plan.Nodes[len(t.Plan.Nodes)-1] {
			t.Path = Reverse(t.Plan.Nodes)
		} else {
			t.Path = c.ShortestPath(t.Node, t.Plan.Nodes[0])
		}
	}
	if len(t.Path) <= 1 {
		s.Arrive(c, t)
	}
}
func (s *State) Arrive(c Config, t *Truck) {
	t.Path = nil
	t.Progress = 0
	t.Index = 0
	if t.Payload > 0 {
		t.State = "queued_unloading"
		for _, e := range s.Crushers {
			if e.Node == t.Node {
				e.Queue = append(e.Queue, t.ID)
				return
			}
		}
	} else {
		t.State = "queued_loading"
		if t.HasCycle {
			t.Cycles.Add(s.Time - t.CycleStart)
		}
		t.HasCycle = true
		t.CycleStart = s.Time
		for _, e := range s.Loaders {
			if e.Node == t.Node {
				e.Queue = append(e.Queue, t.ID)
				return
			}
		}
	}
	t.State = "idle"
}
func (s *State) applyPending(c Config, t *Truck) []Result {
	if t.Pending == nil {
		return nil
	}
	t.Plan = t.Pending
	t.Pending = nil
	r := Result{t.PendingCommand, t.PendingRun, "applied", "", "Маршрут применён", s.Time, s.Seq + 1}
	t.PendingCommand = ""
	t.PendingRun = ""
	s.RemoveFromQueues(t.ID)
	s.Travel(c, t, t.Payload > 0)
	s.Log(t.ID + ": новый маршрут применён")
	return []Result{r}
}
func (s *State) StartQueues(c Config) {
	s.startRepairs()
	for _, e := range s.Loaders {
		if e.Current == "" && len(e.Queue) > 0 {
			t := s.Truck(e.Queue[0])
			e.Queue = e.Queue[1:]
			e.Current = t.ID
			t.State = "positioning_loading"
			t.Timer = c.PositionLoading
		}
	}
	for _, e := range s.Crushers {
		if e.Current == "" && len(e.Queue) > 0 {
			t := s.Truck(e.Queue[0])
			if c.BufferCapacity-e.Buffer-e.Reserved+1e-8 >= t.Payload {
				e.Queue = e.Queue[1:]
				e.Current = t.ID
				e.Reserved = t.Payload
				t.State = "positioning_unloading"
				t.Timer = c.PositionUnloading
			}
		}
	}
}

// Step advances one simulated second through exact event boundaries. Crushing
// occurs before arrival events, so a just-dumped load is never processed retroactively.
func (s *State) Step(c Config, dt float64) []Result {
	if s.Mode != "running" {
		return nil
	}
	dt = math.Min(dt, c.ShiftSeconds-s.Time)
	var results []Result
	for left := dt; left > 1e-8; {
		s.replayFailures()
		s.StartQueues(c)
		h := left
		if s.ReplayFailures && s.FailureCursor < len(s.FailureScenario) {
			h = math.Min(h, s.FailureScenario[s.FailureCursor].Time-s.Time)
		}
		phase := c.CrusherSwitch - math.Mod(s.Time, c.CrusherSwitch)
		if phase > 1e-8 {
			h = math.Min(h, phase)
		}
		for _, t := range s.Trucks {
			if operating(t) {
				h = math.Min(h, t.Timer)
			} else if moving(t) {
				speed := truckSpeed(c, t)
				remain := c.EdgeLength(t.Path[t.Index], t.Path[t.Index+1]) * (1 - t.Progress) * 3600 / speed
				h = math.Min(h, remain)
			}
		}
		for i, e := range s.Crushers {
			if e.Current == "" && len(e.Queue) > 0 && e.Buffer > 0 {
				need := s.Truck(e.Queue[0]).Payload - (c.BufferCapacity - e.Buffer)
				if need > 1e-8 {
					h = math.Min(h, need/(s.Capacity(c, i)/3600))
				}
			}
		}
		if h < 1e-8 {
			h = math.Min(left, 1e-7)
		}
		for i, e := range s.Crushers {
			rate := s.Capacity(c, i) / 3600
			mass := math.Min(e.Buffer, rate*h)
			e.Buffer = math.Max(0, e.Buffer-mass)
			e.Processed += mass
			active := mass / rate
			e.Busy += active
			e.Starved += h - active
		}
		for _, e := range s.Loaders {
			if e.Current != "" {
				e.Busy += h
			} else {
				e.Idle += h
			}
		}
		for _, r := range s.Repairs {
			if r.Current != "" {
				r.Busy += h
			} else {
				r.Idle += h
			}
		}
		for _, t := range s.Trucks {
			if t.Failure != "" {
				t.Downtime += h
			}
			if t.State == "queued_repair" {
				t.RepairWait += h
			}
			if t.State == "queued_loading" || t.State == "queued_unloading" {
				t.QueueWait += h
			}
			if operating(t) {
				t.Timer -= h
			} else if moving(t) {
				speed := truckSpeed(c, t)
				t.Progress += speed * h / 3600 / c.EdgeLength(t.Path[t.Index], t.Path[t.Index+1])
			}
		}
		s.Time += h
		left -= h
		for _, t := range s.Trucks {
			if moving(t) && t.Progress >= 1-1e-8 {
				t.Index++
				t.Node = t.Path[t.Index]
				t.Progress = 0
				if t.State == "to_repair" {
					if t.Index >= len(t.Path)-1 {
						s.arriveRepair(t)
					}
				} else if t.Pending != nil {
					results = append(results, s.applyPending(c, t)...)
				} else if t.Index >= len(t.Path)-1 {
					s.Arrive(c, t)
				}
			}
			if !operating(t) || t.Timer > 1e-8 {
				continue
			}
			switch t.State {
			case "diagnosing":
				t.State = "repairing"
				t.Timer = RepairWorkSeconds
				s.Log(t.ID + ": диагностика завершена, ремонт — 50 минут")
			case "repairing":
				results = append(results, s.finishRepair(c, t)...)
			case "positioning_loading":
				t.State = "loading"
				t.Timer = c.PassSeconds
				t.LoadStarted = s.Time
			case "loading":
				v := math.Min(c.Bucket*c.Fill, math.Min(c.Body-t.Volume, (c.Capacity-t.Payload)/c.Density))
				mass := math.Max(0, v*c.Density)
				t.Volume += v
				t.Payload += mass
				for _, e := range s.Loaders {
					if e.Current == t.ID {
						e.Loaded += mass
						e.Passes++
						if t.Payload >= c.Capacity-1e-8 || t.Volume >= c.Body-1e-8 {
							e.Current = ""
						}
					}
				}
				if t.Payload >= c.Capacity-1e-8 || t.Volume >= c.Body-1e-8 {
					t.Loading.Add(s.Time - t.LoadStarted)
					s.Log(t.ID + ": погружено " + fmt.Sprintf("%.0f т", t.Payload))
					if t.Pending != nil {
						results = append(results, s.applyPending(c, t)...)
					} else {
						s.Travel(c, t, true)
					}
				} else {
					t.Timer = c.PassSeconds
				}
			case "positioning_unloading":
				t.State = "unloading"
				t.Timer = c.Unloading
				t.UnloadStarted = s.Time
			case "unloading":
				for _, e := range s.Crushers {
					if e.Current == t.ID {
						e.Buffer += t.Payload
						e.Delivered += t.Payload
						e.Reserved = 0
						e.Current = ""
					}
				}
				t.Trips++
				t.Payload = 0
				t.Volume = 0
				t.Unloading.Add(s.Time - t.UnloadStarted)
				s.Log(t.ID + ": рейс завершён")
				if t.Pending != nil {
					results = append(results, s.applyPending(c, t)...)
				} else {
					s.Travel(c, t, false)
				}
			}
		}
	}
	if s.Time >= c.ShiftSeconds-1e-8 {
		s.Time = c.ShiftSeconds
		s.Mode = "finished"
		for _, t := range s.Trucks {
			if t.Pending != nil {
				results = append(results, Result{t.PendingCommand, t.PendingRun, "cancelled", "SHIFT_FINISHED", "Смена завершена", s.Time, s.Seq + 1})
				t.Pending = nil
				t.PendingCommand = ""
			}
		}
		s.Log("12 часов завершены. Отчёт сохранён.")
		s.Report = s.ReportBytes(c)
	}
	return results
}
