package main

import (
	"math"
	"math/rand"
	"testing"
	"time"
)

func TestNewSimulation_distributesServersAcrossMachinesAndRegions(t *testing.T) {
	sim, err := NewSimulation(115, 23, ModeTrend)
	if err != nil {
		t.Fatalf("NewSimulation: %v", err)
	}
	if len(sim.Servers) != 115 {
		t.Fatalf("servers: got %d want 115", len(sim.Servers))
	}
	if len(sim.Machines) != 23 {
		t.Fatalf("machines: got %d want 23", len(sim.Machines))
	}

	seenServerIDs := map[int32]struct{}{}
	seenMachineIDs := map[int32]struct{}{}
	serversPerMachine := map[int32]int{}
	serversPerRegion := map[string]int{}

	for _, srv := range sim.Servers {
		if srv.Players < 0 || srv.Players > srv.MaxPlayers {
			t.Fatalf("server %d players out of range: %d (max %d)", srv.ServerID, srv.Players, srv.MaxPlayers)
		}
		if srv.UsedSlots < 0 || srv.UsedSlots > srv.MaxPlayers {
			t.Fatalf("server %d used_slots out of range: %d", srv.ServerID, srv.UsedSlots)
		}
		if srv.CPU < cpuIdleMin || srv.CPU > cpuLoadedMax {
			t.Fatalf("server %d cpu out of range: %d", srv.ServerID, srv.CPU)
		}
		if srv.MemMB < memBaseMinMB {
			t.Fatalf("server %d mem out of range: %d", srv.ServerID, srv.MemMB)
		}
		if _, ok := seenServerIDs[srv.ServerID]; ok {
			t.Fatalf("duplicate server_id %d", srv.ServerID)
		}
		seenServerIDs[srv.ServerID] = struct{}{}
		serversPerMachine[srv.MachineID]++
		serversPerRegion[srv.Region]++
	}

	for _, machine := range sim.Machines {
		if _, ok := seenMachineIDs[machine.MachineID]; ok {
			t.Fatalf("duplicate machine_id %d", machine.MachineID)
		}
		seenMachineIDs[machine.MachineID] = struct{}{}
		if machine.ServerCount != int32(serversPerMachine[machine.MachineID]) {
			t.Fatalf("machine %d server_count mismatch: field=%d actual=%d",
				machine.MachineID, machine.ServerCount, serversPerMachine[machine.MachineID])
		}
	}

	for _, region := range regionDefs {
		count := serversPerRegion[region.Name]
		if count < 38 || count > 39 {
			t.Fatalf("region %s server count %d, want 38 or 39", region.Name, count)
		}
	}
}

func TestNewSimulation_rejectsTooFewServers(t *testing.T) {
	if _, err := NewSimulation(5, 10, ModeTrend); err == nil {
		t.Fatal("expected error when num-servers < num-machines")
	}
}

func TestRegionActivityAt_twoDailyPeaks(t *testing.T) {
	utc := time.Date(2026, 6, 18, 0, 0, 0, 0, time.UTC)

	noon := regionActivityAt(utc.Add(12 * time.Hour))
	evening := regionActivityAt(utc.Add(17 * time.Hour))
	night := regionActivityAt(utc.Add(4 * time.Hour))

	if noon < 0.85 {
		t.Fatalf("noon activity too low: %f", noon)
	}
	if evening < 0.85 {
		t.Fatalf("evening activity too low: %f", evening)
	}
	if night > 0.25 {
		t.Fatalf("night activity too high: %f", night)
	}
	if math.Abs(noon-evening) > 0.15 {
		t.Fatalf("expected similar peak heights, noon=%f evening=%f", noon, evening)
	}
}

func TestRegionActivityAt_offsetsShiftPeaks(t *testing.T) {
	utc := time.Date(2026, 6, 18, 22, 0, 0, 0, time.UTC)

	usEastLocal := utc.Add(time.Duration(regionUTCOffset("us-east")) * time.Hour)
	euWestLocal := utc.Add(time.Duration(regionUTCOffset("eu-west")) * time.Hour)

	usEastAtUTC22 := regionActivityAt(usEastLocal)
	euWestAtUTC22 := regionActivityAt(euWestLocal)

	if usEastAtUTC22 <= euWestAtUTC22 {
		t.Fatalf("expected us-east evening peak while eu-west is quiet at UTC 22:00, us-east=%f eu-west=%f",
			usEastAtUTC22, euWestAtUTC22)
	}
	if usEastAtUTC22 < 0.85 {
		t.Fatalf("us-east activity too low at local 17:00: %f", usEastAtUTC22)
	}
	if euWestAtUTC22 > 0.35 {
		t.Fatalf("eu-west activity too high at local 23:00: %f", euWestAtUTC22)
	}
}

func TestRefreshMetrics_playersTrackRegionalActivity(t *testing.T) {
	sim, err := NewSimulation(30, 6, ModeTrend)
	if err != nil {
		t.Fatalf("NewSimulation: %v", err)
	}

	peakTime := time.Date(2026, 6, 18, 22, 0, 0, 0, time.UTC)
	sim.RefreshMetrics(peakTime, randNew(1))

	var usEastPlayers, euWestPlayers int32
	for _, srv := range sim.Servers {
		switch srv.Region {
		case "us-east":
			usEastPlayers += srv.Players
		case "eu-west":
			euWestPlayers += srv.Players
		}
	}

	if usEastPlayers <= euWestPlayers {
		t.Fatalf("expected us-east busier than eu-west at UTC 22:00, got us-east=%d eu-west=%d",
			usEastPlayers, euWestPlayers)
	}
}

func TestRefreshMetrics_cpuScalesWithPlayers(t *testing.T) {
	rng := randNew(2)

	busySrv := Server{MaxPlayers: 25, sessionAffinity: 1.0}
	quietSrv := Server{MaxPlayers: 25, sessionAffinity: 1.0}

	refreshServerMetrics(&busySrv, 0.95, rng)
	refreshServerMetrics(&quietSrv, 0.10, rng)

	if busySrv.CPU <= quietSrv.CPU {
		t.Fatalf("expected higher CPU with more players, busy=%d quiet=%d", busySrv.CPU, quietSrv.CPU)
	}
	if busySrv.MemMB <= quietSrv.MemMB {
		t.Fatalf("expected higher memory with more players, busy=%d quiet=%d", busySrv.MemMB, quietSrv.MemMB)
	}
	if busySrv.Players <= quietSrv.Players {
		t.Fatalf("expected more players at peak activity, busy=%d quiet=%d", busySrv.Players, quietSrv.Players)
	}
}

func TestRefreshMetrics_flatModeConsistentLoad(t *testing.T) {
	sim, err := NewSimulation(30, 6, ModeFlat)
	if err != nil {
		t.Fatalf("NewSimulation: %v", err)
	}
	if sim.Mode != ModeFlat {
		t.Fatalf("expected mode %q, got %q", ModeFlat, sim.Mode)
	}

	peakTime := time.Date(2026, 6, 18, 22, 0, 0, 0, time.UTC)
	sim.RefreshMetrics(peakTime, randNew(1))

	var totalPlayers int32
	for _, srv := range sim.Servers {
		totalPlayers += srv.Players
	}

	quietTime := time.Date(2026, 6, 18, 4, 0, 0, 0, time.UTC)
	sim.RefreshMetrics(quietTime, randNew(1))

	var quietPlayers int32
	for _, srv := range sim.Servers {
		quietPlayers += srv.Players
	}

	diff := totalPlayers - quietPlayers
	if diff < 0 {
		diff = -diff
	}
	if float64(diff)/float64(totalPlayers) > 0.15 {
		t.Fatalf("flat mode should produce similar player counts across times, peak=%d quiet=%d diff=%d",
			totalPlayers, quietPlayers, diff)
	}
}

func randNew(seed int64) *rand.Rand {
	return rand.New(rand.NewSource(seed))
}
