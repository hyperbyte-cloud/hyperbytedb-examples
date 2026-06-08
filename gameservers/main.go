package main

import (
	"flag"
	"log"
	"sync"
	"time"
)

func main() {
	url := flag.String("url", "http://localhost:8086", "InfluxDB v1 HTTP URL")
	db := flag.String("db", "multiplay", "InfluxDB database name")
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

	serverRows := flag.Int("server-rows", 115000, "Number of server_stats rows per cycle")
	serverSmallRows := flag.Int("server-small-rows", 115000, "Number of server_stats_small rows per cycle")
	machineRows := flag.Int("machine-rows", 60919, "Number of machine_stats rows per cycle")
	actionRows := flag.Int("action-rows", 0, "Number of action_log rows per cycle")

	flag.Parse()

	periodExplicit := false
	flag.Visit(func(f *flag.Flag) {
		if f.Name == "period" {
			periodExplicit = true
		}
	})

	effectivePeriod := *period
	if *continuous && !periodExplicit {
		effectivePeriod = time.Minute
	}

	w := &InfluxWriter{URL: *url, Database: *db}

	if *createDB {
		if err := w.CreateDatabase(); err != nil {
			log.Fatalf("Failed to create database: %v", err)
		}
		log.Printf("Database %q created (or already exists)", *db)
	}

	if effectivePeriod > 0 {
		rps := float64(*serverRows+*serverSmallRows+*machineRows+*actionRows) / effectivePeriod.Seconds()
		log.Printf("Paced mode: period=%v, ~%.1f rows/s total, jitter=%.2f, concurrency=%d",
			effectivePeriod, rps, *jitter, *concurrency)
	} else {
		log.Printf("Burst mode: batch=%d, workers=%d", *batch, *workers)
	}

	runCycle := func() {
		start := time.Now()
		var wg sync.WaitGroup
		wg.Add(4)
		go func() {
			defer wg.Done()
			generateServerStats(w, *serverRows, *batch, *workers, effectivePeriod, *jitter, *concurrency)
		}()
		go func() {
			defer wg.Done()
			generateMachineStats(w, *machineRows, *batch, *workers, effectivePeriod, *jitter, *concurrency)
		}()
		go func() {
			defer wg.Done()
			generateActionLog(w, *actionRows, *batch, *workers, effectivePeriod, *jitter, *concurrency)
		}()
		go func() {
			defer wg.Done()
			generateServerStatsSmall(w, *serverSmallRows, *batch, *workers, effectivePeriod, *jitter, *concurrency)
		}()
		wg.Wait()
		log.Printf("Cycle finished in %v", time.Since(start))
	}

	for {
		cycleStart := time.Now()
		runCycle()
		if !*continuous {
			break
		}
		if pad := effectivePeriod - time.Since(cycleStart); pad > 0 {
			log.Printf("Waiting %v to fill period", pad.Round(time.Millisecond))
			time.Sleep(pad)
		}
	}
}
