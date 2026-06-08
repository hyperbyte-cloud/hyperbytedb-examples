package main

import (
	"fmt"
	"strconv"
	"strings"
)

func escapeTagValue(s string) string {
	s = strings.ReplaceAll(s, `\`, `\\`)
	s = strings.ReplaceAll(s, " ", `\ `)
	s = strings.ReplaceAll(s, ",", `\,`)
	s = strings.ReplaceAll(s, "=", `\=`)
	return s
}

func escapeStringField(s string) string {
	s = strings.ReplaceAll(s, `\`, `\\`)
	s = strings.ReplaceAll(s, `"`, `\"`)
	return s
}

var positionSourceNames = map[int]string{
	0: "adsb",
	1: "asterix",
	2: "mlat",
	3: "flarm",
}

type AircraftState struct {
	ICAO24        string
	Callsign      string
	OriginCountry string
	TimePosition  int64
	LastContact   int64
	Lat           float64
	Lon           float64
	HasLat        bool
	HasLon        bool
	BaroAltitude  float64
	HasBaroAltitude bool
	OnGround      bool
	HasOnGround   bool
	Velocity      float64
	HasVelocity   bool
	TrueTrack     float64
	HasTrueTrack  bool
	VerticalRate  float64
	HasVerticalRate bool
	GeoAltitude   float64
	HasGeoAltitude bool
	Squawk        string
	SPI           bool
	HasSPI        bool
	PositionSource int
	HasPositionSource bool
	Category      int
	HasCategory   bool
	TimestampNS   int64
}

func (s AircraftState) ToLineProtocol() string {
	if s.ICAO24 == "" || s.TimestampNS == 0 {
		return ""
	}

	tags := []string{
		fmt.Sprintf("icao24=%s", escapeTagValue(s.ICAO24)),
	}
	if callsign := strings.TrimSpace(s.Callsign); callsign != "" {
		tags = append(tags, fmt.Sprintf("callsign=%s", escapeTagValue(callsign)))
	}
	if s.OriginCountry != "" {
		tags = append(tags, fmt.Sprintf("origin_country=%s", escapeTagValue(s.OriginCountry)))
	}
	if s.HasPositionSource {
		name := positionSourceNames[s.PositionSource]
		if name == "" {
			name = strconv.Itoa(s.PositionSource)
		}
		tags = append(tags, fmt.Sprintf("position_source=%s", escapeTagValue(name)))
	}

	var fields []string
	if s.HasLat {
		fields = append(fields, fmt.Sprintf("lat=%s", formatFloat(s.Lat)))
	}
	if s.HasLon {
		fields = append(fields, fmt.Sprintf("lon=%s", formatFloat(s.Lon)))
	}
	if s.HasBaroAltitude {
		fields = append(fields, fmt.Sprintf("baro_altitude=%s", formatFloat(s.BaroAltitude)))
	}
	if s.HasGeoAltitude {
		fields = append(fields, fmt.Sprintf("geo_altitude=%s", formatFloat(s.GeoAltitude)))
	}
	if s.HasVelocity {
		fields = append(fields, fmt.Sprintf("velocity=%s", formatFloat(s.Velocity)))
	}
	if s.HasTrueTrack {
		fields = append(fields, fmt.Sprintf("true_track=%s", formatFloat(s.TrueTrack)))
	}
	if s.HasVerticalRate {
		fields = append(fields, fmt.Sprintf("vertical_rate=%s", formatFloat(s.VerticalRate)))
	}
	if s.HasOnGround {
		fields = append(fields, fmt.Sprintf("on_ground=%t", s.OnGround))
	}
	if s.HasSPI {
		fields = append(fields, fmt.Sprintf("spi=%t", s.SPI))
	}
	if s.HasCategory {
		fields = append(fields, fmt.Sprintf("category=%di", s.Category))
	}
	if s.TimePosition > 0 {
		fields = append(fields, fmt.Sprintf("time_position=%di", s.TimePosition))
	}
	if s.LastContact > 0 {
		fields = append(fields, fmt.Sprintf("last_contact=%di", s.LastContact))
	}
	if s.Squawk != "" {
		fields = append(fields, fmt.Sprintf("squawk=\"%s\"", escapeStringField(s.Squawk)))
	}

	if len(fields) == 0 {
		return ""
	}

	return fmt.Sprintf(
		"aircraft_state,%s %s %d",
		strings.Join(tags, ","),
		strings.Join(fields, ","),
		s.TimestampNS,
	)
}

func formatFloat(v float64) string {
	return strconv.FormatFloat(v, 'f', -1, 64)
}

func statesToLines(states []AircraftState) []string {
	lines := make([]string, 0, len(states))
	for _, state := range states {
		if line := state.ToLineProtocol(); line != "" {
			lines = append(lines, line)
		}
	}
	return lines
}

func writeStates(writer *InfluxWriter, states []AircraftState, batchSize int) (int, error) {
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

	for _, state := range states {
		line := state.ToLineProtocol()
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
