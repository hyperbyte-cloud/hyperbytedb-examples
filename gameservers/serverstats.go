package main

import (
	"fmt"
	"log"
	"math/rand"
	"strings"
	"sync"
	"time"
)

func writeServerStatsRow(buf *strings.Builder, srv *Server, ts int64) {
	fmt.Fprintf(buf,
		"server_stats,account_service_id=%d,fleet=%s,fleet_id=%s,game_id=%d,location_id=%d,machine_id=%d,map=%s,mod_id=%d,profile_id=%d,provider=%s,region=%s,region_id=%s,server_id=%d cpu=%di,max_players=%di,mem=%di,players=%di,used_slots=%di %d",
		srv.AccountServiceID,
		escapeTag(srv.Fleet),
		escapeTag(srv.FleetID),
		srv.GameID,
		srv.LocationID,
		srv.MachineID,
		escapeTag(srv.Map),
		srv.ModID,
		srv.ProfileID,
		escapeTag(srv.Provider),
		escapeTag(srv.Region),
		escapeTag(srv.RegionID),
		srv.ServerID,
		srv.CPU,
		srv.MaxPlayers,
		srv.MemMB,
		srv.Players,
		srv.UsedSlots,
		ts,
	)
}

func generateServerStats(w *InfluxWriter, sim *Simulation, batchSize, numWorkers int, period time.Duration, jitter float64, concurrency int) {
	total := len(sim.Servers)
	if total <= 0 {
		return
	}
	if period > 0 {
		generateServerStatsPaced(w, sim, period, jitter, concurrency)
	} else {
		generateServerStatsBurst(w, sim, batchSize, numWorkers)
	}
}

func generateServerStatsPaced(w *InfluxWriter, sim *Simulation, period time.Duration, jitter float64, concurrency int) {
	total := len(sim.Servers)
	rng := rand.New(rand.NewSource(time.Now().UnixNano()))

	const sendsPerSec = 10
	numBatches := int(period.Seconds()) * sendsPerSec
	if numBatches < 1 {
		numBatches = 1
	}
	if numBatches > total {
		numBatches = total
	}

	rowsPerBatch := total / numBatches
	extraRows := total % numBatches

	start := time.Now()
	sent := 0
	periodNs := period.Nanoseconds()

	var wg sync.WaitGroup
	sem := make(chan struct{}, concurrency)

	for i := 0; i < numBatches; i++ {
		slotNs := int64(i) * periodNs / int64(numBatches)
		target := start.Add(time.Duration(slotNs))
		if jitter > 0 {
			spanNs := periodNs / int64(numBatches)
			j := int64(float64(spanNs) * jitter)
			if j > 0 {
				target = target.Add(time.Duration(rng.Int63n(2*j+1) - j))
			}
		}
		if d := time.Until(target); d > 0 {
			time.Sleep(d)
		}

		n := rowsPerBatch
		if i < extraRows {
			n++
		}
		if sent+n > total {
			n = total - sent
		}

		ts := start.Add(time.Duration(slotNs)).UnixNano()
		var buf strings.Builder
		for k := 0; k < n; k++ {
			if k > 0 {
				buf.WriteByte('\n')
			}
			writeServerStatsRow(&buf, &sim.Servers[sent+k], ts)
		}
		sent += n
		body := buf.String()

		sem <- struct{}{}
		wg.Add(1)
		go func() {
			defer func() { <-sem; wg.Done() }()
			if err := w.WriteBody(body); err != nil {
				log.Printf("server_stats paced: %v", err)
			}
		}()
	}

	wg.Wait()
	elapsed := time.Since(start)
	log.Printf("server_stats: %d rows paced over %v", sent, elapsed.Round(time.Millisecond))
	if elapsed < period {
		log.Printf("server_stats WARMUP: finished %v before period end — next cycle will align", (period - elapsed).Round(time.Millisecond))
	}
}

func generateServerStatsBurst(w *InfluxWriter, sim *Simulation, batchSize, numWorkers int) {
	total := len(sim.Servers)
	perWorker := total / numWorkers
	if perWorker == 0 {
		perWorker = total
		numWorkers = 1
	}
	var wg sync.WaitGroup
	wg.Add(numWorkers)

	currentTimestamp := time.Now().UnixNano()

	for i := 0; i < numWorkers; i++ {
		workerID := i
		startIdx := workerID * perWorker
		endIdx := startIdx + perWorker
		if workerID == numWorkers-1 {
			endIdx = total
		}
		go func(id, from, to int) {
			defer wg.Done()
			start := time.Now()
			var buf strings.Builder
			count := 0

			for k := from; k < to; k++ {
				if count > 0 {
					buf.WriteByte('\n')
				}
				writeServerStatsRow(&buf, &sim.Servers[k], currentTimestamp)
				count++

				if count >= batchSize {
					if err := w.WriteBody(buf.String()); err != nil {
						log.Printf("server_stats worker %d: %v", id, err)
						return
					}
					buf.Reset()
					count = 0
				}
			}

			if count > 0 {
				if err := w.WriteBody(buf.String()); err != nil {
					log.Printf("server_stats worker %d: %v", id, err)
					return
				}
			}

			log.Printf("server_stats worker %d: %d rows in %.2fs", id, to-from, time.Since(start).Seconds())
		}(workerID, startIdx, endIdx)
	}
	wg.Wait()
	log.Printf("server_stats: %d total rows burst", total)
}
