package main

import (
	"flag"
	"log"
	"os"
	"strings"
	"time"
)

const (
	defaultStudyID     = 2911040 // Galapagos Albatrosses (public)
	defaultSensorType  = "gps"
	defaultSensorTypeID = 653
)

func main() {
	url := flag.String("url", "http://localhost:8086", "HyperbyteDB HTTP URL")
	db := flag.String("db", "marine_tracking", "HyperbyteDB database name")
	createDB := flag.Bool("create-db", false, "Create the database before writing data")
	dryRun := flag.Bool("dry-run", false, "Fetch and format data without writing to HyperbyteDB")
	batch := flag.Int("batch", 5000, "Lines per HTTP write request")

	studyID := flag.Int64("study-id", defaultStudyID, "Movebank study ID")
	sensorType := flag.String("sensor-type", defaultSensorType, "Movebank sensor type name (e.g. gps)")
	sensorTypeID := flag.Int("sensor-type-id", defaultSensorTypeID, "Movebank sensor type ID for CSV direct-read")

	username := flag.String("username", os.Getenv("MOVEBANK_USERNAME"), "Movebank username (or MOVEBANK_USERNAME env)")
	password := flag.String("password", os.Getenv("MOVEBANK_PASSWORD"), "Movebank password (or MOVEBANK_PASSWORD env)")
	apiToken := flag.String("api-token", os.Getenv("MOVEBANK_API_TOKEN"), "Movebank API token (or MOVEBANK_API_TOKEN env)")

	animals := flag.String("animals", "", "Comma-separated animal local identifiers (default: discover all)")
	maxEvents := flag.Int("max-events", 0, "Max events per animal for public JSON API (0 = all)")
	reduction := flag.String("reduction-profile", "", "Movebank event reduction profile (EURING_01..EURING_04)")

	timestampStart := flag.String("timestamp-start", "", "Start timestamp for CSV API (yyyyMMddHHmmssSSS)")
	timestampEnd := flag.String("timestamp-end", "", "End timestamp for CSV API (yyyyMMddHHmmssSSS)")
	timestampStartMS := flag.Int64("timestamp-start-ms", 0, "Start timestamp for JSON API (Unix ms)")
	timestampEndMS := flag.Int64("timestamp-end-ms", 0, "End timestamp for JSON API (Unix ms)")

	flag.Parse()

	writer := &InfluxWriter{URL: *url, Database: *db}
	client := NewMovebankClient(*username, *password, *apiToken)

	if *createDB && !*dryRun {
		if err := writer.CreateDatabase(); err != nil {
			log.Fatalf("create database: %v", err)
		}
		log.Printf("Database %q ready", *db)
	}

	animalIDs := splitCSV(*animals)
	opts := FetchOptions{
		StudyID:            *studyID,
		SensorTypeID:       *sensorTypeID,
		SensorType:         *sensorType,
		Animals:            animalIDs,
		MaxEventsPerAnimal: *maxEvents,
		TimestampStart:     *timestampStart,
		TimestampEnd:       *timestampEnd,
		TimestampStartMS:   *timestampStartMS,
		TimestampEndMS:     *timestampEndMS,
		ReductionProfile:   *reduction,
	}

	if len(animalIDs) == 0 {
		log.Printf("Discovering animals in study %d...", *studyID)
		var discovered []string
		var err error
		if client.authenticated() {
			discovered, err = client.ListIndividuals(*studyID)
		} else {
			discovered, err = client.DiscoverAnimalsPublic(*studyID, *sensorType)
		}
		if err != nil {
			log.Fatalf("discover animals: %v", err)
		}
		if len(discovered) == 0 {
			log.Fatal("no animals found; specify -animals explicitly")
		}
		log.Printf("Found %d animals", len(discovered))
		if client.authenticated() {
			opts.Animals = discovered
		}
	}

	start := time.Now()
	var points []TrackingPoint
	var err error

	if client.authenticated() {
		log.Printf("Fetching GPS events via Movebank direct-read CSV API (study %d)...", *studyID)
		if study, studyErr := client.GetStudy(*studyID); studyErr == nil && study.Name != "" {
			log.Printf("Study: %s", study.Name)
		}
		points, err = client.FetchEventsCSV(opts)
	} else {
		log.Printf("Fetching GPS events via Movebank public JSON API (study %d)...", *studyID)
		points, err = client.FetchEventsJSON(opts)
	}
	if err != nil {
		log.Fatalf("fetch events: %v", err)
	}
	if len(points) == 0 {
		log.Fatal("no tracking points returned")
	}
	log.Printf("Fetched %d location points in %v", len(points), time.Since(start))

	if *dryRun {
		sample := 3
		if sample > len(points) {
			sample = len(points)
		}
		for i := 0; i < sample; i++ {
			log.Printf("sample line: %s", points[i].ToLineProtocol())
		}
		log.Printf("Dry run complete: %d points formatted, not written", len(points))
		return
	}

	written, err := writePoints(writer, points, *batch)
	if err != nil {
		log.Fatalf("write to hyperbytedb: %v", err)
	}
	log.Printf("Wrote %d lines to %q on %s", written, *db, *url)
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

func writePoints(writer *InfluxWriter, points []TrackingPoint, batchSize int) (int, error) {
	if batchSize <= 0 {
		batchSize = 5000
	}

	var buf strings.Builder
	written := 0
	linesInBatch := 0

	flush := func() error {
		if buf.Len() == 0 {
			return nil
		}
		if err := writer.WriteBody(buf.String()); err != nil {
			return err
		}
		written += linesInBatch
		buf.Reset()
		linesInBatch = 0
		return nil
	}

	for _, point := range points {
		line := point.ToLineProtocol()
		if line == "" {
			continue
		}
		if buf.Len() > 0 {
			buf.WriteByte('\n')
		}
		buf.WriteString(line)
		linesInBatch++

		if linesInBatch >= batchSize {
			if err := flush(); err != nil {
				return written, err
			}
		}
	}

	if err := flush(); err != nil {
		return written, err
	}
	return written, nil
}

func init() {
	log.SetPrefix("marine-tracking: ")
}
