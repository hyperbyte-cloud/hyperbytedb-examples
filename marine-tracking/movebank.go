package main

import (
	"crypto/md5"
	"encoding/csv"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"
)

const (
	movebankDirectReadURL = "https://www.movebank.org/movebank/service/direct-read"
	movebankPublicJSONURL = "https://www.movebank.org/movebank/service/public/json"
	movebankAuthJSONURL   = "https://www.movebank.org/movebank/service/json-auth"
)

type MovebankClient struct {
	HTTPClient *http.Client
	Username   string
	Password   string
	APIToken   string
}

func NewMovebankClient(username, password, apiToken string) *MovebankClient {
	return &MovebankClient{
		HTTPClient: &http.Client{Timeout: 10 * time.Minute},
		Username:   username,
		Password:   password,
		APIToken:   apiToken,
	}
}

func (c *MovebankClient) authenticated() bool {
	return c.Username != "" || c.APIToken != ""
}

func (c *MovebankClient) requestToken() (string, error) {
	if c.Username == "" || c.Password == "" {
		return "", fmt.Errorf("username and password required to request an API token")
	}
	req, err := http.NewRequest(http.MethodGet, movebankDirectReadURL, nil)
	if err != nil {
		return "", err
	}
	q := req.URL.Query()
	q.Set("service", "request-token")
	req.URL.RawQuery = q.Encode()
	req.SetBasicAuth(c.Username, c.Password)

	resp, err := c.HTTPClient.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return "", err
	}
	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("request token failed (status %d): %s", resp.StatusCode, body)
	}
	return strings.TrimSpace(string(body)), nil
}

func (c *MovebankClient) directRead(params url.Values) ([]byte, error) {
	if c.APIToken == "" && c.Username != "" && c.Password != "" {
		token, err := c.requestToken()
		if err != nil {
			return nil, fmt.Errorf("api token: %w", err)
		}
		c.APIToken = token
	}

	req, err := http.NewRequest(http.MethodGet, movebankDirectReadURL, nil)
	if err != nil {
		return nil, err
	}
	req.URL.RawQuery = params.Encode()
	if c.APIToken != "" {
		q := req.URL.Query()
		q.Set("api-token", c.APIToken)
		req.URL.RawQuery = q.Encode()
	} else if c.Username != "" && c.Password != "" {
		req.SetBasicAuth(c.Username, c.Password)
	}

	resp, err := c.HTTPClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, err
	}
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("direct-read failed (status %d): %s", resp.StatusCode, truncate(body, 500))
	}

	if strings.Contains(string(body), "License Terms:") || resp.Header.Get("accept-license") == "true" {
		hash := md5.Sum(body)
		licenseMD5 := hex.EncodeToString(hash[:])

		req2, err := http.NewRequest(http.MethodGet, movebankDirectReadURL, nil)
		if err != nil {
			return nil, err
		}
		q := req.URL.Query()
		q.Set("license-md5", licenseMD5)
		req2.URL.RawQuery = q.Encode()
		if c.APIToken != "" {
			q.Set("api-token", c.APIToken)
			req2.URL.RawQuery = q.Encode()
		} else if c.Username != "" && c.Password != "" {
			req2.SetBasicAuth(c.Username, c.Password)
		}
		for _, cookie := range resp.Cookies() {
			req2.AddCookie(cookie)
		}

		resp2, err := c.HTTPClient.Do(req2)
		if err != nil {
			return nil, err
		}
		defer resp2.Body.Close()
		body, err = io.ReadAll(resp2.Body)
		if err != nil {
			return nil, err
		}
		if resp2.StatusCode != http.StatusOK {
			return nil, fmt.Errorf("direct-read after license accept failed (status %d): %s", resp2.StatusCode, truncate(body, 500))
		}
	}

	if strings.Contains(string(body), "No data available") {
		return nil, fmt.Errorf("no data available for this request; check study permissions")
	}
	if strings.HasPrefix(strings.TrimSpace(string(body)), "<") {
		return nil, fmt.Errorf("unexpected HTML response: %s", truncate(body, 500))
	}

	return body, nil
}

func truncate(b []byte, n int) string {
	if len(b) <= n {
		return string(b)
	}
	return string(b[:n]) + "..."
}

type StudyInfo struct {
	ID   int64
	Name string
}

func (c *MovebankClient) GetStudy(studyID int64) (*StudyInfo, error) {
	params := url.Values{}
	params.Set("entity_type", "study")
	params.Set("study_id", strconv.FormatInt(studyID, 10))
	params.Set("attributes", "id,name")

	body, err := c.directRead(params)
	if err != nil {
		return nil, err
	}
	rows, err := parseCSVRecords(body)
	if err != nil {
		return nil, err
	}
	if len(rows) == 0 {
		return nil, fmt.Errorf("study %d not found", studyID)
	}
	id, _ := parseOptionalInt64(rows[0]["id"])
	name := rows[0]["name"]
	return &StudyInfo{ID: id, Name: name}, nil
}

func (c *MovebankClient) ListIndividuals(studyID int64) ([]string, error) {
	params := url.Values{}
	params.Set("entity_type", "individual")
	params.Set("study_id", strconv.FormatInt(studyID, 10))
	params.Set("attributes", "local_identifier")

	body, err := c.directRead(params)
	if err != nil {
		return nil, err
	}
	rows, err := parseCSVRecords(body)
	if err != nil {
		return nil, err
	}

	animals := make([]string, 0, len(rows))
	for _, row := range rows {
		id := strings.Trim(row["local_identifier"], `"`)
		if id != "" {
			animals = append(animals, id)
		}
	}
	return animals, nil
}

func (c *MovebankClient) FetchEventsCSV(opts FetchOptions) ([]TrackingPoint, error) {
	params := url.Values{}
	params.Set("entity_type", "event")
	params.Set("study_id", strconv.FormatInt(opts.StudyID, 10))
	params.Set("sensor_type_id", strconv.Itoa(opts.SensorTypeID))
	params.Set(
		"attributes",
		"individual_local_identifier,tag_local_identifier,timestamp,location_long,location_lat,visible,individual_taxon_canonical_name,ground_speed,heading,individual_id,tag_id,deployment_id",
	)
	if len(opts.Animals) > 0 {
		params.Set("individual_local_identifier", strings.Join(opts.Animals, ","))
	}
	if opts.TimestampStart != "" {
		params.Set("timestamp_start", opts.TimestampStart)
	}
	if opts.TimestampEnd != "" {
		params.Set("timestamp_end", opts.TimestampEnd)
	}
	if opts.ReductionProfile != "" {
		params.Set("event_reduction_profile", opts.ReductionProfile)
	}

	body, err := c.directRead(params)
	if err != nil {
		return nil, err
	}
	return csvEventsToPoints(body, opts.StudyID, opts.SensorType)
}

type FetchOptions struct {
	StudyID          int64
	SensorTypeID     int
	SensorType       string
	Animals          []string
	MaxEventsPerAnimal int
	TimestampStart   string
	TimestampEnd     string
	TimestampStartMS int64
	TimestampEndMS   int64
	ReductionProfile string
}

type jsonIndividual struct {
	StudyID                     int64  `json:"study_id"`
	IndividualLocalIdentifier   string `json:"individual_local_identifier"`
	IndividualTaxonCanonicalName string `json:"individual_taxon_canonical_name"`
	IndividualID                int64  `json:"individual_id"`
	SensorTypeID                int64  `json:"sensor_type_id"`
	Locations                   []map[string]json.RawMessage `json:"locations"`
}

type jsonResponse struct {
	Individuals []jsonIndividual `json:"individuals"`
}

func (c *MovebankClient) FetchEventsJSON(opts FetchOptions) ([]TrackingPoint, error) {
	baseURL := movebankPublicJSONURL
	if c.authenticated() {
		baseURL = movebankAuthJSONURL
	}

	params := url.Values{}
	params.Set("study_id", strconv.FormatInt(opts.StudyID, 10))
	params.Set("sensor_type", opts.SensorType)
	if opts.MaxEventsPerAnimal > 0 {
		params.Set("max_events_per_individual", strconv.Itoa(opts.MaxEventsPerAnimal))
	}
	if opts.TimestampStartMS > 0 {
		params.Set("timestamp_start", strconv.FormatInt(opts.TimestampStartMS, 10))
	}
	if opts.TimestampEndMS > 0 {
		params.Set("timestamp_end", strconv.FormatInt(opts.TimestampEndMS, 10))
	}
	if opts.ReductionProfile != "" {
		params.Set("event_reduction_profile", opts.ReductionProfile)
	}
	params.Set("attributes", "timestamp,location_long,location_lat,ground_speed,heading,visible")

	for _, animal := range opts.Animals {
		params.Add("individual_local_identifiers", animal)
	}

	req, err := http.NewRequest(http.MethodGet, baseURL, nil)
	if err != nil {
		return nil, err
	}
	req.URL.RawQuery = params.Encode()
	if c.authenticated() && c.Username != "" && c.Password != "" {
		req.SetBasicAuth(c.Username, c.Password)
	}

	resp, err := c.HTTPClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, err
	}
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("json fetch failed (status %d): %s", resp.StatusCode, truncate(body, 500))
	}

	var payload jsonResponse
	if err := json.Unmarshal(body, &payload); err != nil {
		return nil, fmt.Errorf("decode json: %w", err)
	}

	points := make([]TrackingPoint, 0)
	for _, individual := range payload.Individuals {
		for _, loc := range individual.Locations {
			point := TrackingPoint{
				StudyID:         individual.StudyID,
				AnimalID:        individual.IndividualLocalIdentifier,
				Taxon:           individual.IndividualTaxonCanonicalName,
				SensorType:      opts.SensorType,
				IndividualID:    individual.IndividualID,
				HasIndividualID: individual.IndividualID > 0,
			}
			if ts, ok := decodeJSONInt64(loc["timestamp"]); ok {
				point.TimestampNS = millisToNanos(ts)
			}
			if lat, ok := decodeJSONFloat(loc["location_lat"]); ok {
				point.Lat = lat
				point.HasLat = true
			}
			if lon, ok := decodeJSONFloat(loc["location_long"]); ok {
				point.Lon = lon
				point.HasLon = true
			}
			if speed, ok := decodeJSONFloat(loc["ground_speed"]); ok {
				point.GroundSpeed = speed
				point.HasGroundSpeed = true
			}
			if heading, ok := decodeJSONFloat(loc["heading"]); ok {
				point.Heading = heading
				point.HasHeading = true
			}
			if visible, ok := decodeJSONBool(loc["visible"]); ok {
				point.Visible = visible
				point.HasVisible = true
			}
			if point.TimestampNS == 0 || (!point.HasLat && !point.HasLon) {
				continue
			}
			points = append(points, point)
		}
	}
	return points, nil
}

func (c *MovebankClient) DiscoverAnimalsPublic(studyID int64, sensorType string) ([]string, error) {
	opts := FetchOptions{
		StudyID:            studyID,
		SensorType:         sensorType,
		MaxEventsPerAnimal: 1,
	}
	points, err := c.FetchEventsJSON(opts)
	if err != nil {
		return nil, err
	}
	seen := make(map[string]struct{})
	animals := make([]string, 0)
	for _, p := range points {
		if p.AnimalID == "" {
			continue
		}
		if _, ok := seen[p.AnimalID]; ok {
			continue
		}
		seen[p.AnimalID] = struct{}{}
		animals = append(animals, p.AnimalID)
	}
	return animals, nil
}

func parseCSVRecords(body []byte) ([]map[string]string, error) {
	reader := csv.NewReader(strings.NewReader(string(body)))
	reader.LazyQuotes = true
	reader.TrimLeadingSpace = true

	records, err := reader.ReadAll()
	if err != nil {
		return nil, err
	}
	if len(records) < 2 {
		return nil, nil
	}

	headers := records[0]
	rows := make([]map[string]string, 0, len(records)-1)
	for _, record := range records[1:] {
		row := make(map[string]string, len(headers))
		for i, header := range headers {
			if i < len(record) {
				row[header] = record[i]
			}
		}
		rows = append(rows, row)
	}
	return rows, nil
}

func csvEventsToPoints(body []byte, studyID int64, sensorType string) ([]TrackingPoint, error) {
	rows, err := parseCSVRecords(body)
	if err != nil {
		return nil, err
	}

	points := make([]TrackingPoint, 0, len(rows))
	for _, row := range rows {
		ts, err := parseMovebankTimestamp(strings.Trim(row["timestamp"], `"`))
		if err != nil {
			continue
		}

		point := TrackingPoint{
			StudyID:    studyID,
			AnimalID:   strings.Trim(row["individual_local_identifier"], `"`),
			TagID:      strings.Trim(row["tag_local_identifier"], `"`),
			Taxon:      strings.Trim(row["individual_taxon_canonical_name"], `"`),
			SensorType: sensorType,
			TimestampNS: ts,
		}
		if lat, ok := parseOptionalFloat(strings.Trim(row["location_lat"], `"`)); ok {
			point.Lat = lat
			point.HasLat = true
		}
		if lon, ok := parseOptionalFloat(strings.Trim(row["location_long"], `"`)); ok {
			point.Lon = lon
			point.HasLon = true
		}
		if speed, ok := parseOptionalFloat(strings.Trim(row["ground_speed"], `"`)); ok {
			point.GroundSpeed = speed
			point.HasGroundSpeed = true
		}
		if heading, ok := parseOptionalFloat(strings.Trim(row["heading"], `"`)); ok {
			point.Heading = heading
			point.HasHeading = true
		}
		if visible, ok := parseOptionalBool(strings.Trim(row["visible"], `"`)); ok {
			point.Visible = visible
			point.HasVisible = true
		}
		if id, ok := parseOptionalInt64(strings.Trim(row["individual_id"], `"`)); ok {
			point.IndividualID = id
			point.HasIndividualID = true
		}
		if id, ok := parseOptionalInt64(strings.Trim(row["tag_id"], `"`)); ok {
			point.TagNumericID = id
			point.HasTagNumericID = true
		}
		if id, ok := parseOptionalInt64(strings.Trim(row["deployment_id"], `"`)); ok {
			point.DeploymentID = id
			point.HasDeploymentID = true
		}
		if !point.HasLat && !point.HasLon {
			continue
		}
		points = append(points, point)
	}
	return points, nil
}

func decodeJSONFloat(raw json.RawMessage) (float64, bool) {
	if len(raw) == 0 {
		return 0, false
	}
	var f float64
	if err := json.Unmarshal(raw, &f); err == nil {
		return f, true
	}
	var s string
	if err := json.Unmarshal(raw, &s); err == nil {
		return parseOptionalFloat(s)
	}
	return 0, false
}

func decodeJSONInt64(raw json.RawMessage) (int64, bool) {
	if len(raw) == 0 {
		return 0, false
	}
	var i int64
	if err := json.Unmarshal(raw, &i); err == nil {
		return i, true
	}
	var f float64
	if err := json.Unmarshal(raw, &f); err == nil {
		return int64(f), true
	}
	return 0, false
}

func decodeJSONBool(raw json.RawMessage) (bool, bool) {
	if len(raw) == 0 {
		return false, false
	}
	var b bool
	if err := json.Unmarshal(raw, &b); err == nil {
		return b, true
	}
	var s string
	if err := json.Unmarshal(raw, &s); err == nil {
		return parseOptionalBool(s)
	}
	return false, false
}
