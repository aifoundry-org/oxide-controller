package util

import (
	"fmt"
	"net/http"
	"os"
)

// LoadFile loads the contents from the specified path
// and returns the key material as a byte slice.
// If the path is empty, it returns an empty byte slice and no error.
// If the file cannot be read, it returns an error.
func LoadFile(p string) ([]byte, error) {
	if p == "" {
		return nil, nil
	}
	data, err := os.ReadFile(p)
	if err != nil {
		return nil, fmt.Errorf("failed to read file: %w", err)
	}
	return data, nil
}

// LoadFileAllowMissing loads the contents from the specified path
// and returns the key material as a byte slice.
// If the path is empty, it returns an empty byte slice and no error.
// If the file does not exist, it returns an empty byte slice and no error.
// If the file does exist but cannot be read, it returns an error.
func LoadFileAllowMissing(p string) ([]byte, error) {
	if p == "" {
		return nil, nil
	}
	_, err := os.Stat(p)
	if err != nil && !os.IsNotExist(err) {
		return nil, fmt.Errorf("failed to stat file: %w", err)
	}
	if err != nil && os.IsNotExist(err) {
		return nil, nil
	}
	data, err := os.ReadFile(p)
	if err != nil {
		return nil, fmt.Errorf("failed to read file: %w", err)
	}
	return data, nil
}

// DownloadFile downloads a file from a URL and saves it to the local filesystem
// It should understand different file URL schemes, but for now, just knows https
func DownloadFile(filepath, url string) error {
	resp, err := http.Get(url)
	if err != nil {
		return fmt.Errorf("failed to download file: %w", err)
	}
	defer resp.Body.Close()

	f, err := os.Create(filepath)
	if err != nil {
		return fmt.Errorf("failed to create file: %w", err)
	}
	defer f.Close()

	if _, err := f.ReadFrom(resp.Body); err != nil {
		return fmt.Errorf("failed to write file: %w", err)
	}
	return nil
}

// SaveFileIfNotExists saves a file to the specified path if it does not already exist
func SaveFileIfNotExists(path string, data []byte) error {
	// Check if the file exists
	if _, err := os.Stat(path); err == nil {
		// File exists
		return fmt.Errorf("file already exists")
	} else if !os.IsNotExist(err) {
		// Some other error while checking
		return err
	}

	// Write the file if it doesn't exist
	return os.WriteFile(path, data, 0644)
}
