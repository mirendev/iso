//go:build !linux_build
// +build !linux_build

package iso

import (
	_ "embed"
	"fmt"
	"os"
	"path/filepath"
)

//go:embed iso-linux-amd64
var linuxBinaryAmd64 []byte

//go:embed iso-linux-arm64
var linuxBinaryArm64 []byte

// extractLinuxBinary extracts the embedded Linux iso binary to the .iso directory
// and returns the path to that file. Reuses existing file if present and valid.
func extractLinuxBinary(isoDir, arch string) (string, error) {
	// Determine which architecture binary to use
	var linuxBinary []byte

	switch arch {
	case "amd64":
		linuxBinary = linuxBinaryAmd64
	case "arm64":
		linuxBinary = linuxBinaryArm64
	default:
		return "", fmt.Errorf("unsupported architecture: %s", arch)
	}

	if len(linuxBinary) == 0 {
		return "", fmt.Errorf("embedded Linux binary for %s is empty - was iso-linux-%s built first?", arch, arch)
	}

	// Use .iso directory for extracted binary
	extractPath := filepath.Join(isoDir, fmt.Sprintf("iso-linux-%s", arch))

	// Check if file already exists and has correct size
	if stat, err := os.Stat(extractPath); err == nil {
		if stat.Size() == int64(len(linuxBinary)) {
			// File exists with correct size, reuse it
			return extractPath, nil
		}
		// Size mismatch, remove old file
		os.Remove(extractPath)
	}

	// Write the embedded binary to the .iso directory
	if err := os.WriteFile(extractPath, linuxBinary, 0755); err != nil {
		return "", fmt.Errorf("failed to write embedded binary: %w", err)
	}

	return extractPath, nil
}
