package util

import (
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"strings"
)

// LoadFile loads the contents from the specified path
// and returns the key material as a byte slice.
// If the path is empty, it returns an empty byte slice and no error.
// If the file cannot be read, it returns an error.
func LoadFile(path string) ([]byte, error) {
	if path == "" {
		return nil, errors.New("file path is empty")
	}
	// Check if the file exists
	p, err := expandPath(path)
	if err != nil {
		return nil, fmt.Errorf("failed to expand path: %w", err)
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
func LoadFileAllowMissing(path string) ([]byte, error) {
	if path == "" {
		return nil, nil
	}
	// Check if the file exists
	p, err := expandPath(path)
	if err != nil {
		return nil, fmt.Errorf("failed to expand path: %w", err)
	}
	_, err = os.Stat(p)
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
// It should understand different file URL schemes, but for now, just knows https.
// updateChannel is a channel that will receive progress updates if bytes written.
// The caller must be prepared to close the channel, and must ensure it is non-blocking.
// It writes updates with every buffer, which is 32KB.
func DownloadFile(filepath, url string, updateChannel chan<- int64, progressInterval int64) error {
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

	var totalWritten, lastWritten int64

	buf := make([]byte, 32*1024) // 32KB buffer
	for {
		n, err := resp.Body.Read(buf)
		if n > 0 {
			written, writeErr := f.Write(buf[:n])
			if writeErr != nil {
				return fmt.Errorf("failed to write file: %w", writeErr)
			}
			totalWritten += int64(written)
			if updateChannel != nil && (totalWritten-lastWritten) >= progressInterval {
				updateChannel <- totalWritten
				lastWritten = totalWritten
			}
		}
		if err != nil {
			if err == http.ErrBodyReadAfterClose || errors.Is(err, io.EOF) {
				break
			}
			return fmt.Errorf("failed to read response body: %w", err)
		}
	}

	return nil
}

// SaveFile saves a file to the specified path. Returns error if file already exists, unless
// overwrite is set to true.
func SaveFile(path string, data []byte, overwrite bool) error {
	// Check if the file exists
	p, err := expandPath(path)
	if err != nil {
		return fmt.Errorf("failed to expand path: %w", err)
	}
	if _, err := os.Stat(p); err == nil {
		// File exists
		if !overwrite {
			return fmt.Errorf("file already exists")
		}
		// chose to overwrite, so wipe it out
		if err := os.Remove(p); err != nil {
			return fmt.Errorf("failed to remove existing file: %w", err)
		}
	} else if !os.IsNotExist(err) {
		// Some other error while checking
		return err
	}

	// Write the file if it doesn't exist
	return os.WriteFile(p, data, 0644)
}

// expandPath ensure that the path is expanded
func expandPath(path string) (string, error) {
	if strings.HasPrefix(path, "~/") {
		home, err := os.UserHomeDir()
		if err != nil {
			return "", err
		}
		return strings.Replace(path, "~", home, 1), nil
	}
	return path, nil
}
