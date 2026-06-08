package main

import (
	"flag"
	"log"
	"os"
	"strings"
	"time"
)

func main() {
	url := flag.String("url", "http://localhost:8086", "HyperbyteDB HTTP URL")
	db := flag.String("db", "flight_tracking", "HyperbyteDB database name")
	createDB := flag.Bool("create-db", false, "Create the database before writing data")
	dryRun := flag.Bool("dry-run", false, "Fetch and format data without writing to HyperbyteDB")
	batch := flag.Int("batch", 5000, "Lines per HTTP write request")

	continuous := flag.Bool("continuous", true, "Poll OpenSky continuously for live aircraft states")
	interval := flag.Duration("interval", 10*time.Second, "Poll interval between OpenSky requests")
	extended := flag.Bool("extended", true, "Request extended aircraft category from OpenSky")

	username := flag.String("username", os.Getenv("OPENSKY_USERNAME"), "OpenSky username (or OPENSKY_USERNAME env)")
	password := flag.String("password", os.Getenv("OPENSKY_PASSWORD"), "OpenSky password (or OPENSKY_PASSWORD env)")

	bboxMinLat := flag.Float64("lamin", 0, "Bounding box minimum latitude (decimal degrees)")
	bboxMinLon := flag.Float64("lomin", 0, "Bounding box minimum longitude (decimal degrees)")
	bboxMaxLat := flag.Float64("lamax", 0, "Bounding box maximum latitude (decimal degrees)")
	bboxMaxLon := flag.Float64("lomax", 0, "Bounding box maximum longitude (decimal degrees)")
	icao24 := flag.String("icao24", "", "Comma-separated ICAO24 transponder addresses (hex)")

	flag.Parse()

	writer := &InfluxWriter{URL: *url, Database: *db}
	client := NewOpenSkyClient(*username, *password)

	opts := FetchOptions{
		BoundingBox: BoundingBox{
			MinLat: *bboxMinLat,
			MinLon: *bboxMinLon,
			MaxLat: *bboxMaxLat,
			MaxLon: *bboxMaxLon,
		},
		ICAO24:   splitCSV(*icao24),
		Extended: *extended,
	}

	if *createDB && !*dryRun {
		if err := writer.CreateDatabase(); err != nil {
			log.Fatalf("create database: %v", err)
		}
		log.Printf("Database %q ready", *db)
	}

	if opts.BoundingBox.Valid() {
		log.Printf(
			"Bounding box: lat [%.4f, %.4f], lon [%.4f, %.4f]",
			opts.BoundingBox.MinLat, opts.BoundingBox.MaxLat,
			opts.BoundingBox.MinLon, opts.BoundingBox.MaxLon,
		)
	} else if len(opts.ICAO24) > 0 {
		log.Printf("Tracking %d aircraft by ICAO24", len(opts.ICAO24))
	} else {
		log.Printf("Fetching all visible aircraft worldwide")
	}

	if !*continuous {
		runOnce(writer, client, opts, *dryRun, *batch)
		return
	}

	log.Printf("Live mode: polling OpenSky every %s", *interval)
	for {
		cycleStart := time.Now()
		if err := runOnce(writer, client, opts, *dryRun, *batch); err != nil {
			log.Printf("cycle error: %v", err)
			time.Sleep(minDuration(*interval, 30*time.Second))
			continue
		}

		if sleep := *interval - time.Since(cycleStart); sleep > 0 {
			time.Sleep(sleep)
		}
	}
}

func runOnce(writer *InfluxWriter, client *OpenSkyClient, opts FetchOptions, dryRun bool, batch int) error {
	start := time.Now()
	states, responseTime, err := client.FetchStates(opts)
	if err != nil {
		return err
	}
	if len(states) == 0 {
		log.Printf("No aircraft states returned (response time=%d)", responseTime)
		return nil
	}

	airborne := 0
	for _, state := range states {
		if state.HasOnGround && !state.OnGround {
			airborne++
		}
	}
	log.Printf(
		"Fetched %d aircraft states (%d airborne) in %v (opensky time=%d)",
		len(states), airborne, time.Since(start), responseTime,
	)

	if dryRun {
		sample := 3
		if sample > len(states) {
			sample = len(states)
		}
		for i := 0; i < sample; i++ {
			log.Printf("sample line: %s", states[i].ToLineProtocol())
		}
		log.Printf("Dry run complete: %d points formatted, not written", len(states))
		return nil
	}

	written, err := writeStates(writer, states, batch)
	if err != nil {
		return err
	}
	log.Printf("Wrote %d lines to %q", written, writer.Database)
	return nil
}

func splitCSV(value string) []string {
	if value == "" {
		return nil
	}
	parts := strings.Split(value, ",")
	out := make([]string, 0, len(parts))
	for _, part := range parts {
		part = strings.TrimSpace(part)
		if part != "" {
			out = append(out, part)
		}
	}
	return out
}

func minDuration(a, b time.Duration) time.Duration {
	if a < b {
		return a
	}
	return b
}

func init() {
	log.SetPrefix("flight-tracking: ")
}
