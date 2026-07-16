// Command n2yo-influx writes satellite positions to an InfluxDB v1.x HTTP endpoint.
// N2YO mode: REST API (https://www.n2yo.com/api/). SGP4 mode: Celestrak TLEs or Space-Track GP/3le
// (https://www.space-track.org/documentation#/api) + local propagation.
package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"log"
	"net/http"
	"net/url"
	"os"
	"strconv"
	"strings"
	"time"
)

const n2yoBase = "https://api.n2yo.com/rest/v1/satellite/positions"

type positionsInfo struct {
	Satname           string `json:"satname"`
	Satid             int    `json:"satid"`
	Transactionscount int    `json:"transactionscount"`
}

type positionSample struct {
	Satlatitude  float64 `json:"satlatitude"`
	Satlongitude float64 `json:"satlongitude"`
	Sataltitude  float64 `json:"sataltitude"`
	Azimuth      float64 `json:"azimuth"`
	Elevation    float64 `json:"elevation"`
	Ra           float64 `json:"ra"`
	Dec          float64 `json:"dec"`
	Timestamp    int64   `json:"timestamp"`
}

type positionsResponse struct {
	Info      positionsInfo    `json:"info"`
	Positions []positionSample `json:"positions"`
	Error     string           `json:"error"`
}

type influxWriter struct {
	url        string
	database   string
	user       string
	password   string
	httpClient *http.Client
	dryRun     bool
}

func (w *influxWriter) createDatabase() error {
	u := w.url + "/query"
	v := url.Values{}
	v.Set("q", "CREATE DATABASE "+w.database)
	req, err := http.NewRequest(http.MethodPost, u, strings.NewReader(v.Encode()))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	if w.user != "" {
		req.SetBasicAuth(w.user, w.password)
	}
	return w.do(req, false)
}

func (w *influxWriter) writeLineProtocol(body string) error {
	if w.dryRun {
		lines := strings.Split(body, "\n")
		sample := 3
		if sample > len(lines) {
			sample = len(lines)
		}
		for i := 0; i < sample; i++ {
			log.Printf("sample line: %s", lines[i])
		}
		log.Printf("Dry run complete: %d lines formatted, not written", len(lines))
		return nil
	}
	qs := url.Values{}
	qs.Set("db", w.database)
	qs.Set("precision", "s")
	u := w.url + "/write?" + qs.Encode()
	req, err := http.NewRequest(http.MethodPost, u, strings.NewReader(body))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "text/plain; charset=utf-8")
	if w.user != "" {
		req.SetBasicAuth(w.user, w.password)
	}
	return w.do(req, true)
}

func (w *influxWriter) do(req *http.Request, writeEndpoint bool) error {
	resp, err := w.httpClient.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	b, _ := io.ReadAll(resp.Body)
	if writeEndpoint {
		if resp.StatusCode != http.StatusNoContent && resp.StatusCode != http.StatusOK {
			return fmt.Errorf("influx write: status %d: %s", resp.StatusCode, b)
		}
	} else {
		if resp.StatusCode != http.StatusOK {
			return fmt.Errorf("influx query: status %d: %s", resp.StatusCode, b)
		}
	}
	return nil
}

func escapeInfluxTag(s string) string {
	s = strings.ReplaceAll(s, `\`, `\\`)
	s = strings.ReplaceAll(s, `,`, `\,`)
	s = strings.ReplaceAll(s, ` `, `\ `)
	s = strings.ReplaceAll(s, `=`, `\=`)
	return s
}

// line for InfluxDB 1.x line protocol: measurement,tags fields timestamp (seconds precision)
func lineProtocol(satid int, satname string, txCount int, p positionSample) string {
	name := escapeInfluxTag(satname)
	return fmt.Sprintf(
		"n2yo_position,satid=%d,satname=%s satlatitude=%.8f,satlongitude=%.8f,sataltitude_km=%.4f,azimuth=%.4f,elevation=%.4f,ra=%.8f,dec=%.8f,api_tx_count=%di %d",
		satid, name,
		p.Satlatitude, p.Satlongitude, p.Sataltitude, p.Azimuth, p.Elevation, p.Ra, p.Dec, txCount,
		p.Timestamp,
	)
}

func fetchPositions(ctx context.Context, apiKey string, norad int, lat, lng, alt float64, seconds int) (*positionsResponse, error) {
	if seconds < 1 || seconds > 300 {
		return nil, fmt.Errorf("seconds must be 1–300 (N2YO limit)")
	}
	p := fmt.Sprintf("%s/%d/%.5f/%.5f/%.1f/%d", n2yoBase, norad, lat, lng, alt, seconds)
	u, err := url.Parse(p)
	if err != nil {
		return nil, err
	}
	q := u.Query()
	q.Set("apiKey", apiKey)
	u.RawQuery = q.Encode()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u.String(), nil)
	if err != nil {
		return nil, err
	}
	c := &http.Client{Timeout: 30 * time.Second}
	resp, err := c.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	b, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, err
	}
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("n2yo HTTP %d: %s", resp.StatusCode, string(b))
	}
	var out positionsResponse
	if err := json.Unmarshal(b, &out); err != nil {
		return nil, fmt.Errorf("n2yo JSON: %w", err)
	}
	if strings.TrimSpace(out.Error) != "" {
		return nil, fmt.Errorf("n2yo: %s", out.Error)
	}
	return &out, nil
}

func mustEnvOrFlag(flagVal, envName string) string {
	if flagVal != "" {
		return flagVal
	}
	return strings.TrimSpace(os.Getenv(envName))
}

// linesForSatellite returns line-protocol lines for one NORAD id (one N2YO request).
func linesForSatellite(
	ctx context.Context,
	apiKey string,
	norad int,
	lat, lng, alt float64,
	seconds int,
) ([]string, error) {
	data, err := fetchPositions(ctx, apiKey, norad, lat, lng, alt, seconds)
	if err != nil {
		return nil, err
	}
	if len(data.Positions) == 0 {
		return nil, fmt.Errorf("no positions (satid %d)", data.Info.Satid)
	}
	satid := data.Info.Satid
	if satid == 0 {
		satid = norad
	}
	satname := data.Info.Satname
	tx := data.Info.Transactionscount
	var lines []string
	for _, p := range data.Positions {
		lines = append(lines, lineProtocol(satid, satname, tx, p))
	}
	return lines, nil
}

// parseNORADList parses "25544,43013" into unique ids (order preserved, duplicates removed).
func parseNORADList(s string) ([]int, error) {
	s = strings.TrimSpace(s)
	if s == "" {
		return nil, fmt.Errorf("at least one NORAD id required in -satellites")
	}
	var out []int
	seen := make(map[int]struct{})
	for _, p := range strings.Split(s, ",") {
		p = strings.TrimSpace(p)
		if p == "" {
			continue
		}
		n, err := strconv.Atoi(p)
		if err != nil {
			return nil, fmt.Errorf("invalid NORAD %q: %w", p, err)
		}
		if _, ok := seen[n]; ok {
			continue
		}
		seen[n] = struct{}{}
		out = append(out, n)
	}
	if len(out) == 0 {
		return nil, fmt.Errorf("at least one NORAD id required in -satellites")
	}
	return out, nil
}

func runPass(
	w *influxWriter,
	apiKey string,
	noradIDs []int,
	lat, lng, alt float64,
	seconds int,
) (int, error) {
	// N2YO calls are sequential; allow enough time for all satellites in one pass.
	d := 45*time.Second + 20*time.Second*time.Duration(len(noradIDs))
	if d > 5*time.Minute {
		d = 5 * time.Minute
	}
	ctx, cancel := context.WithTimeout(context.Background(), d)
	defer cancel()
	var all []string
	var errs int
	for _, id := range noradIDs {
		lines, err := linesForSatellite(ctx, apiKey, id, lat, lng, alt, seconds)
		if err != nil {
			log.Printf("norad %d: %v", id, err)
			errs++
			continue
		}
		all = append(all, lines...)
	}
	if len(all) == 0 {
		return 0, fmt.Errorf("no points written (failures: %d/%d)", errs, len(noradIDs))
	}
	if err := w.writeLineProtocol(strings.Join(all, "\n")); err != nil {
		return 0, err
	}
	if errs > 0 {
		log.Printf("partial: %d satellite(s) failed, wrote %d point(s)", errs, len(all))
	}
	return len(all), nil
}

func main() {
	url := flag.String("url", "http://localhost:8086", "HyperbyteDB HTTP URL")
	db := flag.String("db", "n2yo", "HyperbyteDB database name")
	influxUser := flag.String("influx-user", "", "HyperbyteDB username (optional)")
	influxPass := flag.String("influx-password", "", "HyperbyteDB password (optional)")
	createDB := flag.Bool("create-db", false, "Create the database before writing data")
	dryRun := flag.Bool("dry-run", false, "Fetch and format data without writing to HyperbyteDB")
	batch := flag.Int("batch", 5000, "Lines per HTTP write request")
	continuous := flag.Bool("continuous", true, "Poll continuously for satellite positions")
	apiKey := flag.String("api-key", "", "N2YO API key (or set N2YO_API_KEY)")
	satelliteList := flag.String("satellites", "25544", "Comma-separated NORAD catalog IDs to monitor (e.g. 25544,43013,33591)")
	obsLat := flag.Float64("lat", 0, "Observer latitude (decimal degrees)")
	obsLng := flag.Float64("lng", 0, "Observer longitude (decimal degrees)")
	obsAlt := flag.Float64("alt", 0, "Observer altitude above sea level (meters)")
	seconds := flag.Int("seconds", 1, "N2YO only: position samples per satellite (1–300, each is +1s). Ignored for -sgp4")
	interval := flag.Duration("interval", 15*time.Second, "Poll interval between write cycles; 0 = once and exit")
	sgp4 := flag.Bool("sgp4", false, "Use Celestrak TLE + local SGP4 (e.g. all Starlink). No N2YO key. See -sgp4-url, or -spacetrack for space-track.org")
	sgp4URL := flag.String("sgp4-url", defaultCelestrakStarlink, "Celestrak gp.php TLE URL (used with -sgp4; ignored if -spacetrack)")
	sgp4Cache := flag.String("sgp4-cache", DefaultTLECachePath(), "TLE file: saved on success; on HTTP 403/offline, load from this file. Use 'none' to disable. Not used with -spacetrack (use -spacetrack-cache)")
	// Celestrak updates GP data about every 2h; re-fetching more often can trigger 403.
	// Space-Track GP: at most ~1 TLE download per hour (see https://www.space-track.org/documentation#/api).
	sgp4Refresh := flag.Duration("sgp4-refresh", 2*time.Hour+5*time.Minute, "Re-download TLEs this often in -sgp4 mode (Celestrak: ≥2h5m; Space-Track: <1h is raised to 1h)")

	spacetrack := flag.Bool("spacetrack", false, "With -sgp4: full GP/3le from space-track.org (incl. Alpha-5) instead of Celestrak. See https://www.space-track.org/documentation#/api — account + credentials")
	spacetrackID := flag.String("spacetrack-identity", "", "Space-Track username (or SPACETRACK_IDENTITY)")
	spacetrackPassword := flag.String("spacetrack-password", "", "Space-Track password (or SPACETRACK_PASSWORD)")
	spacetrackCache := flag.String("spacetrack-cache", DefaultSpaceTrackCachePath(), "3le cache file; 'none' to disable. Used only with -spacetrack")
	flag.Parse()

	if !*continuous {
		*interval = 0
	}

	if *spacetrack && !*sgp4 {
		log.Fatal("-spacetrack requires -sgp4")
	}

	if *sgp4 {
		cp := *sgp4Cache
		if strings.EqualFold(cp, "none") {
			cp = ""
		}
		stC := *spacetrackCache
		if strings.EqualFold(stC, "none") {
			stC = ""
		}
		ident := mustEnvOrFlag(*spacetrackID, "SPACETRACK_IDENTITY")
		passw := mustEnvOrFlag(*spacetrackPassword, "SPACETRACK_PASSWORD")
		if *spacetrack {
			runSGP4Mode(
				*url, *db, *influxUser, *influxPass, *createDB, *dryRun, *batch,
				"", "", *sgp4Refresh, *interval,
				*obsLat, *obsLng, *obsAlt,
				true, ident, passw, stC,
			)
			return
		}
		runSGP4Mode(
			*url, *db, *influxUser, *influxPass, *createDB, *dryRun, *batch,
			*sgp4URL, cp, *sgp4Refresh, *interval,
			*obsLat, *obsLng, *obsAlt,
			false, "", "", "",
		)
		return
	}

	key := mustEnvOrFlag(*apiKey, "N2YO_API_KEY")
	if key == "" {
		log.Fatal("N2YO API key required: -api-key or N2YO_API_KEY (or use -sgp4 for TLE+SGP4 without N2YO)")
	}

	noradIDs, err := parseNORADList(*satelliteList)
	if err != nil {
		log.Fatalf("satellites: %v", err)
	}
	if *interval == 0 {
		log.Printf("one-shot: %d satellite(s) (NORAD: %v)", len(noradIDs), noradIDs)
	} else {
		log.Printf("monitoring %d satellite(s) every %v (NORAD: %v)", len(noradIDs), *interval, noradIDs)
	}

	w := &influxWriter{
		url:        strings.TrimSuffix(*url, "/"),
		database:   *db,
		user:       *influxUser,
		password:   *influxPass,
		httpClient: &http.Client{Timeout: 30 * time.Second},
		dryRun:     *dryRun,
	}
	if *createDB {
		if err := w.createDatabase(); err != nil {
			log.Fatalf("create database: %v", err)
		}
		log.Printf("database %q created (or already exists)", *db)
	}

	do := func() {
		n, err := runPass(w, key, noradIDs, *obsLat, *obsLng, *obsAlt, *seconds)
		if err != nil {
			log.Printf("error: %v", err)
			return
		}
		log.Printf("wrote %d point(s) to %s / db=%q", n, w.url, w.database)
	}

	if *interval > 0 {
		do()
		t := time.NewTicker(*interval)
		defer t.Stop()
		for range t.C {
			do()
		}
		return
	}
	n, err := runPass(w, key, noradIDs, *obsLat, *obsLng, *obsAlt, *seconds)
	if err != nil {
		log.Fatalf("%v", err)
	}
	log.Printf("wrote %d point(s) to %s / db=%q", n, w.url, w.database)
}
