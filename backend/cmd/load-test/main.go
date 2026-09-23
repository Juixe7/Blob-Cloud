package main

import (
	"context"
	"flag"
	"fmt"
	"net/http"
	"sort"
	"sync"
	"sync/atomic"
	"time"
)

type Result struct {
	StatusCode int
	Duration   time.Duration
	Err        error
}

func main() {
	baseURL := flag.String("url", "http://localhost:8090", "Base URL of the Blob-Cloud API server")
	concurrency := flag.Int("c", 30, "Number of concurrent worker routines")
	numRequests := flag.Int("n", 300, "Total number of requests to dispatch")
	endpoint := flag.String("endpoint", "/metrics", "Endpoint path to load test (e.g. /metrics, /api/health)")
	flag.Parse()

	targetURL := *baseURL + *endpoint

	fmt.Println("================================================================================")
	fmt.Println("⚡ Blob-Cloud High-Concurrency Load & Stress Test")
	fmt.Println("================================================================================")
	fmt.Printf("  • Target URL:    %s\n", targetURL)
	fmt.Printf("  • Concurrency:   %d workers\n", *concurrency)
	fmt.Printf("  • Total Requests:%d requests\n", *numRequests)
	fmt.Println("================================================================================")
	fmt.Println("Checking server connectivity...")

	// Initial probe to verify server is reachable
	probeClient := &http.Client{Timeout: 3 * time.Second}
	probeResp, err := probeClient.Get(targetURL)
	if err != nil {
		fmt.Println()
		fmt.Printf("❌ Cannot connect to %s: %v\n", targetURL, err)
		fmt.Println()
		fmt.Println("💡 To run load tests against your local API:")
		fmt.Println("   1. Start the API server in another terminal:")
		fmt.Println("      cd backend && go run cmd/api/main.go")
		fmt.Println("   2. Re-run this command: go run cmd/load-test/main.go")
		return
	}
	_ = probeResp.Body.Close()
	fmt.Printf("✅ Connection verified! HTTP %d OK\n\n", probeResp.StatusCode)

	// Dispatch worker pool
	results := make([]Result, *numRequests)
	var reqIndex int64 = -1
	var successCount, failureCount int64

	client := &http.Client{
		Timeout: 10 * time.Second,
		Transport: &http.Transport{
			MaxIdleConns:        *concurrency * 2,
			MaxIdleConnsPerHost: *concurrency * 2,
			IdleConnTimeout:     30 * time.Second,
		},
	}

	startOverall := time.Now()
	var wg sync.WaitGroup
	ctx := context.Background()

	for w := 0; w < *concurrency; w++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for {
				idx := atomic.AddInt64(&reqIndex, 1)
				if idx >= int64(*numRequests) {
					return
				}

				reqStart := time.Now()
				req, _ := http.NewRequestWithContext(ctx, "GET", targetURL, nil)
				resp, rErr := client.Do(req)
				dur := time.Since(reqStart)

				status := 0
				if rErr == nil {
					status = resp.StatusCode
					_ = resp.Body.Close()
					if status < 400 {
						atomic.AddInt64(&successCount, 1)
					} else {
						atomic.AddInt64(&failureCount, 1)
					}
				} else {
					atomic.AddInt64(&failureCount, 1)
				}

				results[idx] = Result{
					StatusCode: status,
					Duration:   dur,
					Err:        rErr,
				}
			}
		}()
	}

	wg.Wait()
	totalDuration := time.Since(startOverall)

	// Analyze results
	durations := make([]time.Duration, 0, len(results))
	var totalLatency time.Duration
	for _, r := range results {
		if r.Duration > 0 {
			durations = append(durations, r.Duration)
			totalLatency += r.Duration
		}
	}
	sort.Slice(durations, func(i, j int) bool { return durations[i] < durations[j] })

	rps := float64(*numRequests) / totalDuration.Seconds()
	avgLatency := totalLatency / time.Duration(len(durations))
	p50 := percentile(durations, 0.50)
	p90 := percentile(durations, 0.90)
	p95 := percentile(durations, 0.95)
	p99 := percentile(durations, 0.99)
	maxLat := durations[len(durations)-1]

	fmt.Println("================================================================================")
	fmt.Println("📊 Load Test Results")
	fmt.Println("================================================================================")
	fmt.Printf("  • Total Time Elapsed: %v\n", totalDuration.Round(time.Millisecond))
	fmt.Printf("  • Total Dispatched:   %d\n", *numRequests)
	fmt.Printf("  • Successes (2xx):    %d (%.1f%%)\n", successCount, (float64(successCount)/float64(*numRequests))*100)
	fmt.Printf("  • Failures / Errors:  %d (%.1f%%)\n", failureCount, (float64(failureCount)/float64(*numRequests))*100)
	fmt.Printf("  • Throughput (RPS):   %.1f req/sec\n", rps)
	fmt.Println("--------------------------------------------------------------------------------")
	fmt.Println("⏱️  Latency Distribution:")
	fmt.Printf("  • Mean (Avg):  %v\n", avgLatency.Round(time.Microsecond))
	fmt.Printf("  • p50 (Median):%v\n", p50.Round(time.Microsecond))
	fmt.Printf("  • p90:         %v\n", p90.Round(time.Microsecond))
	fmt.Printf("  • p95:         %v\n", p95.Round(time.Microsecond))
	fmt.Printf("  • p99:         %v\n", p99.Round(time.Microsecond))
	fmt.Printf("  • Max:         %v\n", maxLat.Round(time.Microsecond))
	fmt.Println("================================================================================")
}

func percentile(d []time.Duration, p float64) time.Duration {
	if len(d) == 0 {
		return 0
	}
	idx := int(float64(len(d)-1) * p)
	return d[idx]
}
