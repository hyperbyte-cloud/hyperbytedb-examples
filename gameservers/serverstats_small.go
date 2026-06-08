package main

import (
	"fmt"
	"log"
	"math/rand"
	"strings"
	"sync"
	"time"
)

func writeServerStatsSmallRow(buf *strings.Builder, rng *rand.Rand, ts int64) {
	// Refactored for easier modification of values
	type ServerStatsSmallParams struct {
		AccountServiceID int32
		FleetID          string
		ServerID         int32
		Players          int32
		Timestamp        int64
	}

	// Generate all randomized or fixed values here for easy modification
	params := ServerStatsSmallParams{
		AccountServiceID: rng.Int31n(20),
		FleetID:          fmt.Sprintf("ab7b39da-3571-4b13-bc06-69833b4f7e1%d", rng.Int31n(9)),
		ServerID:         rng.Int31n(2000),
		Players:          rng.Int31n(25),
		Timestamp:        ts,
	}

	fmt.Fprintf(buf,
		"server_stats_small,account_service_id=%d,fleet_id=%s,server_id=%d players=%di %d",
		params.AccountServiceID,
		params.FleetID,
		params.ServerID,
		params.Players,
		params.Timestamp,
	)

}
func generateServerStatsSmall(w *InfluxWriter, total, batchSize, numWorkers int, period time.Duration, jitter float64, concurrency int) {
	if total <= 0 {
		return
	}
	if period > 0 {
		generateServerStatsSmallPaced(w, total, period, jitter, concurrency)
	} else {
		generateServerStatsSmallBurst(w, total, batchSize, numWorkers)
	}
}

func generateServerStatsSmallPaced(w *InfluxWriter, total int, period time.Duration, jitter float64, concurrency int) {
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

		ts := time.Now().UnixNano()
		var buf strings.Builder
		for k := 0; k < n; k++ {
			if k > 0 {
				buf.WriteByte('\n')
			}
			writeServerStatsSmallRow(&buf, rng, ts)
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
	log.Printf("server_stats: %d rows paced over %v", sent, time.Since(start).Round(time.Millisecond))
}

func generateServerStatsSmallBurst(w *InfluxWriter, total, batchSize, numWorkers int) {
	perWorker := total / numWorkers
	var wg sync.WaitGroup
	wg.Add(numWorkers)

	currentTimestamp := time.Now().UnixNano()

	for i := 0; i < numWorkers; i++ {
		go func(id int) {
			defer wg.Done()
			rng := rand.New(rand.NewSource(time.Now().UnixNano() + int64(id)))
			start := time.Now()
			var buf strings.Builder
			count := 0

			for k := 0; k < perWorker; k++ {
				if count > 0 {
					buf.WriteByte('\n')
				}
				writeServerStatsSmallRow(&buf, rng, currentTimestamp)
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

			log.Printf("server_stats worker %d: %d rows in %.2fs", id, perWorker, time.Since(start).Seconds())
		}(i)
	}
	wg.Wait()
	log.Printf("server_stats_small: %d total rows burst", total)
}
