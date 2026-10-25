package domain

import (
	"encoding/json"
	"fmt"
	"math"
	"testing"
)

func scenario(t *testing.T) (Config, *State) {
	t.Helper()
	c, err := LoadConfig("../config/site.json")
	if err != nil {
		t.Fatal(err)
	}
	s := NewState(c)
	s.RandomSeed = "operations-review-20261025"
	s.OperationsVersion = 0
	s.RandomFailures = nil
	for _, truck := range s.Trucks {
		truck.FuelInitialized = false
	}
	s.EnsureOperations(c)
	for i, truck := range s.Trucks {
		r := c.Routes[i%4]
		truck.Plan = &Plan{ID: r.ID, Nodes: r.Nodes}
		s.InitialPlans[truck.ID] = truck.Plan
	}
	s.Mode = "running"
	for _, truck := range s.Trucks {
		s.Travel(c, truck, false)
	}
	return c, s
}

func checkBalances(t *testing.T, c Config, s *State) {
	t.Helper()
	loaded, delivered, processed, payload, buffer := s.Totals()
	if math.Abs(loaded-delivered-payload) > 1e-5 || math.Abs(delivered-processed-buffer) > 1e-5 {
		t.Fatalf("cargo imbalance at %f", s.Time)
	}
	for _, truck := range s.Trucks {
		if math.IsNaN(truck.Fuel) || truck.Fuel < -1e-8 || truck.Fuel > c.Operations.Tank+1e-8 || math.Abs(truck.InitialFuel+truck.FuelAdded-truck.FuelConsumed-truck.Fuel) > 1e-5 {
			t.Fatalf("fuel imbalance: %+v", truck)
		}
	}
}

func TestFullShiftWithFuelAndRandomFailures(t *testing.T) {
	c, s := scenario(t)
	var transitions = map[string]bool{}
	for s.Time < c.ShiftSeconds {
		if int(s.Time)%30 == 0 {
			for _, truck := range s.Trucks {
				if truck.State != "failed" {
					continue
				}
				kind := "dispatch_service"
				if truck.Failure == "medium" {
					kind = "send_to_repair"
				}
				if truck.Failure == "heavy" {
					kind = "dispatch_tow"
				}
				cmd := Command{Schema: 1, ID: fmt.Sprintf("check-%d-%s", int(s.Time), truck.ID), RunID: s.RunID, Type: kind, Target: truck.ID, Role: "mechanic"}
				s.Handle(c, cmd)
			}
		}
		s.Step(c, 1)
		for _, truck := range s.Trucks {
			transitions[truck.State] = true
		}
		if int(s.Time)%60 == 0 {
			checkBalances(t, c, s)
		}
	}
	checkBalances(t, c, s)
	if s.Mode != "finished" || len(s.Report) == 0 {
		t.Fatal("no complete shift report")
	}
	load := s.LoadingSummary()
	if load["count"].(int) == 0 || load["min_t"].(float64) < 230-1e-8 || load["max_t"].(float64) > 250+1e-8 || load["min_t"] == load["max_t"] {
		t.Fatalf("load spread missing: %v", load)
	}
	for _, state := range []string{"to_refuel", "queued_refuel", "refueling", "failed", "field_work", "repairing"} {
		if !transitions[state] {
			t.Errorf("state not exercised: %s", state)
		}
	}
	if len(s.FailureScenario) == 0 || s.FuelSummary()["added_l"] <= 0 {
		t.Fatal("no failures or refuelling")
	}
	t.Logf("loading=%v fuel=%v failures=%d", load, s.FuelSummary(), len(s.FailureScenario))
}

func TestFieldServiceAndHeavyTowCycle(t *testing.T) {
	c, s := scenario(t)
	s.RandomFailures = nil
	s.Step(c, 30)
	issue := func(kind, target, role, severity string) {
		t.Helper()
		cmd := Command{Schema: 1, ID: kind + "-cycle-check", RunID: s.RunID, Type: kind, Target: target, Role: role}
		cmd.Parameters.Severity = severity
		rs, _ := s.Handle(c, cmd)
		if rs[0].Status != "applied" {
			t.Fatal(rs)
		}
	}
	issue("inject_failure", "truck-01", "dispatcher", "heavy")
	issue("inject_failure", "truck-02", "dispatcher", "light")
	issue("dispatch_tow", "truck-01", "mechanic", "")
	issue("dispatch_service", "truck-02", "mechanic", "")
	issue("preventive_service", "truck-03", "mechanic", "")
	towing := false
	for i := 0; i < 16000; i++ {
		s.Step(c, 1)
		truck := s.Trucks[0]
		if truck.State == "being_towed" {
			towing = true
			v := s.Vehicles[2]
			if truck.Node != v.Node || truck.Index != v.Index || truck.Progress != v.Progress || JSON(truck.Path) != JSON(v.Path) {
				t.Fatal("truck and tow positions diverged")
			}
		}
	}
	if !towing || s.Trucks[0].RepairsCompleted != 1 || s.Trucks[1].RepairsCompleted != 1 || s.Trucks[2].PreventiveCompleted != 1 {
		t.Fatal("repair cycle incomplete")
	}
	for _, v := range s.Vehicles {
		if v.State != "idle" || v.Completed != 1 {
			t.Fatalf("vehicle did not return: %+v", v)
		}
	}
	checkBalances(t, c, s)
}

func TestRepeatAndRestoreKeepRandomInputs(t *testing.T) {
	c, s := scenario(t)
	seed := s.RandomSeed
	fuel := s.Trucks[0].Fuel
	target := s.loadTarget(c, s.Trucks[0])
	scheduled := JSON(s.RandomFailures)
	for i := 0; i < 5000; i++ {
		s.Step(c, 1)
	}
	bytes, _ := json.Marshal(s)
	var restored State
	if err := json.Unmarshal(bytes, &restored); err != nil {
		t.Fatal(err)
	}
	restored.EnsureOperations(c)
	if JSON(restored.RandomFailures) != scheduled || restored.RandomCursor != s.RandomCursor || restored.Trucks[0].Fuel != s.Trucks[0].Fuel {
		t.Fatal("restore changed random inputs or fuel")
	}
	s.Mode = "paused"
	rs, _ := s.Handle(c, Command{Schema: 1, ID: "repeat-check", RunID: s.RunID, Type: "repeat_shift", Role: "dispatcher"})
	if rs[len(rs)-1].Status != "applied" || s.RandomSeed != seed || s.Trucks[0].Fuel != fuel || s.loadTarget(c, s.Trucks[0]) != target || JSON(s.RandomFailures) != scheduled || s.RandomCursor != 0 {
		t.Fatal("repeat changed initial conditions")
	}
}

func TestFuelQueueIndependentOfRepairCrew(t *testing.T) {
	c, s := scenario(t)
	s.RandomFailures = nil
	r := s.Repairs[0]
	r.Current = "truck-20"
	s.Trucks[19].State = "repairing"
	s.Trucks[19].Timer = 9000
	for _, truck := range s.Trucks[:2] {
		s.releaseProduction(truck)
		truck.Path = nil
		truck.Node = "R1"
		truck.State = "idle"
		truck.Fuel = 300
		truck.InitialFuel = 300
		if !s.beginRefuel(c, truck, "R1") {
			t.Fatal("cannot refuel")
		}
	}
	s.Step(c, 1)
	if r.FuelCurrent != "truck-01" || len(r.FuelQueue) != 1 || r.Current != "truck-20" {
		t.Fatal("fuel and repair queues interfered")
	}
	for i := 0; i < 1250; i++ {
		s.Step(c, 1)
	}
	if s.Trucks[0].Refuels != 1 || s.Trucks[1].Refuels != 1 || r.Current != "truck-20" {
		t.Fatal("fuel blocked by repairs")
	}
	checkBalances(t, c, s)
}

func TestEmptyTankCanBeTowedToFuel(t *testing.T) {
	c, s := scenario(t)
	s.RandomFailures = nil
	truck := s.Trucks[0]
	truck.Fuel = .001
	truck.InitialFuel = .001
	s.Step(c, 1)
	if truck.State != "out_of_fuel" {
		t.Fatalf("empty truck still driving: %s", truck.State)
	}
	cmd := Command{Schema: 1, ID: "tow-fuel-check", RunID: s.RunID, Type: "dispatch_tow", Target: truck.ID, Role: "mechanic"}
	rs, _ := s.Handle(c, cmd)
	if rs[0].Status != "applied" {
		t.Fatal(rs)
	}
	seenTow := false
	for i := 0; i < 5000 && truck.Refuels == 0; i++ {
		s.Step(c, 1)
		if s.Vehicles[2].State == "towing" {
			seenTow = true
			if truck.State != "being_towed" || fuelRate(c, truck) != 0 {
				t.Fatalf("engine running during tow: truck=%s rate=%f time=%f", truck.State, fuelRate(c, truck), s.Time)
			}
		}
	}
	if !seenTow {
		t.Fatal("empty tank tow not observed")
	}
	if truck.Refuels != 1 {
		t.Fatalf("not recovered: %s", truck.State)
	}
	checkBalances(t, c, s)
}
