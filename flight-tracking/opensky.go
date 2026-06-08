package main

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"
)

const openskyBaseURL = "https://opensky-network.org/api"

type OpenSkyClient struct {
	HTTPClient *http.Client
	Username   string
	Password   string
}

func NewOpenSkyClient(username, password string) *OpenSkyClient {
	return &OpenSkyClient{
		HTTPClient: &http.Client{Timeout: 30 * time.Second},
		Username:   username,
		Password:   password,
	}
}

type BoundingBox struct {
	MinLat float64
	MinLon float64
	MaxLat float64
	MaxLon float64
}

func (b BoundingBox) Valid() bool {
	return b.MinLat != 0 || b.MinLon != 0 || b.MaxLat != 0 || b.MaxLon != 0
}

type FetchOptions struct {
	BoundingBox BoundingBox
	ICAO24      []string
	Time        int64
	Extended    bool
}

type statesResponse struct {
	Time   int64           `json:"time"`
	States [][]interface{} `json:"states"`
}

func (c *OpenSkyClient) FetchStates(opts FetchOptions) ([]AircraftState, int64, error) {
	params := url.Values{}
	if opts.BoundingBox.Valid() {
		params.Set("lamin", formatCoord(opts.BoundingBox.MinLat))
		params.Set("lomin", formatCoord(opts.BoundingBox.MinLon))
		params.Set("lamax", formatCoord(opts.BoundingBox.MaxLat))
		params.Set("lomax", formatCoord(opts.BoundingBox.MaxLon))
	}
	for _, icao := range opts.ICAO24 {
		params.Add("icao24", strings.ToLower(strings.TrimSpace(icao)))
	}
	if opts.Time > 0 {
		params.Set("time", strconv.FormatInt(opts.Time, 10))
	}
	if opts.Extended {
		params.Set("extended", "1")
	}

	reqURL := openskyBaseURL + "/states/all"
	if encoded := params.Encode(); encoded != "" {
		reqURL += "?" + encoded
	}

	req, err := http.NewRequest(http.MethodGet, reqURL, nil)
	if err != nil {
		return nil, 0, err
	}
	if c.Username != "" && c.Password != "" {
		req.SetBasicAuth(c.Username, c.Password)
	}

	resp, err := c.HTTPClient.Do(req)
	if err != nil {
		return nil, 0, err
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, 0, err
	}

	if resp.StatusCode == http.StatusTooManyRequests {
		return nil, 0, fmt.Errorf("rate limited (429): retry after backoff")
	}
	if resp.StatusCode != http.StatusOK {
		return nil, 0, fmt.Errorf("opensky request failed (status %d): %s", resp.StatusCode, truncate(body, 300))
	}

	var payload statesResponse
	if err := json.Unmarshal(body, &payload); err != nil {
		return nil, 0, fmt.Errorf("decode opensky response: %w", err)
	}

	states := make([]AircraftState, 0, len(payload.States))
	for _, row := range payload.States {
		state, ok := parseStateRow(row, payload.Time)
		if ok {
			states = append(states, state)
		}
	}
	return states, payload.Time, nil
}

func parseStateRow(row []interface{}, responseTime int64) (AircraftState, bool) {
	if len(row) < 17 {
		return AircraftState{}, false
	}

	state := AircraftState{
		ICAO24:        asString(row[0]),
		Callsign:      asString(row[1]),
		OriginCountry: asString(row[2]),
		TimePosition:  asInt64(row[3]),
		LastContact:   asInt64(row[4]),
	}

	if lon, ok := asFloat(row[5]); ok {
		state.Lon = lon
		state.HasLon = true
	}
	if lat, ok := asFloat(row[6]); ok {
		state.Lat = lat
		state.HasLat = true
	}
	if alt, ok := asFloat(row[7]); ok {
		state.BaroAltitude = alt
		state.HasBaroAltitude = true
	}
	if onGround, ok := asBool(row[8]); ok {
		state.OnGround = onGround
		state.HasOnGround = true
	}
	if velocity, ok := asFloat(row[9]); ok {
		state.Velocity = velocity
		state.HasVelocity = true
	}
	if track, ok := asFloat(row[10]); ok {
		state.TrueTrack = track
		state.HasTrueTrack = true
	}
	if vrate, ok := asFloat(row[11]); ok {
		state.VerticalRate = vrate
		state.HasVerticalRate = true
	}
	if geoAlt, ok := asFloat(row[13]); ok {
		state.GeoAltitude = geoAlt
		state.HasGeoAltitude = true
	}
	state.Squawk = asString(row[14])
	if spi, ok := asBool(row[15]); ok {
		state.SPI = spi
		state.HasSPI = true
	}
	if posSrc, ok := asInt(row[16]); ok {
		state.PositionSource = posSrc
		state.HasPositionSource = true
	}
	if len(row) > 17 {
		if category, ok := asInt(row[17]); ok {
			state.Category = category
			state.HasCategory = true
		}
	}

	switch {
	case state.TimePosition > 0:
		state.TimestampNS = state.TimePosition * int64(time.Second)
	case state.LastContact > 0:
		state.TimestampNS = state.LastContact * int64(time.Second)
	case responseTime > 0:
		state.TimestampNS = responseTime * int64(time.Second)
	default:
		return AircraftState{}, false
	}

	if state.ICAO24 == "" {
		return AircraftState{}, false
	}
	return state, true
}

func asString(v interface{}) string {
	if v == nil {
		return ""
	}
	switch value := v.(type) {
	case string:
		return value
	default:
		return fmt.Sprint(value)
	}
}

func asFloat(v interface{}) (float64, bool) {
	if v == nil {
		return 0, false
	}
	switch value := v.(type) {
	case float64:
		return value, true
	case json.Number:
		f, err := value.Float64()
		return f, err == nil
	default:
		return 0, false
	}
}

func asInt(v interface{}) (int, bool) {
	if v == nil {
		return 0, false
	}
	switch value := v.(type) {
	case float64:
		return int(value), true
	case json.Number:
		i, err := value.Int64()
		return int(i), err == nil
	default:
		return 0, false
	}
}

func asInt64(v interface{}) int64 {
	if v == nil {
		return 0
	}
	switch value := v.(type) {
	case float64:
		return int64(value)
	case json.Number:
		i, err := value.Int64()
		if err != nil {
			return 0
		}
		return i
	default:
		return 0
	}
}

func asBool(v interface{}) (bool, bool) {
	if v == nil {
		return false, false
	}
	switch value := v.(type) {
	case bool:
		return value, true
	default:
		return false, false
	}
}

func formatCoord(v float64) string {
	return strconv.FormatFloat(v, 'f', -1, 64)
}

func truncate(b []byte, n int) string {
	if len(b) <= n {
		return string(b)
	}
	return string(b[:n]) + "..."
}
