//go:build !linux_build
// +build !linux_build

package iso

import (
	"compress/gzip"
	"bytes"
	_ "embed"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"runtime"
)

//go:embed build/iso-linux-amd64.gz
var linuxBinaryAmd64Gz []byte

//go:embed build/iso-linux-arm64.gz
var linuxBinaryArm64Gz []byte

// extractLinuxBinary extracts the embedded Linux iso binary to the .iso directory
// and returns the path to that file. Reuses existing file if present and valid.
func extractLinuxBinary(isoDir, arch string) (string, error) {
	// If we're already running on Linux with the same architecture as Docker,
	// just use the current executable instead of extracting
	if runtime.GOOS == "linux" && runtime.GOARCH == arch {
		exePath, err := os.Executable()
		if err != nil {
			return "", fmt.Errorf("failed to get executable path: %w", err)
		}
		return exePath, nil
	}

	// Determine which compressed binary to use
	var compressedBinary []byte

	switch arch {
	case "amd64":
		compressedBinary = linuxBinaryAmd64Gz
	case "arm64":
		compressedBinary = linuxBinaryArm64Gz
	default:
		return "", fmt.Errorf("unsupported architecture: %s", arch)
	}

	if len(compressedBinary) == 0 {
		return "", fmt.Errorf("embedded Linux binary for %s is empty - was iso-linux-%s built first?", arch, arch)
	}

	// Decompress the binary
	gzReader, err := gzip.NewReader(bytes.NewReader(compressedBinary))
	if err != nil {
		return "", fmt.Errorf("failed to create gzip reader: %w", err)
	}
	defer gzReader.Close()

	decompressed, err := io.ReadAll(gzReader)
	if err != nil {
		return "", fmt.Errorf("failed to decompress binary: %w", err)
	}

	// Use .iso directory for extracted binary
	extractPath := filepath.Join(isoDir, fmt.Sprintf("iso-linux-%s", arch))

	// Check if file already exists and has correct size
	if stat, err := os.Stat(extractPath); err == nil {
		if stat.Size() == int64(len(decompressed)) {
			// File exists with correct size, reuse it
			return extractPath, nil
		}
		// Size mismatch, remove old file
		os.Remove(extractPath)
	}

	// Write the decompressed binary to the .iso directory
	if err := os.WriteFile(extractPath, decompressed, 0755); err != nil {
		return "", fmt.Errorf("failed to write embedded binary: %w", err)
	}

	return extractPath, nil
}
