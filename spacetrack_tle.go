// Space-Track.org API: https://www.space-track.org/documentation#/api
// Single bulk GP/3le query; Alpha-5 lines are converted before go-satellite (tle_alpha5.go).

package main

import (
	"context"
	"fmt"
	"io"
	"log"
	"net/http"
	"net/http/cookiejar"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"time"
)

const spaceTrackAuthURL = "https://www.space-track.org/ajaxauth/login"

// Full GP list: on-orbit (null decay_date), propagable recent epoch, 3le (name + 2 lines).
// Includes Alpha-5 catalog (NORAD 100k–340k) — parsed in tle_alpha5.go.
// Per Space-Track: at most about one such GP download per hour; see API guidelines.
const spaceTrackGP3LEQuery = "https://www.space-track.org/basicspacedata/query/" +
	"class/gp/" +
	"decay_date/null-val/" +
	"epoch/%3Enow-10/" +
	"orderby/norad_cat_id%20asc/" +
	"format/3le/" +
	"emptyresult/show"

type spaceTrackClient struct {
	identity, password string
	hc                 *http.Client
}

func newSpaceTrackClient(identity, password string) (*spaceTrackClient, error) {
	identity, password = strings.TrimSpace(identity), strings.TrimSpace(password)
	if identity == "" || password == "" {
		return nil, fmt.Errorf("space-track: set -spacetrack-identity and -spacetrack-password (or SPACETRACK_IDENTITY and SPACETRACK_PASSWORD)")
	}
	jar, err := cookiejar.New(nil)
	if err != nil {
		return nil, err
	}
	hc := &http.Client{
		Timeout: 20 * time.Minute,
		Jar:     jar,
	}
	// Identifiable user-agent; avoid empty or generic bot strings.
	hc.Transport = &userAgentRoundTripper{
		T:     http.DefaultTransport,
		agent: "n2yo-influx/1.0 (https://github.com/; Space-Track GP bulk; +https://www.space-track.org/)",
	}
	return &spaceTrackClient{identity: identity, password: password, hc: hc}, nil
}

type userAgentRoundTripper struct {
	T     http.RoundTripper
	agent string
}

func (u *userAgentRoundTripper) RoundTrip(req *http.Request) (*http.Response, error) {
	r := req.Clone(req.Context())
	if r.Header.Get("User-Agent") == "" {
		r.Header.Set("User-Agent", u.agent)
	}
	if u.T == nil {
		return http.DefaultTransport.RoundTrip(r)
	}
	return u.T.RoundTrip(r)
}

func (c *spaceTrackClient) login(ctx context.Context) error {
	form := url.Values{}
	form.Set("identity", c.identity)
	form.Set("password", c.password)
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, spaceTrackAuthURL, strings.NewReader(form.Encode()))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	resp, err := c.hc.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	_, _ = io.Copy(io.Discard, resp.Body)
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("space-track login: HTTP %d (check credentials and account status)", resp.StatusCode)
	}
	return nil
}

// fetch3LEOnOrbit downloads the bulk GP 3le set. Logs in, then GETs. Retries once after re-login on 401/403.
func (c *spaceTrackClient) fetch3LEOnOrbit(ctx context.Context) ([]byte, error) {
	doGet := func() (*http.Response, error) {
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, spaceTrackGP3LEQuery, nil)
		if err != nil {
			return nil, err
		}
		return c.hc.Do(req)
	}
	if err := c.login(ctx); err != nil {
		return nil, err
	}
	for attempt := 0; attempt < 2; attempt++ {
		resp, err := doGet()
		if err != nil {
			return nil, err
		}
		b, rerr := io.ReadAll(resp.Body)
		_ = resp.Body.Close()
		if rerr != nil {
			return nil, rerr
		}
		if resp.StatusCode == http.StatusOK {
			if len(b) < 200 {
				return nil, fmt.Errorf("space-track: response too small (%d bytes)", len(b))
			}
			return b, nil
		}
		sn := string(b)
		if len(sn) > 500 {
			sn = sn[:500] + "…"
		}
		if (resp.StatusCode == http.StatusUnauthorized || resp.StatusCode == http.StatusForbidden) && attempt == 0 {
			log.Printf("space-track: HTTP %d, re-logging in and retrying", resp.StatusCode)
			if err := c.login(ctx); err != nil {
				return nil, err
			}
			continue
		}
		return nil, fmt.Errorf("space-track: HTTP %d: %s", resp.StatusCode, sn)
	}
	return nil, fmt.Errorf("space-track: could not load GP 3le")
}

// spacetrack3LEFromNetworkWithCache fetches 3le from Space-Track, writes to cache on success, else falls back to disk.
func spacetrack3LEFromNetworkWithCache(ctx context.Context, c *spaceTrackClient, cachePath string) (data []byte, fromDisk bool, err error) {
	b, e := c.fetch3LEOnOrbit(ctx)
	if e == nil {
		if cachePath != "" {
			if mk := os.MkdirAll(filepath.Dir(cachePath), 0o750); mk != nil {
				log.Printf("TLE cache dir: %v", mk)
			} else if w := os.WriteFile(cachePath, b, 0o600); w != nil {
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
		return nil, false, fmt.Errorf("%w; no 3le on disk at %q: %v", e, cachePath, re)
	}
	if len(cached) < 200 {
		return nil, false, fmt.Errorf("%w; cache at %q is too small", e, cachePath)
	}
	log.Printf("using cached Space-Track 3le %s (fetch: %v)", cachePath, e)
	return cached, true, nil
}

// DefaultSpaceTrackCachePath is ~/.cache/n2yo-influx/gp-3le.txt
func DefaultSpaceTrackCachePath() string {
	d, err := os.UserCacheDir()
	if err != nil {
		return filepath.Join(".", "n2yo-influx-gp-3le.txt")
	}
	return filepath.Join(d, "n2yo-influx", "gp-3le.txt")
}

func spaceTrackMinRefresh() time.Duration { return 1 * time.Hour }
