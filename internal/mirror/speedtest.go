package mirror

import (
	"context"
	"io"
	"net/http"
	"sort"
	"sync"
	"time"
)

const (
	speedTestTimeout    = 5 * time.Second
	speedTestMaxWorkers = 10
)

// SpeedTest measures latency and throughput for the given mirror URLs,
// returning the top N results sorted by throughput descending (errors last).
func (d *Discovery) SpeedTest(ctx context.Context, urls []string, topN int) []SpeedResult {
	results := d.measureLatency(ctx, urls)

	// Sort by latency ascending, errors last.
	sort.Slice(results, func(i, j int) bool {
		if results[i].Error != "" && results[j].Error == "" {
			return false
		}
		if results[i].Error == "" && results[j].Error != "" {
			return true
		}
		return results[i].LatencyMs < results[j].LatencyMs
	})

	// Select top N candidates without errors for throughput measurement.
	var candidates []SpeedResult
	var errored []SpeedResult
	for _, r := range results {
		if r.Error != "" {
			errored = append(errored, r)
			continue
		}
		if len(candidates) < topN {
			candidates = append(candidates, r)
		} else {
			// Beyond topN, treat as not measured further but still include
			errored = append(errored, r)
		}
	}

	// Phase 2: measure throughput for candidates.
	throughputResults := d.measureThroughput(ctx, candidates)

	// Combine throughput results with errored results.
	final := append(throughputResults, errored...)

	// Sort by throughput descending, errors last.
	sort.Slice(final, func(i, j int) bool {
		if final[i].Error != "" && final[j].Error == "" {
			return false
		}
		if final[i].Error == "" && final[j].Error != "" {
			return true
		}
		return final[i].ThroughputKBps > final[j].ThroughputKBps
	})

	return final
}

// measureLatency performs concurrent HTTP HEAD requests to measure latency.
func (d *Discovery) measureLatency(ctx context.Context, urls []string) []SpeedResult {
	results := make([]SpeedResult, len(urls))
	sem := make(chan struct{}, speedTestMaxWorkers)
	var wg sync.WaitGroup

	for i, u := range urls {
		wg.Add(1)
		go func(idx int, url string) {
			defer wg.Done()
			sem <- struct{}{}
			defer func() { <-sem }()

			reqCtx, cancel := context.WithTimeout(ctx, speedTestTimeout)
			defer cancel()

			req, err := http.NewRequestWithContext(reqCtx, http.MethodHead, url, nil)
			if err != nil {
				results[idx] = SpeedResult{URL: url, Error: err.Error()}
				return
			}
			req.Header.Set("User-Agent", "airgap/1.0")

			start := time.Now()
			resp, err := d.client.Do(req)
			elapsed := time.Since(start)

			if err != nil {
				results[idx] = SpeedResult{URL: url, LatencyMs: int(elapsed.Milliseconds()), Error: err.Error()}
				return
			}
			resp.Body.Close()

			results[idx] = SpeedResult{
				URL:       url,
				LatencyMs: int(elapsed.Milliseconds()),
			}
		}(i, u)
	}

	wg.Wait()
	return results
}

// measureThroughput performs concurrent HTTP GET requests to measure download throughput.
func (d *Discovery) measureThroughput(ctx context.Context, candidates []SpeedResult) []SpeedResult {
	results := make([]SpeedResult, len(candidates))
	sem := make(chan struct{}, speedTestMaxWorkers)
	var wg sync.WaitGroup

	for i, c := range candidates {
		wg.Add(1)
		go func(idx int, sr SpeedResult) {
			defer wg.Done()
			sem <- struct{}{}
			defer func() { <-sem }()

			reqCtx, cancel := context.WithTimeout(ctx, speedTestTimeout)
			defer cancel()

			req, err := http.NewRequestWithContext(reqCtx, http.MethodGet, sr.URL, nil)
			if err != nil {
				sr.Error = err.Error()
				results[idx] = sr
				return
			}
			req.Header.Set("User-Agent", "airgap/1.0")

			start := time.Now()
			resp, err := d.client.Do(req)
			if err != nil {
				sr.Error = err.Error()
				results[idx] = sr
				return
			}
			defer resp.Body.Close()

			bytes, err := io.Copy(io.Discard, resp.Body)
			elapsed := time.Since(start)

			if err != nil {
				sr.Error = err.Error()
				results[idx] = sr
				return
			}

			if elapsed.Seconds() > 0 {
				sr.ThroughputKBps = float64(bytes) / elapsed.Seconds() / 1024.0
			}
			results[idx] = sr
		}(i, c)
	}

	wg.Wait()
	return results
}
