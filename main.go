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
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promhttp"
)

// Define Prometheus metrics for the 4 Golden Signals
var (
	// 1. LATENCY (Histogram to measure response time distribution)
	latencyHistogram = prometheus.NewHistogramVec(
		prometheus.HistogramOpts{
			Name:    "api_prober_latency_seconds",
			Help:    "The time taken to probe the target in seconds (Latency).",
			Buckets: prometheus.DefBuckets,
		},
		[]string{"target"},
	)

	// 2. TRAFFIC (Counter to measure the rate of incoming requests/probes)
	trafficCounter = prometheus.NewCounterVec(
		prometheus.CounterOpts{
			Name: "api_prober_traffic_total",
			Help: "Total number of probes sent to the target (Traffic).",
		},
		[]string{"target"},
	)

	// 3. ERRORS (Counter to track failed requests, e.g., non-2xx status codes)
	errorCounter = prometheus.NewCounterVec(
		prometheus.CounterOpts{
			Name: "api_prober_errors_total",
			Help: "Total number of failed probes (Errors).",
		},
		[]string{"target", "status_code"},
	)

	// 4. SATURATION (Gauge to track current concurrent active workers per target)
	saturationGauge = prometheus.NewGaugeVec(
		prometheus.GaugeOpts{
			Name: "api_prober_saturation_active_workers",
			Help: "Number of active concurrent probing workers (Saturation).",
		},
		[]string{"target"},
	)
)


func init() {
	// Register all metrics with Prometheus
	prometheus.MustRegister(latencyHistogram)
	prometheus.MustRegister(trafficCounter)
	prometheus.MustRegister(errorCounter)
	prometheus.MustRegister(saturationGauge)
}

// readTargets parses the targets.csv file and returns a list of valid URLs.
// It logs malformed rows instead of crashing entirely.
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

// probeTarget executes an HTTP GET request against a target and records metrics
func probeTarget(ctx context.Context, target string, client *http.Client) {
	// Increment Saturation before starting the work
	saturationGauge.WithLabelValues(target).Inc()
	defer saturationGauge.WithLabelValues(target).Dec()

	// Increment Traffic
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

	// Record Latency
	latencyHistogram.WithLabelValues(target).Observe(duration)

	// Check for Errors (Any status code outside the 2xx range)
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		statusStr := fmt.Sprintf("%d", resp.StatusCode)
		errorCounter.WithLabelValues(target, statusStr).Inc()
		slog.Warn("Target returned non-2xx status code", "target", target, "status_code", resp.StatusCode)
	} else {
		slog.Info("Target probed successfully", "target", target, "duration_seconds", duration)
	}
}

// worker manages the lifecycle loop for a single target and listens to context cancellation
func worker(ctx context.Context, target string, client *http.Client, wg *sync.WaitGroup) {
	defer wg.Done()
	ticker := time.NewTicker(2 * time.Second) // Probe every 2 seconds
	defer ticker.Stop()

	slog.Info("Starting prober routine for target", "target", target)

	// Execute initial probe immediately
	probeTarget(ctx, target, client)

	for {
		select {
		case <-ctx.Done():
			slog.Info("Stopping worker routine due to context cancellation", "target", target)
			return
		case <-ticker.C:
			probeTarget(ctx, target, client)
		}
	}
}

// watchTargets monitors the configuration file for changes and manages live worker pools
func watchTargets(ctx context.Context, filepath string, client *http.Client, activeWorkers map[string]context.CancelFunc, mu *sync.Mutex, wg *sync.WaitGroup) {
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

			// Check if file has been modified since the last loop check
			if stat.ModTime().After(lastModTime) {
				lastModTime = stat.ModTime()
				slog.Info("Targets file modification detected, synchronization triggered", "file", filepath)

				newTargets, err := readTargets(filepath)
				if err != nil {
					slog.Error("Failed to read targets during live reload", "file", filepath, "error", err)
					continue
				}

				mu.Lock()
				// 1. Terminate workers for targets removed from the CSV configuration
				for target, cancelFunc := range activeWorkers {
					if !contains(newTargets, target) {
						slog.Info("Target missing from new config, cancelling worker execution", "target", target)
						cancelFunc()
						delete(activeWorkers, target)
					}
				}

				// 2. Initialize new workers for fresh targets appended to the configuration
				for _, target := range newTargets {
					if _, exists := activeWorkers[target]; !exists {
						slog.Info("New target found, allocating standalone worker architecture", "target", target)
						
						workerCtx, workerCancel := context.WithCancel(ctx)
						activeWorkers[target] = workerCancel

						wg.Add(1)
						go worker(workerCtx, target, client, wg)
					}
				}
				mu.Unlock()
			}
		}
	}
}

// Helper to determine if a specific slice context holds the required key
func contains(slice []string, key string) bool {
	for _, item := range slice {
		if item == key {
			return true
		}
	}
	return false
}

func main() {
	// Initialize structured production JSON logging
	logger := slog.New(slog.NewJSONHandler(os.Stdout, nil))
	slog.SetDefault(logger)

	targetsFile := "targets.csv"

	// Shared HTTP client with a strict timeout policy
	client := &http.Client{
		Timeout: 5 * time.Second,
	}

	// Create a cancelable context tied to OS lifecycle signals
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	// Dynamic memory maps and synchronization primitives to safely control goroutines
	var wg sync.WaitGroup
	var mu sync.Mutex
	activeWorkers := make(map[string]context.CancelFunc)

	// Start the dynamic background file watcher tracking target mutations
	go watchTargets(ctx, targetsFile, client, activeWorkers, &mu, &wg)

	// Setup the HTTP server using the default mux to include pprof + prometheus
    http.Handle("/metrics", promhttp.Handler())
    
    srv := &http.Server{
        Addr:    ":8080",
        Handler: nil, // Setting handler to nil forces it to use http.DefaultServeMux
    }

	// Listen for termination signals in a separate goroutine to trigger graceful shutdown
	shutdownChan := make(chan os.Signal, 1)
	signal.Notify(shutdownChan, os.Interrupt, syscall.SIGTERM)

	go func() {
		<-shutdownChan
		slog.Info("Received shutdown signal, initiating graceful termination...")
		
		// Stop all background worker routine loops and file watcher via context cancellation
		cancel()

		// Allow the HTTP server 5 seconds to finish serving existing metrics requests
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

	// Wait until all dynamically handled workers have completely finished their last loop execution
	wg.Wait()
	slog.Info("Quadrasign stack components stopped cleanly. Goodbye.")
}