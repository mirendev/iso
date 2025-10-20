//go:build linux_build
// +build linux_build

package iso

import (
	"fmt"
	"os"
)

// extractLinuxBinary is a stub for when building the Linux binary itself
func extractLinuxBinary(isoDir, arch string) (string, error) {
	// When building the Linux binary, just use the current executable
	// This code path should never actually be called in practice
	exePath, err := os.Executable()
	if err != nil {
		return "", fmt.Errorf("failed to get executable path: %w", err)
	}
	return exePath, nil
}
