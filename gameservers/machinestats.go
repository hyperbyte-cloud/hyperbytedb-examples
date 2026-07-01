package main

import (
	"fmt"
	"log"
	"math/rand"
	"strings"
	"sync"
	"time"
)

func writeMachineStatsRow(buf *strings.Builder, machine *Machine, ts int64) {
	fmt.Fprintf(buf,
		"machine_stats,account_service_id=%d,fleet=%s,fleet_id=%s,location_id=%d,machine_id=%d,provider=%s,region=%s,region_id=%s am=%di,avm=%di,cs=%di,free=%di,idle=%f,servers=%di,swapin=%di,swapout=%di,swapused=%di,sys=%f,user=%f %d",
		machine.AccountServiceID,
		escapeTag(machine.Fleet),
		escapeTag(machine.FleetID),
		machine.LocationID,
		machine.MachineID,
		escapeTag(machine.Provider),
		escapeTag(machine.Region),
		escapeTag(machine.RegionID),
		machine.MemActiveMB,
		machine.MemAvailableMB,
		machine.MemCachedMB,
		machine.MemFreeMB,
		machine.Idle,
		machine.ServerCount,
		machine.SwapIn,
		machine.SwapOut,
		machine.SwapUsed,
		machine.CPUSys,
		machine.CPUUser,
		ts,
	)
}

func generateMachineStats(w *InfluxWriter, sim *Simulation, batchSize, numWorkers int, period time.Duration, jitter float64, concurrency int) {
	total := len(sim.Machines)
	if total <= 0 {
		return
	}
	if period > 0 {
		generateMachineStatsPaced(w, sim, period, jitter, concurrency)
	} else {
		generateMachineStatsBurst(w, sim, batchSize, numWorkers)
	}
}

func generateMachineStatsPaced(w *InfluxWriter, sim *Simulation, period time.Duration, jitter float64, concurrency int) {
	total := len(sim.Machines)
	rng := rand.New(rand.NewSource(time.Now().UnixNano() + 1))

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
			writeMachineStatsRow(&buf, &sim.Machines[sent+k], ts)
		}
		sent += n
		body := buf.String()

		sem <- struct{}{}
		wg.Add(1)
		go func() {
			defer func() { <-sem; wg.Done() }()
			if err := w.WriteBody(body); err != nil {
				log.Printf("machine_stats paced: %v", err)
			}
		}()
	}

	wg.Wait()
	log.Printf("machine_stats: %d rows paced over %v", sent, time.Since(start).Round(time.Millisecond))
}

func generateMachineStatsBurst(w *InfluxWriter, sim *Simulation, batchSize, numWorkers int) {
	total := len(sim.Machines)
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
				writeMachineStatsRow(&buf, &sim.Machines[k], currentTimestamp)
				count++

				if count >= batchSize {
					if err := w.WriteBody(buf.String()); err != nil {
						log.Printf("machine_stats worker %d: %v", id, err)
						return
					}
					buf.Reset()
					count = 0
				}
			}

			if count > 0 {
				if err := w.WriteBody(buf.String()); err != nil {
					log.Printf("machine_stats worker %d: %v", id, err)
					return
				}
			}

			log.Printf("machine_stats worker %d: %d rows in %.2fs", id, to-from, time.Since(start).Seconds())
		}(workerID, startIdx, endIdx)
	}
	wg.Wait()
	log.Printf("machine_stats: %d total rows burst", total)
}
