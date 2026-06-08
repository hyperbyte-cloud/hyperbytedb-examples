package main

import (
	"flag"
	"log"
	"time"
)

func main() {
	url := flag.String("url", "http://localhost:8086", "HyperbyteDB HTTP URL")
	db := flag.String("db", "energy_grid", "HyperbyteDB database name")
	createDB := flag.Bool("create-db", false, "Create the database before writing data")
	dryRun := flag.Bool("dry-run", false, "Generate sample lines without writing to HyperbyteDB")
	continuous := flag.Bool("continuous", true, "Emit a new batch every interval")
	interval := flag.Duration("interval", time.Minute, "Batch interval between write cycles")
	seed := flag.Int64("seed", time.Now().UnixNano(), "Random seed for reproducible simulation")
	cycles := flag.Int("cycles", 1, "Number of batches to emit when -continuous=false")

	flag.Parse()

	writer := &InfluxWriter{URL: *url, Database: *db}
	sim := NewSimulator(*seed)

	if *createDB && !*dryRun {
		if err := writer.CreateDatabase(); err != nil {
			log.Fatalf("create database: %v", err)
		}
		log.Printf("Database %q ready", *db)
	}

	log.Printf(
		"Energy grid simulator: %d generators, %d demand sites, %d storage sites, interval=%s",
		len(generatorSites), len(demandSites), len(storageSites), *interval,
	)

	emit := func(cycle int) error {
		batch := sim.GenerateCycle(time.Now())
		lines := batch.Lines()

		genMW, demandMW := 0.0, 0.0
		for _, p := range batch.Generation {
			genMW += p.PowerMW
		}
		for _, p := range batch.Demand {
			demandMW += p.DemandMW
		}

		log.Printf(
			"cycle %d: %d points, generation=%.1f MW, demand=%.1f MW, net=%.1f MW",
			cycle, len(lines), genMW, demandMW, genMW-demandMW,
		)

		if *dryRun {
			sample := 4
			if sample > len(lines) {
				sample = len(lines)
			}
			for i := 0; i < sample; i++ {
				log.Printf("sample: %s", lines[i])
			}
			return nil
		}

		return writer.WriteBody(joinLines(lines))
	}

	if !*continuous {
		for i := 1; i <= *cycles; i++ {
			if err := emit(i); err != nil {
				log.Fatalf("cycle %d: %v", i, err)
			}
			if i < *cycles {
				time.Sleep(*interval)
			}
		}
		return
	}

	cycle := 0
	for {
		cycle++
		cycleStart := time.Now()
		if err := emit(cycle); err != nil {
			log.Printf("cycle %d failed: %v", cycle, err)
		}
		if sleep := *interval - time.Since(cycleStart); sleep > 0 {
			time.Sleep(sleep)
		}
	}
}

func init() {
	log.SetPrefix("energy-grid: ")
}
