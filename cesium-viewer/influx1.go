package main

// InfluxDB 1.x HTTP /query?db= &q= InfluxQL only (no Flux).

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"
)

type influxQueryResult struct {
	Results []struct {
		StatementID int            `json:"statement_id"`
		Series      []influxSeries `json:"series"`
	} `json:"results"`
	Error string `json:"error"`
}

type influxSeries struct {
	Name    string            `json:"name"`
	Tags    map[string]string `json:"tags"`
	Columns []string          `json:"columns"`
	Values  [][]any           `json:"values"`
	Partial bool              `json:"partial"`
}

// Satellite is JSON for the Cesium client.
type Satellite struct {
	Norad       int     `json:"norad"`
	Name        string  `json:"name"`
	Lat         float64 `json:"lat"`
	Lon         float64 `json:"lon"`
	AltMeters   float64 `json:"altMeters"`
	Azimuth     float64 `json:"azimuth,omitempty"`
	Elevation   float64 `json:"elevation,omitempty"`
	Measurement string  `json:"measurement"`
}

// APIResponse is the /api/v1/satellites response body.
type APIResponse struct {
	Updated     time.Time   `json:"updated"`
	Count       int         `json:"count"`
	Window      string      `json:"window"`
	Measurement string      `json:"measurement"`
	Database    string      `json:"database"`
	Satellites  []Satellite `json:"satellites"`
}

type influx1Client struct {
	baseURL  string
	database string
	user     string
	password string
	hc       *http.Client
}

func (c *influx1Client) query(ctx context.Context, q string) (*influxQueryResult, error) {
	u, err := url.Parse(strings.TrimRight(c.baseURL, "/") + "/query")
	if err != nil {
		return nil, err
	}
	v := u.Query()
	v.Set("q", q)
	v.Set("db", c.database)
	u.RawQuery = v.Encode()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u.String(), nil)
	if err != nil {
		return nil, err
	}
	if c.user != "" {
		req.SetBasicAuth(c.user, c.password)
	}
	req.Header.Set("Accept", "application/json")
	resp, err := c.hc.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	b, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, err
	}
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("influx query HTTP %d: %s", resp.StatusCode, string(b))
	}
	var out influxQueryResult
	if err := json.Unmarshal(b, &out); err != nil {
		return nil, fmt.Errorf("influx json: %w", err)
	}
	if strings.TrimSpace(out.Error) != "" {
		return nil, fmt.Errorf("influx: %s", out.Error)
	}
	return &out, nil
}

// buildSelectPositionsSQL is read-only: latest per (satid,satname) within window, SLIMIT.
func buildSelectPositionsSQL(measurement, window string, limit int) string {
	return fmt.Sprintf(
		`SELECT `+
			`last("satlatitude") AS "lat", `+
			`last("satlongitude") AS "lon", `+
			`last("sataltitude_km") AS "alt_km", `+
			`last("azimuth") AS "az", `+
			`last("elevation") AS "el" `+
			`FROM %q `+
			`WHERE time > now() - %s `+
			`GROUP BY "satid","satname" `+
			`SLIMIT %d`,
		measurement, window, limit,
	)
}

func parsePositionSeries(results *influxQueryResult, measure string) []Satellite {
	if results == nil || len(results.Results) == 0 {
		return nil
	}
	var out []Satellite
	for _, r := range results.Results {
		for _, s := range r.Series {
			if s.Name != "" && !strings.EqualFold(s.Name, measure) {
				continue
			}
			cix := colIndex(s.Columns)
			for _, row := range s.Values {
				if len(row) < 1 {
					continue
				}
				sat, ok := seriesRowToSatellite(s.Tags, cix, row, measure)
				if !ok {
					continue
				}
				out = append(out, sat)
			}
		}
	}
	return out
}

type colMap map[string]int

func colIndex(cols []string) colMap {
	m := make(colMap, len(cols))
	for i, c := range cols {
		m[strings.ToLower(strings.TrimSpace(c))] = i
	}
	return m
}

func (c colMap) get(row []any, name string) (float64, bool) {
	i, ok := c[strings.ToLower(name)]
	if !ok || i >= len(row) {
		return 0, false
	}
	return anyToFloat(row[i])
}

func anyToFloat(a any) (float64, bool) {
	switch v := a.(type) {
	case float64:
		return v, true
	case string:
		f, e := strconv.ParseFloat(v, 64)
		return f, e == nil
	case json.Number:
		f, e := v.Float64()
		return f, e == nil
	case nil:
		return 0, false
	default:
		return 0, false
	}
}

func seriesRowToSatellite(tags map[string]string, c colMap, row []any, measure string) (Satellite, bool) {
	if tags == nil {
		tags = map[string]string{}
	}
	var sat Satellite
	sat.Measurement = measure
	if idStr, ok := tags["satid"]; ok {
		if n, err := strconv.Atoi(strings.TrimSpace(idStr)); err == nil {
			sat.Norad = n
		}
	}
	if n, ok := tags["satname"]; ok {
		sat.Name = n
	}
	la, ok1 := c.get(row, "lat")
	lo, ok2 := c.get(row, "lon")
	if !ok1 || !ok2 {
		if la2, a := c.get(row, "satlatitude"); a {
			la, ok1 = la2, true
		}
		if lo2, a := c.get(row, "satlongitude"); a {
			lo, ok2 = lo2, true
		}
	}
	if !ok1 || !ok2 {
		return Satellite{}, false
	}
	sat.Lat, sat.Lon = la, lo
	altKm, haveAlt := c.get(row, "alt_km")
	if !haveAlt {
		altKm, haveAlt = c.get(row, "alt")
	}
	if haveAlt {
		sat.AltMeters = altKm * 1000
	} else {
		sat.AltMeters = 550_000
	}
	if a, ok := c.get(row, "az"); ok {
		sat.Azimuth = a
	}
	if e, ok := c.get(row, "el"); ok {
		sat.Elevation = e
	}
	if sat.Name == "" {
		if sat.Norad != 0 {
			sat.Name = fmt.Sprintf("NORAD-%d", sat.Norad)
		} else {
			sat.Name = "?"
		}
	}
	return sat, true
}
