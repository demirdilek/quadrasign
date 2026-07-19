package main

import (
	"os"
	"path/filepath"
	"testing"
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