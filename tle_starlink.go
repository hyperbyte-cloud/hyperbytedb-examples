package main

// Celestrak TLE fetch and SGP4 propagation (all Starlink sats) — no N2YO per satellite.

import (
	"context"
	"fmt"
	"io"
	"log"
	"math"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/joshuaferrara/go-satellite"
)

const defaultCelestrakStarlink = "https://celestrak.org/NORAD/elements/gp.php?GROUP=starlink&FORMAT=tle"

type tleObject struct {
	Name  string
	NORAD int
	Sat   satellite.Satellite // initialized from TLE; reused each propagate step
}

func fetchCelestrakTLE(ctx context.Context, client *http.Client, rawURL string) ([]byte, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, rawURL, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("User-Agent", "n2yo-influx/1.0 (SGP4; +https://celestrak.org/)")
	resp, err := client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	b, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, err
	}
	if resp.StatusCode != http.StatusOK {
		sn := string(b)
		if len(sn) > 500 {
			sn = sn[:500] + "…"
		}
		if resp.StatusCode == http.StatusForbidden {
			return nil, fmt.Errorf("HTTP 403: celestrak may rate-limit the same TLE set (often 2h between downloads): %s", sn)
		}
		return nil, fmt.Errorf("HTTP %d: %s", resp.StatusCode, sn)
	}
	return b, nil
}

// DefaultTLECachePath returns a path like ~/.cache/n2yo-influx/starlink.tle
func DefaultTLECachePath() string {
	d, err := os.UserCacheDir()
	if err != nil {
		return filepath.Join(".", "n2yo-influx-starlink.tle")
	}
	return filepath.Join(d, "n2yo-influx", "starlink.tle")
}

// tleFromNetworkWithCache fetches TLEs from the URL; on success it writes to cachePath (if set).
// On any HTTP/network error, it reads and returns the last good file from cachePath.
func tleFromNetworkWithCache(
	ctx context.Context,
	client *http.Client,
	tleURL, cachePath string,
) (data []byte, fromDisk bool, err error) {
	b, e := fetchCelestrakTLE(ctx, client, tleURL)
	if e == nil {
		if cachePath != "" {
			if mk := os.MkdirAll(filepath.Dir(cachePath), 0o750); mk != nil {
				log.Printf("TLE cache dir: %v", mk)
			} else if w := os.WriteFile(cachePath, b, 0o644); w != nil {
				log.Printf("TLE cache write %s: %v", cachePath, w)
			}
		}
		return b, false, nil
	}
	if cachePath == "" {
		return nil, false, e
	}
	cached, re := os.ReadFile(cachePath)
	if re != nil {
		return nil, false, fmt.Errorf("%w; no TLE on disk at %q: %v", e, cachePath, re)
	}
	if len(cached) < 200 {
		return nil, false, fmt.Errorf("%w; TLE on disk at %q is too small", e, cachePath)
	}
	log.Printf("using cached TLE %s (Celestrak: %v)", cachePath, e)
	return cached, true, nil
}

// parse3LE consumes Celestrak 3-line TLE groups: name, line1, line2
func parseCelestrakTLE(data []byte) ([]tleObject, error) {
	lines := strings.Split(string(data), "\n")
	var out []tleObject
	for i := 0; i+2 < len(lines); {
		name := strings.TrimSpace(lines[i])
		l1 := strings.TrimRight(lines[i+1], "\r")
		l2 := strings.TrimRight(lines[i+2], "\r")
		if !strings.HasPrefix(l1, "1 ") || !strings.HasPrefix(l2, "2 ") {
			i++
			continue
		}
		if len(l1) < 64 || len(l2) < 64 {
			i += 3
			continue
		}
		norad, err := noradFromTLELine1(l1)
		if err != nil {
			i += 3
			continue
		}
		l1c, l2c := l1, l2
		if needsTLEPatch(l1) {
			if len(l1) < 69 || len(l2) < 69 {
				i += 3
				continue
			}
			l1c, l2c = patchTLEPairForParser(l1, l2, norad)
		}
		sat := satellite.TLEToSat(l1c, l2c, satellite.GravityWGS84)
		if name == "" {
			name = fmt.Sprintf("NORAD-%d", norad)
		}
		out = append(out, tleObject{Name: name, NORAD: norad, Sat: sat})
		i += 3
	}
	if len(out) == 0 {
		return nil, fmt.Errorf("no TLE elements parsed (expect Celestrack 3-line format)")
	}
	return out, nil
}

// propagateECI at UTC time
func propagateECI(s satellite.Satellite, t time.Time) (pos, vel satellite.Vector3) {
	utc := t.UTC()
	return satellite.Propagate(
		s,
		utc.Year(), int(utc.Month()), utc.Day(),
		utc.Hour(), utc.Minute(), utc.Second(),
	)
}

func llaAndVel(pos, vel satellite.Vector3, t time.Time) (latDeg, lonDeg, altKm, speedKms float64) {
	utc := t.UTC()
	gmst := satellite.GSTimeFromDate(utc.Year(), int(utc.Month()), utc.Day(), utc.Hour(), utc.Minute(), utc.Second())
	altKm, _, lla := satellite.ECIToLLA(pos, gmst)
	deg := satellite.LatLongDeg(lla)
	latDeg = deg.Latitude
	lonDeg = deg.Longitude
	speedKms = math.Sqrt(vel.X*vel.X + vel.Y*vel.Y + vel.Z*vel.Z) // ECI (km/s)
	return
}

// lookAnglesDeg: observer lat/lng in degrees, alt meters; t must match propagation time
func lookAnglesDeg(pos satellite.Vector3, obsLat, obsLng, obsAltM float64, t time.Time) (azDeg, elDeg float64) {
	obs := satellite.LatLong{
		Latitude:  obsLat * math.Pi / 180.0,
		Longitude: obsLng * math.Pi / 180.0,
	}
	utc := t.UTC()
	jd := satellite.JDay(utc.Year(), int(utc.Month()), utc.Day(), utc.Hour(), utc.Minute(), utc.Second())
	la := satellite.ECIToLookAngles(pos, obs, obsAltM/1000.0, jd)
	return la.Az * 180.0 / math.Pi, la.El * 180.0 / math.Pi
}

func lineTLE(
	norad int,
	satname, sourceTag, groupTag string,
	latDeg, lonDeg, altKm, velKms, azDeg, elDeg float64,
	tsUnix int64,
) string {
	name := escapeInfluxTag(satname)
	st := escapeInfluxTag(sourceTag)
	gt := escapeInfluxTag(groupTag)
	if st == "" {
		st = "celestrak"
	}
	if gt == "" {
		gt = "tle"
	}
	return fmt.Sprintf(
		"tle_position,satid=%d,satname=%s,source=%s,group=%s satlatitude=%.7f,satlongitude=%.7f,sataltitude_km=%.4f,velocity_kms=%.5f,azimuth=%.3f,elevation=%.3f,api_tx_count=0i %d",
		norad, name, st, gt, latDeg, lonDeg, altKm, velKms, azDeg, elDeg, tsUnix,
	)
}

func runTLEPass(
	w *influxWriter,
	objs []tleObject,
	obsLat, obsLng, obsAlt float64,
	sourceTag, groupTag string,
) (int, error) {
	now := time.Now().UTC()
	var lines []string
	const batch = 3000
	written := 0
	flush := func() error {
		if len(lines) == 0 {
			return nil
		}
		if err := w.writeLineProtocol(stringsJoinLines(lines)); err != nil {
			return err
		}
		written += len(lines)
		lines = lines[:0]
		return nil
	}

	for _, o := range objs {
		pos, vel := propagateECI(o.Sat, now)
		lat, lon, alt, v := llaAndVel(pos, vel, now)
		az, el := lookAnglesDeg(pos, obsLat, obsLng, obsAlt, now)
		ts := now.Unix()
		lines = append(lines, lineTLE(o.NORAD, o.Name, sourceTag, groupTag, lat, lon, alt, v, az, el, ts))
		if len(lines) >= batch {
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

func stringsJoinLines(lines []string) string { return strings.Join(lines, "\n") }

func refreshTLESet(ctx context.Context, client *http.Client, tleURL, cachePath string) ([]tleObject, error) {
	b, _, err := tleFromNetworkWithCache(ctx, client, tleURL, cachePath)
	if err != nil {
		return nil, err
	}
	return parseCelestrakTLE(b)
}

func refreshTLESetSpaceTrack(ctx context.Context, st *spaceTrackClient, cachePath string) ([]tleObject, error) {
	b, _, err := spacetrack3LEFromNetworkWithCache(ctx, st, cachePath)
	if err != nil {
		return nil, err
	}
	return parseCelestrakTLE(b)
}

func runSGP4Mode(
	influxURL, db, influxUser, influxPass string,
	createDB bool,
	tleURL string,
	tleCachePath string,
	refresh, interval time.Duration,
	obsLat, obsLng, obsAlt float64,
	spacetrack bool,
	spacetrackIdentity, spacetrackPassword, spacetrackCache string,
) {
	w := &influxWriter{
		url:        strings.TrimSuffix(influxURL, "/"),
		database:   db,
		user:       influxUser,
		password:   influxPass,
		httpClient: &http.Client{Timeout: 2 * time.Minute},
	}
	if createDB {
		if err := w.createDatabase(); err != nil {
			log.Fatalf("create database: %v", err)
		}
		log.Printf("database %q created (or already exists)", db)
	}

	celestrackClient := &http.Client{Timeout: 2 * time.Minute}
	var stClient *spaceTrackClient
	if spacetrack {
		if refresh < spaceTrackMinRefresh() {
			log.Printf("space-track: TLE download interval raised to %v (GP guideline: at most about once per hour)", spaceTrackMinRefresh())
			refresh = spaceTrackMinRefresh()
		}
		var err error
		stClient, err = newSpaceTrackClient(spacetrackIdentity, spacetrackPassword)
		if err != nil {
			log.Fatalf("space-track: %v", err)
		}
	}
	var cache []tleObject
	var lastFetch time.Time

	sourceTag, groupTag := "celestrak", "tle"
	if spacetrack {
		sourceTag, groupTag = "spacetrack", "gp"
	} else if strings.Contains(tleURL, "starlink") {
		groupTag = "starlink"
	}

	oneShot := interval == 0
	if oneShot {
		if spacetrack {
			if spacetrackCache != "" {
			log.Printf("one-shot: SGP4 + Space-Track full GP/3le (cache %s)", spacetrackCache)
		} else {
			log.Printf("one-shot: SGP4 + Space-Track full GP/3le (no cache path)")
			}
		} else if tleCachePath != "" {
			log.Printf("one-shot: SGP4 + TLEs from %s (disk cache: %s)", tleURL, tleCachePath)
		} else {
			log.Printf("one-shot: SGP4 + TLEs from %s", tleURL)
		}
	} else {
		if spacetrack {
			log.Printf("SGP4: Space-Track bulk GP (3le, all on-orbit + Alpha-5), cache %s, TLE download every %v, position/write every %v", spacetrackCache, refresh, interval)
		} else if tleCachePath != "" {
			log.Printf("SGP4: TLEs from %s, cache %s, refresh %v, position/write every %v", tleURL, tleCachePath, refresh, interval)
		} else {
			log.Printf("SGP4: TLEs from %s, refresh %v, position/write every %v (no -sgp4-cache: no disk fallback on 403)", tleURL, refresh, interval)
		}
	}

	do := func() {
		need := len(cache) == 0 || time.Since(lastFetch) > refresh
		if need {
			fetchTO := 20 * time.Minute
			if !spacetrack {
				fetchTO = 2 * time.Minute
			}
			ctx, cancel := context.WithTimeout(context.Background(), fetchTO)
			var objs []tleObject
			var err error
			if spacetrack {
				objs, err = refreshTLESetSpaceTrack(ctx, stClient, spacetrackCache)
			} else {
				objs, err = refreshTLESet(ctx, celestrackClient, tleURL, tleCachePath)
			}
			cancel()
			if err != nil {
				if oneShot {
					log.Fatalf("TLE fetch: %v", err)
				}
				log.Printf("TLE fetch error: %v", err)
				return
			}
			cache = objs
			lastFetch = time.Now()
			log.Printf("TLE loaded: %d satellites (source %s, group %s)", len(cache), sourceTag, groupTag)
		}
		n, err := runTLEPass(w, cache, obsLat, obsLng, obsAlt, sourceTag, groupTag)
		if err != nil {
			log.Printf("error: %v", err)
			return
		}
		log.Printf("wrote %d point(s) to %s / db=%q", n, w.url, w.database)
	}

	if interval > 0 {
		do()
		t := time.NewTicker(interval)
		defer t.Stop()
		for range t.C {
			do()
		}
		return
	}
	do()
}
