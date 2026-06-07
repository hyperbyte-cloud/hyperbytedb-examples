package main

import (
	"context"
	"embed"
	"encoding/json"
	"flag"
	"fmt"
	"io/fs"
	"log"
	"net/http"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"
)

//go:embed static
var static embed.FS

func main() {
	listen := flag.String("listen", "127.0.0.1:8090", "HTTP listen address")
	influx := flag.String("influx", "http://127.0.0.1:8086", "InfluxDB 1 base URL (no /query path)")
	db := flag.String("db", "n2yo", "InfluxDB database name")
	influxUser := flag.String("influx-user", "", "InfluxDB username")
	influxPass := flag.String("influx-password", os.Getenv("INFLUX_PASSWORD"), "InfluxDB password")
	measurement := flag.String("measurement", "tle_position", "InfluxDB measurement (tle_position or n2yo_position)")
	window := flag.String("window", "15m", "InfluxQL time window, e.g. 15m, 1h")
	// Default SLIMIT: large values make InfluxDB GROUP BY very slow; use ?limit= for more.
	limit := flag.Int("limit", 20_000, "Default SLIMIT (max unique series); query ?limit=; cap 100000")
	cors := flag.String("cors", "*", "Access-Control-Allow-Origin; empty to disable CORS for browser fetches from other sites")
	readTimeout := flag.Duration("read-timeout", 2*time.Minute, "Handler timeout (Influx + JSON); must cover slow GROUP BY+SLIMIT on big series")
	flag.Parse()

	cl := &influx1Client{
		baseURL:  strings.TrimRight(*influx, "/"),
		database: *db,
		user:     *influxUser,
		password: *influxPass,
		// Must be >= read-timeout on /api/v1/satellites or the client cancels the Influx GET first.
		hc: &http.Client{Timeout: *readTimeout + 5*time.Second},
	}

	mux := http.NewServeMux()
	mux.Handle("/api/v1/healthz", withCORS(*cors, http.HandlerFunc(healthz)))
	mux.Handle("/api/v1/satellites", withCORS(*cors, withTimeout(*readTimeout, handleSatellites(cl, *measurement, *db, *window, *limit))))
	mux.Handle("/viewer", withCORS(*cors, withTimeout(*readTimeout, serveStatic("static/cesium-satellite-viewer.html", "text/html; charset=utf-8"))))
	mux.Handle("/", withCORS(*cors, withTimeout(5*time.Second, http.HandlerFunc(redirectRoot))))

	staticFS, err := fs.Sub(static, "static")
	if err != nil {
		log.Fatalf("static fs: %v", err)
	}
	mux.Handle("/static/", withCORS(*cors, http.StripPrefix("/static/", http.FileServer(http.FS(staticFS)))))

	srv := &http.Server{Addr: *listen, Handler: logReq(mux)}
	go func() {
		log.Printf("Cesium InfluxQL API: http://%s/viewer  (influx: %s db=%s measurement=%s)", *listen, cl.baseURL, *db, *measurement)
		if err := srv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			log.Fatalf("listen: %v", err)
		}
	}()
	ch := make(chan os.Signal, 1)
	signal.Notify(ch, syscall.SIGINT, syscall.SIGTERM)
	<-ch
	_ = srv.Shutdown(context.Background())
}

func healthz(w http.ResponseWriter, r *http.Request) {
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write([]byte("ok\n"))
}

func redirectRoot(w http.ResponseWriter, r *http.Request) {
	if r.URL.Path != "/" {
		http.NotFound(w, r)
		return
	}
	if r.Method != http.MethodGet && r.Method != http.MethodHead {
		w.WriteHeader(http.StatusMethodNotAllowed)
		return
	}
	http.Redirect(w, r, "/viewer", http.StatusFound)
}

func serveStatic(relativePath, ct string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		b, err := static.ReadFile(relativePath)
		if err != nil {
			http.Error(w, err.Error(), http.StatusNotFound)
			return
		}
		w.Header().Set("Content-Type", ct)
		_, _ = w.Write(b)
	}
}

func withTimeout(d time.Duration, h http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		ctx, cancel := context.WithTimeout(r.Context(), d)
		defer cancel()
		h.ServeHTTP(w, r.WithContext(ctx))
	})
}

func withCORS(origin string, h http.Handler) http.Handler {
	if strings.TrimSpace(origin) == "" {
		return h
	}
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Access-Control-Allow-Origin", origin)
		w.Header().Set("Access-Control-Allow-Methods", "GET, OPTIONS")
		w.Header().Set("Access-Control-Allow-Headers", "Content-Type")
		if r.Method == http.MethodOptions {
			w.WriteHeader(http.StatusNoContent)
			return
		}
		h.ServeHTTP(w, r)
	})
}

func logReq(h http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		start := time.Now()
		h.ServeHTTP(w, r)
		log.Printf("%s %s %s", r.Method, r.URL.Path, time.Since(start).Round(time.Millisecond))
	})
}

func handleSatellites(c *influx1Client, defaultMeas, defaultDB, defaultWindow string, defaultLimit int) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			http.Error(w, "method", http.StatusMethodNotAllowed)
			return
		}
		q := r.URL.Query()
		meas := q.Get("measurement")
		if meas == "" {
			meas = defaultMeas
		}
		db := q.Get("db")
		if db == "" {
			db = defaultDB
		}
		win := q.Get("window")
		if win == "" {
			win = defaultWindow
		}
		limit := defaultLimit
		if s := q.Get("limit"); s != "" {
			var err error
			if limit, err = parseLimit(s); err != nil {
				http.Error(w, "bad limit", http.StatusBadRequest)
				return
			}
		}
		if limit < 1 {
			limit = 1
		}
		if limit > 100_000 {
			limit = 100_000
		}
		// re-bind db on client (struct copy)
		one := *c
		one.database = db
		sq := buildSelectPositionsSQL(meas, win, limit)
		res, err := one.query(r.Context(), sq)
		if err != nil {
			http.Error(w, err.Error(), http.StatusBadGateway)
			return
		}
		sats := parsePositionSeries(res, meas)
		log.Printf("api/v1/satellites: returned %d points db=%q measurement=%q window=%s slimit=%d", len(sats), db, meas, win, limit)
		out := APIResponse{
			Updated:     time.Now().UTC(),
			Count:       len(sats),
			Window:      win,
			Measurement: meas,
			Database:    db,
			Satellites:  sats,
		}
		w.Header().Set("Content-Type", "application/json; charset=utf-8")
		enc := json.NewEncoder(w)
		enc.SetIndent("", "  ")
		_ = enc.Encode(out)
	})
}

func parseLimit(s string) (int, error) {
	var n int
	_, err := fmt.Sscanf(s, "%d", &n)
	if err != nil {
		return 0, err
	}
	return n, nil
}
