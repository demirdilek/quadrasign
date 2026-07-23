package main

import (
	"context"
	"encoding/csv"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	_ "net/http/pprof" // Trigger pprof initialization automatically
	"os"
	"os/signal"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promhttp"
)

// Define Prometheus metrics for the 4 Golden Signals
var (
	latencyHistogram = prometheus.NewHistogramVec(
		prometheus.HistogramOpts{
			Name:    "api_prober_latency_seconds",
			Help:    "The time taken to probe the target in seconds (Latency).",
			Buckets: prometheus.DefBuckets,
		},
		[]string{"target"},
	)

	trafficCounter = prometheus.NewCounterVec(
		prometheus.CounterOpts{
			Name: "api_prober_traffic_total",
			Help: "Total number of probes sent to the target (Traffic).",
		},
		[]string{"target"},
	)

	errorCounter = prometheus.NewCounterVec(
		prometheus.CounterOpts{
			Name: "api_prober_errors_total",
			Help: "Total number of failed probes (Errors).",
		},
		[]string{"target", "status_code"},
	)

	saturationGauge = prometheus.NewGaugeVec(
		prometheus.GaugeOpts{
			Name: "api_prober_saturation_active_workers",
			Help: "Number of active concurrent probing workers (Saturation).",
		},
		[]string{"target"},
	)
)

func init() {
	prometheus.MustRegister(latencyHistogram)
	prometheus.MustRegister(trafficCounter)
	prometheus.MustRegister(errorCounter)
	prometheus.MustRegister(saturationGauge)
}

// Job represents a single target probe task
type Job struct {
	Target string
}

func readTargets(filepath string) ([]string, error) {
	file, err := os.Open(filepath)
	if err != nil {
		return nil, err
	}
	defer file.Close()

	var targets []string
	reader := csv.NewReader(file)

	for {
		record, err := reader.Read()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			slog.Warn("Failed to parse CSV record, skipping row", "error", err)
			continue
		}
		if len(record) > 0 {
			target := strings.TrimSpace(record[0])
			if target != "" && !strings.HasPrefix(target, "#") {
				targets = append(targets, target)
			}
		}
	}
	return targets, nil
}

func probeTarget(ctx context.Context, target string, client *http.Client) {
	saturationGauge.WithLabelValues(target).Inc()
	defer saturationGauge.WithLabelValues(target).Dec()

	trafficCounter.WithLabelValues(target).Inc()

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, target, nil)
	if err != nil {
		slog.Error("Failed to create HTTP request", "target", target, "error", err)
		errorCounter.WithLabelValues(target, "request_creation_error").Inc()
		return
	}

	startTime := time.Now()
	resp, err := client.Do(req)
	duration := time.Since(startTime).Seconds()

	if err != nil {
		slog.Warn("Target is unreachable or request timed out", "target", target, "error", err)
		errorCounter.WithLabelValues(target, "network_error").Inc()
		return
	}
	defer resp.Body.Close()

	latencyHistogram.WithLabelValues(target).Observe(duration)

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		statusStr := fmt.Sprintf("%d", resp.StatusCode)
		errorCounter.WithLabelValues(target, statusStr).Inc()
		slog.Warn("Target returned non-2xx status code", "target", target, "status_code", resp.StatusCode)
	} else {
		slog.Debug("Target probed successfully", "target", target, "duration_seconds", duration)
	}
}

func workerPool(ctx context.Context, jobs <-chan Job, client *http.Client, wg *sync.WaitGroup) {
	defer wg.Done()
	for {
		select {
		case <-ctx.Done():
			return
		case job, ok := <-jobs:
			if !ok {
				return
			}
			probeTarget(ctx, job.Target, client)
		}
	}
}

func targetScheduler(ctx context.Context, target string, jobs chan<- Job, interval time.Duration, wg *sync.WaitGroup) {
	defer wg.Done()
	ticker := time.NewTicker(interval)
	defer ticker.Stop()

	slog.Debug("Starting target scheduler", "target", target)

	select {
	case jobs <- Job{Target: target}:
	case <-ctx.Done():
		return
	}

	for {
		select {
		case <-ctx.Done():
			slog.Debug("Stopping target scheduler", "target", target)
			return
		case <-ticker.C:
			select {
			case jobs <- Job{Target: target}:
			case <-ctx.Done():
				return
			}
		}
	}
}

func watchTargets(ctx context.Context, filepath string, jobs chan<- Job, client *http.Client, interval time.Duration, activeSchedulers map[string]context.CancelFunc, mu *sync.Mutex, wg *sync.WaitGroup) {
	var lastModTime time.Time
	ticker := time.NewTicker(5 * time.Second)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			stat, err := os.Stat(filepath)
			if err != nil {
				slog.Error("Failed to stat targets file", "file", filepath, "error", err)
				continue
			}

			if stat.ModTime().After(lastModTime) {
				lastModTime = stat.ModTime()
				slog.Info("Targets file modification detected, synchronization triggered", "file", filepath)

				newTargets, err := readTargets(filepath)
				if err != nil {
					slog.Error("Failed to read targets during live reload", "file", filepath, "error", err)
					continue
				}

				mu.Lock()
				for target, cancelFunc := range activeSchedulers {
					if !contains(newTargets, target) {
						slog.Info("Target missing from new config, cancelling scheduler", "target", target)
						cancelFunc()
						delete(activeSchedulers, target)
					}
				}

				for _, target := range newTargets {
					if _, exists := activeSchedulers[target]; !exists {
						slog.Info("New target found, allocating scheduler", "target", target)
						
						schedCtx, schedCancel := context.WithCancel(ctx)
						activeSchedulers[target] = schedCancel

						wg.Add(1)
						go targetScheduler(schedCtx, target, jobs, interval, wg)
					}
				}
				mu.Unlock()
			}
		}
	}
}

func contains(slice []string, key string) bool {
	for _, item := range slice {
		if item == key {
			return true
		}
	}
	return false
}

// getEnvAsInt reads an environment variable and falls back to a default if missing or invalid
func getEnvAsInt(name string, defaultVal int) int {
	valStr := os.Getenv(name)
	if valStr == "" {
		return defaultVal
	}
	val, err := strconv.Atoi(valStr)
	if err != nil {
		slog.Warn("Invalid integer for environment variable, using default", "env", name, "default", defaultVal, "error", err)
		return defaultVal
	}
	return val
}

func main() {
	logger := slog.New(slog.NewJSONHandler(os.Stdout, nil))
	slog.SetDefault(logger)

	targetsFile := "targets.csv"

	// Parse environment variables for scale configuration
	numWorkers := getEnvAsInt("WORKERS", 50)
	jobQueueSize := getEnvAsInt("QUEUE_SIZE", 10000)
	maxIdleConns := getEnvAsInt("MAX_IDLE_CONNS", 1000)
	maxIdleConnsPerHost := getEnvAsInt("MAX_IDLE_CONNS_PER_HOST", 100)
	probeIntervalSeconds := getEnvAsInt("PROBE_INTERVAL_SECONDS", 2)

	probeInterval := time.Duration(probeIntervalSeconds) * time.Second

	slog.Info("Starting api-prober with configuration",
		"workers", numWorkers,
		"queue_size", jobQueueSize,
		"max_idle_conns", maxIdleConns,
		"max_idle_conns_per_host", maxIdleConnsPerHost,
		"probe_interval", probeInterval,
	)

	client := &http.Client{
		Timeout: 5 * time.Second,
		Transport: &http.Transport{
			MaxIdleConns:        maxIdleConns,
			MaxIdleConnsPerHost: maxIdleConnsPerHost,
			IdleConnTimeout:     90 * time.Second,
		},
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	var wg sync.WaitGroup
	var mu sync.Mutex
	activeSchedulers := make(map[string]context.CancelFunc)

	jobs := make(chan Job, jobQueueSize)

	for i := 0; i < numWorkers; i++ {
		wg.Add(1)
		go workerPool(ctx, jobs, client, &wg)
	}

	go watchTargets(ctx, targetsFile, jobs, client, probeInterval, activeSchedulers, &mu, &wg)

	http.Handle("/metrics", promhttp.Handler())
	
	srv := &http.Server{
		Addr:    ":8080",
		Handler: nil,
	}

	shutdownChan := make(chan os.Signal, 1)
	signal.Notify(shutdownChan, os.Interrupt, syscall.SIGTERM)

	go func() {
		<-shutdownChan
		slog.Info("Received shutdown signal, initiating graceful termination...")
		
		cancel()

		shutdownCtx, shutdownCancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer shutdownCancel()

		if err := srv.Shutdown(shutdownCtx); err != nil {
			slog.Error("HTTP server forced to shutdown", "error", err)
		}
	}()

	slog.Info("Metric server starting on :8080/metrics")
	if err := srv.ListenAndServe(); !errors.Is(err, http.ErrServerClosed) {
		slog.Error("HTTP server failed to run", "error", err)
		os.Exit(1)
	}

	wg.Wait()
	slog.Info("api-prober stack components stopped cleanly. Goodbye.")
}