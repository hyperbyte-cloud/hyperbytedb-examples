package main

import (
	"flag"
	"log"
	"math/rand"
	"sync"
	"time"
)

func main() {
	url := flag.String("url", "http://localhost:8086", "InfluxDB v1 HTTP URL")
	db := flag.String("db", "gameservers", "Database name")
	batch := flag.Int("batch", 5000, "Lines per HTTP write request (burst mode only)")
	workers := flag.Int("workers", 6, "Concurrent workers per measurement type (burst mode only)")
	continuous := flag.Bool("continuous", false, "Repeat ingestion cycles forever")
	createDB := flag.Bool("create-db", false, "Create the database before writing data")

	period := flag.Duration("period", 0,
		"Spread all writes evenly across this duration each cycle (0 = burst as fast as possible). "+
			"With -continuous, defaults to 1m unless set explicitly")
	jitter := flag.Float64("jitter", 0.25,
		"Random per-write time shift as a fraction of the average spacing (0 = perfectly even)")
	concurrency := flag.Int("concurrency", 10,
		"Max concurrent in-flight HTTP writes per measurement in paced mode")

	mode := flag.String("mode", ModeTrend, "Player load mode: \"trend\" (daily activity curve) or \"flat\" (consistent load)")
	numServers := flag.Int("num-servers", 115, "Number of game servers in the simulated fleet")
	numMachines := flag.Int("num-machines", 23, "Number of machines hosting the fleet")
	actionRows := flag.Int("action-rows", 0, "Number of action_log rows per cycle")

	flag.Parse()
	log.Printf("[startup] Flags parsed")

	periodExplicit := false
	flag.Visit(func(f *flag.Flag) {
		if f.Name == "period" {
			periodExplicit = true
		}
	})

	t0 := time.Now()
	sim, err := NewSimulation(*numServers, *numMachines, *mode)
	if err != nil {
		log.Fatalf("Invalid simulation topology: %v", err)
	}
	log.Printf("[startup] NewSimulation took %v", time.Since(t0))

	effectivePeriod := *period
	if *continuous && !periodExplicit {
		effectivePeriod = time.Minute
	}

	w := &InfluxWriter{URL: *url, Database: *db}
	log.Printf("[startup] Writer ready for %s / %s", *url, *db)

	if *createDB {
		log.Printf("[startup] Creating database...")
		t0 := time.Now()
		if err := w.CreateDatabase(); err != nil {
			log.Fatalf("Failed to create database: %v", err)
		}
		log.Printf("[startup] Database created in %v", time.Since(t0))
	}

	serverRows := len(sim.Servers)
	machineRows := len(sim.Machines)

	log.Printf("Simulation: %d servers on %d machines (~%.1f servers/machine), %d regions",
		serverRows, machineRows, float64(serverRows)/float64(machineRows), numRegions)
	for _, region := range regionDefs {
		var machines, servers int
		for _, m := range sim.Machines {
			if m.Region == region.Name {
				machines++
			}
		}
		for _, s := range sim.Servers {
			if s.Region == region.Name {
				servers++
			}
		}
		log.Printf("  region %s: %d machines, %d servers", region.Name, machines, servers)
	}

	if effectivePeriod > 0 {
		rps := float64(serverRows+serverRows+machineRows+*actionRows) / effectivePeriod.Seconds()
		log.Printf("Paced mode: period=%v, ~%.1f rows/s total, jitter=%.2f, concurrency=%d",
			effectivePeriod, rps, *jitter, *concurrency)
	} else {
		log.Printf("Burst mode: batch=%d, workers=%d", *batch, *workers)
	}

	metricsRNG := rand.New(rand.NewSource(time.Now().UnixNano()))
	log.Printf("[startup] Entering main loop")

	runCycle := func() {
		log.Printf("[cycle] RefreshMetrics start")
		sim.RefreshMetrics(time.Now(), metricsRNG)
		start := time.Now()
		var wg sync.WaitGroup
		wg.Add(3)
		go func() {
			defer wg.Done()
			log.Printf("[cycle] Server stats generator starting")
			generateServerStats(w, sim, *batch, *workers, effectivePeriod, *jitter, *concurrency)
			log.Printf("[cycle] Server stats generator done")
		}()
		go func() {
			defer wg.Done()
			log.Printf("[cycle] Machine stats generator starting")
			generateMachineStats(w, sim, *batch, *workers, effectivePeriod, *jitter, *concurrency)
			log.Printf("[cycle] Machine stats generator done")
		}()
		go func() {
			defer wg.Done()
			log.Printf("[cycle] Action log generator starting")
			generateActionLog(w, *actionRows, *batch, *workers, effectivePeriod, *jitter, *concurrency)
			log.Printf("[cycle] Action log generator done")
		}()
		wg.Wait()
		log.Printf("[cycle] Cycle finished in %v", time.Since(start))
	}

	cycleNum := 0
	for {
		cycleNum++
		cycleStart := time.Now()
		log.Printf("[cycle] Cycle %d starting at %s", cycleNum, cycleStart.Format("15:04:05.000"))
		runCycle()
		if !*continuous {
			break
		}
		if pad := effectivePeriod - time.Since(cycleStart); pad > 0 {
			log.Printf("[cycle] Waiting %v to fill period", pad.Round(time.Millisecond))
			time.Sleep(pad)
		}
	}
}
