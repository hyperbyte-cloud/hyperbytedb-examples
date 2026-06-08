package main

import (
	"fmt"
	"strconv"
	"strings"
	"time"
)

const movebankTimestampLayout = "2006-01-02 15:04:05.000"

func escapeTagValue(s string) string {
	s = strings.ReplaceAll(s, `\`, `\\`)
	s = strings.ReplaceAll(s, " ", `\ `)
	s = strings.ReplaceAll(s, ",", `\,`)
	s = strings.ReplaceAll(s, "=", `\=`)
	return s
}

func formatFloatField(name string, value float64, fields *[]string) {
	*fields = append(*fields, fmt.Sprintf("%s=%s", name, strconv.FormatFloat(value, 'f', -1, 64)))
}

func formatIntField(name string, value int64, fields *[]string) {
	*fields = append(*fields, fmt.Sprintf("%s=%di", name, value))
}

func formatBoolField(name string, value bool, fields *[]string) {
	*fields = append(*fields, fmt.Sprintf("%s=%t", name, value))
}

type TrackingPoint struct {
	StudyID       int64
	AnimalID      string
	TagID         string
	Taxon         string
	SensorType    string
	TimestampNS   int64
	Lat           float64
	Lon           float64
	HasLat        bool
	HasLon        bool
	GroundSpeed   float64
	HasGroundSpeed bool
	Heading       float64
	HasHeading    bool
	Visible       bool
	HasVisible    bool
	IndividualID  int64
	HasIndividualID bool
	TagNumericID  int64
	HasTagNumericID bool
	DeploymentID  int64
	HasDeploymentID bool
}

func (p TrackingPoint) ToLineProtocol() string {
	tags := []string{
		fmt.Sprintf("study_id=%s", escapeTagValue(strconv.FormatInt(p.StudyID, 10))),
		fmt.Sprintf("animal=%s", escapeTagValue(p.AnimalID)),
	}
	if p.TagID != "" {
		tags = append(tags, fmt.Sprintf("tag=%s", escapeTagValue(p.TagID)))
	}
	if p.Taxon != "" {
		tags = append(tags, fmt.Sprintf("taxon=%s", escapeTagValue(p.Taxon)))
	}
	if p.SensorType != "" {
		tags = append(tags, fmt.Sprintf("sensor=%s", escapeTagValue(p.SensorType)))
	}

	var fields []string
	if p.HasLat {
		formatFloatField("lat", p.Lat, &fields)
	}
	if p.HasLon {
		formatFloatField("lon", p.Lon, &fields)
	}
	if p.HasGroundSpeed {
		formatFloatField("ground_speed", p.GroundSpeed, &fields)
	}
	if p.HasHeading {
		formatFloatField("heading", p.Heading, &fields)
	}
	if p.HasVisible {
		formatBoolField("visible", p.Visible, &fields)
	}
	if p.HasIndividualID {
		formatIntField("individual_id", p.IndividualID, &fields)
	}
	if p.HasTagNumericID {
		formatIntField("tag_id", p.TagNumericID, &fields)
	}
	if p.HasDeploymentID {
		formatIntField("deployment_id", p.DeploymentID, &fields)
	}

	if len(fields) == 0 {
		return ""
	}

	return fmt.Sprintf(
		"animal_location,%s %s %d",
		strings.Join(tags, ","),
		strings.Join(fields, ","),
		p.TimestampNS,
	)
}

func parseMovebankTimestamp(value string) (int64, error) {
	if value == "" {
		return 0, fmt.Errorf("empty timestamp")
	}
	t, err := time.ParseInLocation(movebankTimestampLayout, value, time.UTC)
	if err != nil {
		return 0, err
	}
	return t.UnixNano(), nil
}

func millisToNanos(ms int64) int64 {
	return ms * int64(time.Millisecond)
}

func parseOptionalFloat(value string) (float64, bool) {
	if value == "" {
		return 0, false
	}
	f, err := strconv.ParseFloat(value, 64)
	if err != nil {
		return 0, false
	}
	return f, true
}

func parseOptionalInt64(value string) (int64, bool) {
	if value == "" {
		return 0, false
	}
	i, err := strconv.ParseInt(value, 10, 64)
	if err != nil {
		return 0, false
	}
	return i, true
}

func parseOptionalBool(value string) (bool, bool) {
	switch strings.ToLower(strings.TrimSpace(value)) {
	case "true", "t", "1":
		return true, true
	case "false", "f", "0":
		return false, true
	default:
		return false, false
	}
}
