package main

import (
	"context"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"
)

// English comments as preferred
func TestReadTargets(t *testing.T) {
	// Create a temporary directory for our test file that cleans up automatically
	tmpDir := t.TempDir()
	tmpFile := filepath.Join(tmpDir, "test_targets.csv")

	// Define mock CSV content including comments and empty lines to test robustness
	csvContent := `
http://httpbin/status/200
# This is a comment line and should be ignored

http://httpbin/delay/1
  http://httpbin/status/500  
`

	// Write the mock content to the temporary file
	err := os.WriteFile(tmpFile, []byte(csvContent), 0644)
	if err != nil {
		t.Fatalf("Failed to create temporary test file: %v", err)
	}

	// Execute the actual function from main.go
	targets, err := readTargets(tmpFile)
	if err != nil {
		t.Fatalf("readTargets returned an unexpected error: %v", err)
	}

	// Define what we strictly expect to see parsed
	expected := []string{
		"http://httpbin/status/200",
		"http://httpbin/delay/1",
		"http://httpbin/status/500",
	}

	// Validate the length of the parsed slice
	if len(targets) != len(expected) {
		t.Fatalf("Expected %d targets, but got %d", len(expected), len(targets))
	}

	// Validate the exact content and trimming mechanisms
	for i, url := range targets {
		if url != expected[i] {
			t.Errorf("At index %d: expected %q, but got %q", i, expected[i], url)
		}
	}
}

func TestReadTargets_FileNotFound(t *testing.T) {
	// Execute the function with a file path that definitely does not exist
	_, err := readTargets("non_existent_file.csv")
	
	// We strictly expect an error here
	if err == nil {
		t.Fatal("Expected an error when reading a non-existent file, but got nil")
	}
}

func TestWatchTargets_Integration(t *testing.T) {
	tmpDir := t.TempDir()
	tmpFile := filepath.Join(tmpDir, "targets.csv")

	// Write initial target
	err := os.WriteFile(tmpFile, []byte("http://target1\n"), 0644)
	if err != nil {
		t.Fatalf("Failed to write initial targets: %v", err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	jobs := make(chan Job, 10)
	activeSchedulers := make(map[string]context.CancelFunc)
	var mu sync.Mutex
	var wg sync.WaitGroup

	// Run watchTargets in the background
	go watchTargets(ctx, tmpFile, jobs, nil, 1*time.Second, activeSchedulers, &mu, &wg)

	// Wait for the first file read (5 second hardcoded ticker + buffer)
	time.Sleep(6 * time.Second)

	mu.Lock()
	if len(activeSchedulers) != 1 {
		t.Errorf("Expected 1 active scheduler, got %d", len(activeSchedulers))
	}
	mu.Unlock()

	// Modify the file to remove target1 and add target2
	err = os.WriteFile(tmpFile, []byte("http://target2\n"), 0644)
	if err != nil {
		t.Fatalf("Failed to modify targets: %v", err)
	}

	// Wait for the watcher to pick up the file modification
	time.Sleep(6 * time.Second)

	mu.Lock()
	if len(activeSchedulers) != 1 {
		t.Errorf("Expected exactly 1 active scheduler after modification, got %d", len(activeSchedulers))
	}
	
	if _, exists := activeSchedulers["http://target2"]; !exists {
		t.Errorf("Expected target2 to be scheduled")
	}
	
	if _, exists := activeSchedulers["http://target1"]; exists {
		t.Errorf("Expected target1 scheduler to be cancelled and removed")
	}
	mu.Unlock()
}