package main

import (
	"encoding/csv"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"strings"
	"sync"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promhttp"
)

// Define Prometheus metrics for the 4 Golden Signals
var (
	// 1. LATENCY (Histogram to measure response time distribution)
	latencyHistogram = prometheus.NewHistogramVec(
		prometheus.HistogramOpts{
			Name:    "quadrasign_latency_seconds",
			Help:    "The time taken to probe the target in seconds (Latency).",
			Buckets: prometheus.DefBuckets,
		},
		[]string{"target"},
	)

	// 2. TRAFFIC (Counter to measure the rate of incoming requests/probes)
	trafficCounter = prometheus.NewCounterVec(
		prometheus.CounterOpts{
			Name: "quadrasign_traffic_total",
			Help: "Total number of probes sent to the target (Traffic).",
		},
		[]string{"target"},
	)

	// 3. ERRORS (Counter to track failed requests, e.g., non-2xx status codes)
	errorCounter = prometheus.NewCounterVec(
		prometheus.CounterOpts{
			Name: "quadrasign_errors_total",
			Help: "Total number of failed probes (Errors).",
		},
		[]string{"target", "status_code"},
	)

	// 4. SATURATION (Gauge to track current concurrent active workers per target)
	saturationGauge = prometheus.NewGaugeVec(
		prometheus.GaugeOpts{
			Name: "quadrasign_saturation_active_workers",
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

// readTargets parses the targets.csv file and returns a list of URLs
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
		if err == io.EOF {
			break
		}
		if err != nil {
			return nil, err
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
func probeTarget(target string, client *http.Client) {
	// Increment Saturation before starting the work
	saturationGauge.WithLabelValues(target).Inc()
	defer saturationGauge.WithLabelValues(target).Dec()

	// Increment Traffic
	trafficCounter.WithLabelValues(target).Inc()

	startTime := time.Now()
	resp, err := client.Get(target)
	duration := time.Since(startTime).Seconds()

	if err != nil {
		log.Printf("[ERROR] Target %s is unreachable: %v\n", target, err)
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
		log.Printf("[WARN] Target %s returned status %d\n", target, resp.StatusCode)
	} else {
		log.Printf("[SUCCESS] Target %s probed in %.4fs\n", target, duration)
	}
}

// worker manage the lifecyle loop for a single target
func worker(target string, client *http.Client, wg *sync.WaitGroup) {
	defer wg.Done()
	ticker := time.NewTicker(2 * time.Second) // Probe every 2 seconds
	defer ticker.Stop()

	log.Printf("[INIT] Starting prober routine for target: %s\n", target)

	// Execute initial probe immediately
	probeTarget(target, client)

	for range ticker.C {
		probeTarget(target, client)
	}
}

func main() {
	targetsFile := "targets.csv"
	targets, err := readTargets(targetsFile)
	if err != nil {
		log.Fatalf("Failed to read targets file %s: %v", targetsFile, err)
	}

	if len(targets) == 0 {
		log.Fatalf("No valid targets found in %s", targetsFile)
	}

	// Shared HTTP client with a strict timeout policy
	client := &http.Client{
		Timeout: 5 * time.Second,
	}

	var wg sync.WaitGroup
	// Start a concurrent worker routine for each target
	for _, target := range targets {
		wg.Add(1)
		go worker(target, client, &wg)
	}

	// Expose Prometheus metrics on port 8080
	http.Handle("/metrics", promhttp.Handler())
	log.Println("[INFO] Metric server starting on :8080/metrics")

	if err := http.ListenAndServe(":8080", nil); err != nil {
		log.Fatalf("Failed to start HTTP server: %v", err)
	}

	wg.Wait()
}