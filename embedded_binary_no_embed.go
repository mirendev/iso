//go:build !embed_binaries && !linux_build
// +build !embed_binaries,!linux_build

package iso

import (
	"fmt"
	"os"
	"runtime"
)

// extractLinuxBinary returns the current executable path on Linux,
// or fails on other platforms when binaries are not embedded.
// This version is used when binaries are not embedded (e.g., go install).
func extractLinuxBinary(isoDir, arch string) (string, error) {
	// Only allow using current executable on Linux
	if runtime.GOOS != "linux" {
		return "", fmt.Errorf("embedded Linux binaries are required on %s. Please use 'quake build' or build with -tags embed_binaries", runtime.GOOS)
	}

	exePath, err := os.Executable()
	if err != nil {
		return "", fmt.Errorf("failed to get executable path: %w", err)
	}
	return exePath, nil
}
