package main

import (
	"context"
	"encoding/csv"
	"flag"
	"fmt"
	"math/rand"
	"os"
	"os/signal"
	"strings"
	"sync"
	"sync/atomic"
	"syscall"
	"time"

	"github.com/rachitkumar205/acp-kv/benchmark/adaptive"
)

type Config struct {
	Endpoints        string
	PrometheusURL    string
	Mode             string
	CCSThreshold     float64
	Duration         time.Duration
	Concurrency      int
	Workload         string
	TargetThroughput int
	OutputFile       string
}

type BenchmarkStats struct {
	totalOps       atomic.Int64
	successOps     atomic.Int64
	failedOps      atomic.Int64
	totalLatencyNs atomic.Int64
	minLatencyNs   atomic.Int64
	maxLatencyNs   atomic.Int64
	readOps        atomic.Int64
	writeOps       atomic.Int64
}

func main() {
	cfg := Config{}
	flag.StringVar(&cfg.Endpoints, "endpoints", "localhost:8080", "comma-separated list of ACP endpoints")
	flag.StringVar(&cfg.PrometheusURL, "prometheus-url", "http://localhost:9090", "prometheus URL")
	flag.StringVar(&cfg.Mode, "mode", "ccs-watch", "benchmark mode: ccs-watch, continuous, burst")
	flag.Float64Var(&cfg.CCSThreshold, "ccs-threshold", 0.45, "CCS threshold to trigger operations")
	flag.DurationVar(&cfg.Duration, "duration", 180*time.Second, "benchmark duration")
	flag.IntVar(&cfg.Concurrency, "concurrency", 10, "number of concurrent clients")
	flag.StringVar(&cfg.Workload, "workload", "mixed", "workload type: read-heavy, write-heavy, mixed")
	flag.IntVar(&cfg.TargetThroughput, "target-throughput", 1000, "target throughput (ops/sec)")
	flag.StringVar(&cfg.OutputFile, "output", "results.csv", "output CSV file")
	flag.Parse()

	if err := run(cfg); err != nil {
		fmt.Fprintf(os.Stderr, "error: %v\n", err)
		os.Exit(1)
	}
}

func run(cfg Config) error {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	// handle signals
	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, os.Interrupt, syscall.SIGTERM)
	go func() {
		<-sigCh
		fmt.Println("\nreceived interrupt, shutting down...")
		cancel()
	}()

	// parse endpoints
	endpoints := strings.Split(cfg.Endpoints, ",")
	for i := range endpoints {
		endpoints[i] = strings.TrimSpace(endpoints[i])
	}

	// create client pool
	fmt.Printf("connecting to %d endpoints...\n", len(endpoints))
	pool, err := adaptive.NewClientPool(endpoints)
	if err != nil {
		return fmt.Errorf("failed to create client pool: %w", err)
	}
	defer pool.Close()

	// verify cluster health
	fmt.Println("checking cluster health...")
	if err := pool.HealthCheck(ctx); err != nil {
		return fmt.Errorf("health check failed: %w", err)
	}
	fmt.Println("cluster healthy")

	// create metrics collector (optional for continuous mode)
	var metricsCollector *adaptive.MetricsCollector
	if cfg.Mode == "ccs-watch" {
		metricsCollector = adaptive.NewMetricsCollector(cfg.PrometheusURL)
		fmt.Println("connecting to prometheus...")
		if err := metricsCollector.Update(ctx); err != nil {
			return fmt.Errorf("failed to query prometheus: %w", err)
		}
		fmt.Printf("initial CCS: raw=%.3f, smoothed=%.3f\n",
			metricsCollector.GetCCSRaw(), metricsCollector.GetCCSSmoothed())
	}

	// determine workload proportions
	readRatio := getReadRatio(cfg.Workload)
	fmt.Printf("workload: %s (%.0f%% read / %.0f%% write)\n",
		cfg.Workload, readRatio*100, (1-readRatio)*100)

	// run benchmark
	stats := &BenchmarkStats{}
	stats.minLatencyNs.Store(1<<63 - 1) // max int64

	fmt.Printf("\nstarting benchmark:\n")
	fmt.Printf("  mode: %s\n", cfg.Mode)
	fmt.Printf("  duration: %s\n", cfg.Duration)
	fmt.Printf("  concurrency: %d\n", cfg.Concurrency)
	fmt.Printf("  target throughput: %d ops/sec\n", cfg.TargetThroughput)
	fmt.Println()

	// start metrics collection (if prometheus available)
	snapshotsCh := make(chan adaptive.MetricsSnapshot, 1000)
	metricsEnabled := metricsCollector != nil
	var metricsWg sync.WaitGroup
	if metricsEnabled {
		metricsWg.Add(1)
		go func() {
			defer metricsWg.Done()
			collectMetrics(ctx, metricsCollector, snapshotsCh)
		}()
	}

	// start benchmark workers
	var wg sync.WaitGroup
	for i := 0; i < cfg.Concurrency; i++ {
		wg.Add(1)
		go func(workerID int) {
			defer wg.Done()
			runWorker(ctx, cfg, pool, stats, readRatio, workerID)
		}(i)
	}

	// start progress reporter
	go reportProgress(ctx, stats, metricsCollector, cfg.Duration)

	// wait for duration or cancellation
	select {
	case <-ctx.Done():
	case <-time.After(cfg.Duration):
		cancel()
	}

	// wait for workers to finish
	wg.Wait()

	// wait for metrics collection to finish, then collect all snapshots
	snapshots := make([]adaptive.MetricsSnapshot, 0)
	if metricsEnabled {
		metricsWg.Wait() // wait for collectMetrics to finish
		close(snapshotsCh)
		for snapshot := range snapshotsCh {
			snapshots = append(snapshots, snapshot)
		}
	}

	// print final statistics
	printFinalStats(stats, cfg.Duration)

	// write results to CSV
	if err := writeResults(cfg.OutputFile, snapshots, stats); err != nil {
		return fmt.Errorf("failed to write results: %w", err)
	}
	fmt.Printf("\nresults written to %s\n", cfg.OutputFile)

	return nil
}

func getReadRatio(workload string) float64 {
	switch workload {
	case "read-heavy":
		return 0.95
	case "write-heavy":
		return 0.05
	case "mixed":
		return 0.5
	default:
		return 0.5
	}
}

func runWorker(ctx context.Context, cfg Config, pool *adaptive.ClientPool, stats *BenchmarkStats, readRatio float64, workerID int) {
	// rate limit per worker
	targetOpsPerSec := cfg.TargetThroughput / cfg.Concurrency
	if targetOpsPerSec == 0 {
		targetOpsPerSec = 1
	}
	throttle := time.NewTicker(time.Second / time.Duration(targetOpsPerSec))
	defer throttle.Stop()

	rng := rand.New(rand.NewSource(time.Now().UnixNano() + int64(workerID)))

	for {
		select {
		case <-ctx.Done():
			return
		case <-throttle.C:
			// determine operation type
			isRead := rng.Float64() < readRatio

			// generate key (zipfian-like distribution)
			key := fmt.Sprintf("key%d", zipfian(rng, 100000))

			// execute operation
			start := time.Now()
			var err error
			if isRead {
				_, err = pool.Get().Get(ctx, key)
				stats.readOps.Add(1)
			} else {
				value := []byte(fmt.Sprintf("value-%d-%d", workerID, time.Now().UnixNano()))
				_, err = pool.Get().Put(ctx, key, value)
				stats.writeOps.Add(1)
			}
			latency := time.Since(start)

			// update statistics
			stats.totalOps.Add(1)
			if err != nil {
				stats.failedOps.Add(1)
			} else {
				stats.successOps.Add(1)
			}

			// update latency stats
			latencyNs := latency.Nanoseconds()
			stats.totalLatencyNs.Add(latencyNs)

			// update min latency
			for {
				current := stats.minLatencyNs.Load()
				if latencyNs >= current || stats.minLatencyNs.CompareAndSwap(current, latencyNs) {
					break
				}
			}

			// update max latency
			for {
				current := stats.maxLatencyNs.Load()
				if latencyNs <= current || stats.maxLatencyNs.CompareAndSwap(current, latencyNs) {
					break
				}
			}
		}
	}
}

// zipfian generates zipfian-distributed numbers (simplified)
func zipfian(rng *rand.Rand, max int) int {
	// simplified zipfian: exponential distribution
	return int(float64(max) * (1 - rng.ExpFloat64()/10.0))
}

func collectMetrics(ctx context.Context, collector *adaptive.MetricsCollector, snapshotsCh chan<- adaptive.MetricsSnapshot) {
	ticker := time.NewTicker(1 * time.Second)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			if err := collector.Update(ctx); err != nil {
				// ignore errors during collection
				continue
			}
			snapshot := collector.Snapshot()
			select {
			case snapshotsCh <- snapshot:
			default:
				// channel full, skip this snapshot
			}
		}
	}
}

func reportProgress(ctx context.Context, stats *BenchmarkStats, collector *adaptive.MetricsCollector, duration time.Duration) {
	ticker := time.NewTicker(5 * time.Second)
	defer ticker.Stop()

	startTime := time.Now()

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			elapsed := time.Since(startTime)
			total := stats.totalOps.Load()
			success := stats.successOps.Load()
			failed := stats.failedOps.Load()
			throughput := float64(total) / elapsed.Seconds()

			avgLatency := float64(0)
			if success > 0 {
				avgLatency = float64(stats.totalLatencyNs.Load()) / float64(success) / 1e6 // convert to ms
			}

			remaining := duration - elapsed

			if collector != nil {
				ccsRaw := collector.GetCCSRaw()
				ccsSmoothed := collector.GetCCSSmoothed()
				r, w := collector.GetCurrentQuorum()
				fmt.Printf("[%s] ops=%d (success=%d, failed=%d) throughput=%.0f ops/s avg_latency=%.2fms ccs=%.3f/%.3f r=%d w=%d remaining=%s\n",
					elapsed.Round(time.Second), total, success, failed, throughput, avgLatency,
					ccsRaw, ccsSmoothed, r, w, remaining.Round(time.Second))
			} else {
				fmt.Printf("[%s] ops=%d (success=%d, failed=%d) throughput=%.0f ops/s avg_latency=%.2fms remaining=%s\n",
					elapsed.Round(time.Second), total, success, failed, throughput, avgLatency, remaining.Round(time.Second))
			}
		}
	}
}

func printFinalStats(stats *BenchmarkStats, duration time.Duration) {
	total := stats.totalOps.Load()
	success := stats.successOps.Load()
	failed := stats.failedOps.Load()
	reads := stats.readOps.Load()
	writes := stats.writeOps.Load()

	fmt.Println("\n=== benchmark complete ===")
	fmt.Printf("duration: %s\n", duration)
	fmt.Printf("total operations: %d\n", total)
	fmt.Printf("successful operations: %d (%.2f%%)\n", success, float64(success)/float64(total)*100)
	fmt.Printf("failed operations: %d (%.2f%%)\n", failed, float64(failed)/float64(total)*100)
	fmt.Printf("read operations: %d (%.2f%%)\n", reads, float64(reads)/float64(total)*100)
	fmt.Printf("write operations: %d (%.2f%%)\n", writes, float64(writes)/float64(total)*100)
	fmt.Printf("throughput: %.2f ops/sec\n", float64(total)/duration.Seconds())

	if success > 0 {
		avgLatency := float64(stats.totalLatencyNs.Load()) / float64(success) / 1e6
		minLatency := float64(stats.minLatencyNs.Load()) / 1e6
		maxLatency := float64(stats.maxLatencyNs.Load()) / 1e6
		fmt.Printf("latency: avg=%.2fms min=%.2fms max=%.2fms\n", avgLatency, minLatency, maxLatency)
	}
}

func writeResults(filename string, snapshots []adaptive.MetricsSnapshot, stats *BenchmarkStats) error {
	f, err := os.Create(filename)
	if err != nil {
		return err
	}
	defer f.Close()

	writer := csv.NewWriter(f)
	defer writer.Flush()

	// write header
	header := []string{
		"timestamp", "ccs_raw", "ccs_smoothed", "current_r", "current_w",
		"quorum_adjustments", "staleness_violations",
	}
	if err := writer.Write(header); err != nil {
		return err
	}

	// write snapshots
	for _, snapshot := range snapshots {
		record := []string{
			snapshot.Timestamp.Format(time.RFC3339),
			fmt.Sprintf("%.6f", snapshot.CCSRaw),
			fmt.Sprintf("%.6f", snapshot.CCSSmoothed),
			fmt.Sprintf("%d", snapshot.CurrentR),
			fmt.Sprintf("%d", snapshot.CurrentW),
			fmt.Sprintf("%d", snapshot.QuorumAdjustments),
			fmt.Sprintf("%d", snapshot.StalenessViolations),
		}
		if err := writer.Write(record); err != nil {
			return err
		}
	}

	return nil
}
