package main

import (
	"fmt"
	"log"
	"math"
	"math/rand"
	"time"
)

const (
	numRegions = 3

	ModeTrend = "trend"
	ModeFlat  = "flat"

	flatActivityLevel = 0.5

	fixedMaxPlayers = int32(25)

	cpuIdleMin    = int32(5)
	cpuIdleMax    = int32(15)
	cpuLoadedMin  = int32(45)
	cpuLoadedMax  = int32(75)
	memBaseMinMB  = int32(256)
	memBaseMaxMB  = int32(384)
	memPerPlayerMB = int32(28)

	peakHour1        = 12.0
	peakHour2        = 17.0
	peakWidthHours   = 2.5
	activityFloor    = 0.08
	maxPlayerDelta   = int32(4)
)

var regionDefs = []struct {
	Name           string
	ID             string
	UTCOffsetHours int
}{
	{Name: "us-east", ID: "ab7b39da-3571-4b13-bc06-69833b4f7e10", UTCOffsetHours: -5},
	{Name: "us-west", ID: "ab7b39da-3571-4b13-bc06-69833b4f7e11", UTCOffsetHours: -8},
	{Name: "eu-west", ID: "ab7b39da-3571-4b13-bc06-69833b4f7e12", UTCOffsetHours: 1},
}

// Server is one game server instance in the simulation.
type Server struct {
	AccountServiceID int32
	Fleet            string
	FleetID          string
	GameID           int32
	LocationID       int32
	MachineID        int32
	Map              string
	ModID            int32
	ProfileID        int32
	Provider         string
	Region           string
	RegionID         string
	ServerID         int32
	sessionAffinity  float64
	CPU              int32
	MemMB            int32
	MaxPlayers       int32
	Players          int32
	UsedSlots        int32
}

// Machine hosts a set of game servers in one region.
type Machine struct {
	AccountServiceID int32
	Fleet            string
	FleetID          string
	LocationID       int32
	MachineID        int32
	Provider         string
	Region           string
	RegionID         string
	ServerCount      int32
	CPUUser          float32
	CPUSys           float32
	MemActiveMB      int32
	MemAvailableMB   int32
	MemCachedMB      int32
	MemFreeMB        int32
	MemUsedByServers int32
	SwapIn           int32
	SwapOut          int32
	SwapUsed         int32
	Idle             float32
}

// Simulation holds the full fleet topology: machines and the servers on each machine.
type Simulation struct {
	Mode     string
	Machines []Machine
	Servers  []Server
}

// regionActivityAt returns a load factor in [activityFloor, 1] for local wall-clock time.
// Activity follows two daily peaks at noon and 5 PM with a quiet overnight trough.
func regionActivityAt(local time.Time) float64 {
	hour := float64(local.Hour()) + float64(local.Minute())/60.0 + float64(local.Second())/3600.0

	peak := func(center float64) float64 {
		d := hour - center
		return math.Exp(-(d * d) / (2 * peakWidthHours * peakWidthHours))
	}

	combined := peak(peakHour1) + peak(peakHour2)
	normalized := combined
	if normalized > 1 {
		normalized = 1
	}
	return activityFloor + (1.0-activityFloor)*normalized
}

func regionUTCOffset(name string) int {
	for _, r := range regionDefs {
		if r.Name == name {
			return r.UTCOffsetHours
		}
	}
	return 0
}

// NewSimulation builds an enumerated fleet: numServers spread evenly across numMachines,
// with machines and servers assigned to fixed regions as evenly as possible.
// The mode parameter controls player load behavior: ModeTrend (daily activity curve)
// or ModeFlat (consistent load).
func NewSimulation(numServers, numMachines int, mode string) (*Simulation, error) {
	if numMachines < 1 {
		return nil, fmt.Errorf("num-machines must be at least 1")
	}
	if numServers < numMachines {
		return nil, fmt.Errorf("num-servers (%d) must be >= num-machines (%d)", numServers, numMachines)
	}

	rng := rand.New(rand.NewSource(42))
	now := time.Now().UTC()

	machinesPerRegion, machinesRemainder := splitEvenly(numMachines, numRegions)
	serversPerRegion, serversRemainder := splitEvenly(numServers, numRegions)

	sim := &Simulation{
		Mode:     mode,
		Machines: make([]Machine, 0, numMachines),
		Servers:  make([]Server, 0, numServers),
	}

	nextMachineID := int32(1)
	nextServerID := int32(1)

	serverCreateTotal := time.Duration(0)
	derivedTotal := time.Duration(0)

	for regionIdx, region := range regionDefs {
		regionMachineCount := machinesPerRegion
		if regionIdx < machinesRemainder {
			regionMachineCount++
		}
		regionServerCount := serversPerRegion
		if regionIdx < serversRemainder {
			regionServerCount++
		}
		if regionMachineCount == 0 {
			continue
		}

		serversOnMachine, serversExtra := splitEvenly(regionServerCount, regionMachineCount)

		var regionActivity float64
		if mode == ModeFlat {
			regionActivity = flatActivityLevel
		} else {
			localNow := now.Add(time.Duration(region.UTCOffsetHours) * time.Hour)
			regionActivity = regionActivityAt(localNow)
		}

		for m := 0; m < regionMachineCount; m++ {
			machineServerCount := serversOnMachine
			if m < serversExtra {
				machineServerCount++
			}

			machineID := nextMachineID
			nextMachineID++

			locationID := int32(regionIdx*100 + m)
			accountServiceID := int32(regionIdx % 20)

			machine := Machine{
				AccountServiceID: accountServiceID,
				Fleet:            "Neon Breach",
				FleetID:          "ab7b39da-3571-4b13-bc06-69833b4f7e11",
				LocationID:       locationID,
				MachineID:        machineID,
				Provider:         "oneprovider",
				Region:           region.Name,
				RegionID:         region.ID,
				ServerCount:      int32(machineServerCount),
			}

			var serverMemTotal int32
			var serverCPUTotal int32
			var fillSum float64
			var fillCount int

			tSrv := time.Now()
			for s := 0; s < machineServerCount; s++ {
				srv := Server{
					AccountServiceID: accountServiceID,
					Fleet:            machine.Fleet,
					FleetID:          machine.FleetID,
					GameID:           1,
					LocationID:       locationID,
					MachineID:        machineID,
					Map:              "de_dust2",
					ModID:            1,
					ProfileID:        1,
					Provider:         machine.Provider,
					Region:           region.Name,
					RegionID:         region.ID,
					ServerID:         nextServerID,
					MaxPlayers:       fixedMaxPlayers,
					sessionAffinity:  0.75 + rng.Float64()*0.5,
				}
				refreshServerMetrics(&srv, regionActivity, rng)
				serverMemTotal += srv.MemMB
				serverCPUTotal += srv.CPU
				if srv.MaxPlayers > 0 {
					fillSum += float64(srv.Players) / float64(srv.MaxPlayers)
					fillCount++
				}
				sim.Servers = append(sim.Servers, srv)
				nextServerID++
			}
			serverCreateTotal += time.Since(tSrv)

			var avgFill float64
			if fillCount > 0 {
				avgFill = fillSum / float64(fillCount)
			}
			machine.MemUsedByServers = serverMemTotal
			tDer := time.Now()
			machine.refreshDerivedMetrics(rng, serverCPUTotal, avgFill)
			derivedTotal += time.Since(tDer)
			sim.Machines = append(sim.Machines, machine)
		}
	}

	log.Printf("[NewSim] server create total: %v, refreshDerivedMetrics total: %v",
		serverCreateTotal, derivedTotal)
	return sim, nil
}

func splitEvenly(total, parts int) (base, extra int) {
	if parts <= 0 {
		return 0, 0
	}
	return total / parts, total % parts
}

func randRange(rng *rand.Rand, min, max int32) int32 {
	if max <= min {
		return min
	}
	return min + rng.Int31n(max-min+1)
}

func refreshServerMetrics(srv *Server, regionActivity float64, rng *rand.Rand) {
	targetFill := regionActivity * srv.sessionAffinity
	if targetFill > 1 {
		targetFill = 1
	}
	targetPlayers := int32(float64(srv.MaxPlayers) * targetFill)
	srv.Players = jitterToward(rng, srv.Players, targetPlayers, 0, srv.MaxPlayers, maxPlayerDelta)
	srv.UsedSlots = jitterToward(rng, srv.UsedSlots, srv.Players, 0, srv.MaxPlayers, 2)

	fillRatio := 0.0
	if srv.MaxPlayers > 0 {
		fillRatio = float64(srv.Players) / float64(srv.MaxPlayers)
	}
	srv.CPU = playersToCPU(rng, fillRatio)
	srv.MemMB = playersToMem(rng, fillRatio, srv.Players)
}

func playersToCPU(rng *rand.Rand, fillRatio float64) int32 {
	idle := float64(cpuIdleMin) + rng.Float64()*float64(cpuIdleMax-cpuIdleMin)
	loaded := float64(cpuLoadedMin) + rng.Float64()*float64(cpuLoadedMax-cpuLoadedMin)
	cpu := idle + (loaded-idle)*fillRatio
	return int32(cpu + 0.5)
}

func playersToMem(rng *rand.Rand, fillRatio float64, players int32) int32 {
	base := randRange(rng, memBaseMinMB, memBaseMaxMB)
	mem := base + players*memPerPlayerMB
	mem += int32(fillRatio * float64(randRange(rng, 16, 96)))
	return mem
}

func (m *Machine) refreshDerivedMetrics(rng *rand.Rand, serverCPUTotal int32, avgFill float64) {
	if m.ServerCount == 0 {
		return
	}

	avgCPU := float32(serverCPUTotal) / float32(m.ServerCount)
	m.CPUUser = avgCPU * 0.85
	m.CPUSys = avgCPU * 0.15
	m.Idle = 100 - avgCPU
	if m.Idle < 0 {
		m.Idle = 0
	}

	headroomMB := int32(256)
	loadBump := int32(avgFill * 128)
	m.MemActiveMB = m.MemUsedByServers + randRange(rng, 64, 192) + loadBump
	m.MemCachedMB = randRange(rng, 128, 512) + loadBump/2
	m.MemAvailableMB = m.MemUsedByServers + headroomMB + randRange(rng, 256, 1024)
	m.MemFreeMB = m.MemAvailableMB - m.MemActiveMB
	if m.MemFreeMB < 0 {
		m.MemFreeMB = 0
	}

	swapScale := int32(avgFill * 32)
	m.SwapIn = randRange(rng, 0, 10) + swapScale/4
	m.SwapOut = randRange(rng, 0, 10) + swapScale/4
	m.SwapUsed = randRange(rng, 0, 64) + swapScale
}

// RefreshMetrics advances session-like player counts and resource usage for the given time.
func (sim *Simulation) RefreshMetrics(now time.Time, rng *rand.Rand) {
	regionActivity := make(map[string]float64, len(regionDefs))
	for _, region := range regionDefs {
		if sim.Mode == ModeFlat {
			regionActivity[region.Name] = flatActivityLevel
		} else {
			local := now.UTC().Add(time.Duration(region.UTCOffsetHours) * time.Hour)
			regionActivity[region.Name] = regionActivityAt(local)
		}
	}

	type machAgg struct {
		cpuTotal int32
		memTotal int32
		fillSum  float64
		fillCnt  int
	}
	agg := make(map[int32]*machAgg, len(sim.Machines))
	for _, m := range sim.Machines {
		agg[m.MachineID] = &machAgg{}
	}

	for i := range sim.Servers {
		srv := &sim.Servers[i]
		refreshServerMetrics(srv, regionActivity[srv.Region], rng)
		if a, ok := agg[srv.MachineID]; ok {
			a.cpuTotal += srv.CPU
			a.memTotal += srv.MemMB
			if srv.MaxPlayers > 0 {
				a.fillSum += float64(srv.Players) / float64(srv.MaxPlayers)
				a.fillCnt++
			}
		}
	}

	for i := range sim.Machines {
		machine := &sim.Machines[i]
		a := agg[machine.MachineID]
		if a == nil {
			continue
		}
		machine.MemUsedByServers = a.memTotal
		var avgFill float64
		if a.fillCnt > 0 {
			avgFill = a.fillSum / float64(a.fillCnt)
		}
		machine.refreshDerivedMetrics(rng, a.cpuTotal, avgFill)
	}
}

func jitterToward(rng *rand.Rand, _, target, min, max, maxDelta int32) int32 {
	noise := rng.Int31n(maxDelta*2+1) - maxDelta
	v := target + noise
	if v < min {
		return min
	}
	if v > max {
		return max
	}
	return v
}
