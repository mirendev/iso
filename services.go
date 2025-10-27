package iso

import (
	"fmt"
	"os"
	"path/filepath"

	"gopkg.in/yaml.v3"
)

// Config defines configuration for the ISO environment
type Config struct {
	Privileged  bool              `yaml:"privileged"`
	WorkDir     string            `yaml:"workdir"`
	Volumes     []string          `yaml:"volumes"`
	Cache       []string          `yaml:"cache"`
	Environment map[string]string `yaml:"environment"`
}

// ServiceConfig defines configuration for a service container
type ServiceConfig struct {
	Image       string            `yaml:"image"`
	Environment map[string]string `yaml:"environment"`
	Command     []string          `yaml:"command,omitempty"`
	Port        int               `yaml:"port,omitempty"`
}

// ServicesFile represents the structure of services.yml
type ServicesFile struct {
	Services map[string]ServiceConfig `yaml:"services"`
}

// loadConfigFile loads and parses the .iso/config.yml file
// Returns default config if the file doesn't exist (config is optional)
func loadConfigFile(isoDir string) (*Config, error) {
	configPath := filepath.Join(isoDir, "config.yml")

	// Default configuration
	config := &Config{
		Privileged: false,
		WorkDir:    "/workspace",
	}

	// Check if file exists
	if _, err := os.Stat(configPath); os.IsNotExist(err) {
		// No config file is OK - return defaults
		return config, nil
	}

	// Read the file
	data, err := os.ReadFile(configPath)
	if err != nil {
		return nil, fmt.Errorf("failed to read config file: %w", err)
	}

	// Parse YAML
	if err := yaml.Unmarshal(data, config); err != nil {
		return nil, fmt.Errorf("failed to parse config file: %w", err)
	}

	// Ensure workdir has a default if not specified
	if config.WorkDir == "" {
		config.WorkDir = "/workspace"
	}

	return config, nil
}

// loadServicesFile loads and parses the .iso/services.yml file
// Returns nil if the file doesn't exist (services are optional)
func loadServicesFile(isoDir string) (map[string]ServiceConfig, error) {
	servicesPath := filepath.Join(isoDir, "services.yml")

	// Check if file exists
	if _, err := os.Stat(servicesPath); os.IsNotExist(err) {
		// No services file is OK - return empty map
		return make(map[string]ServiceConfig), nil
	}

	// Read the file
	data, err := os.ReadFile(servicesPath)
	if err != nil {
		return nil, fmt.Errorf("failed to read services file: %w", err)
	}

	// Parse YAML
	var servicesFile ServicesFile
	if err := yaml.Unmarshal(data, &servicesFile); err != nil {
		return nil, fmt.Errorf("failed to parse services file: %w", err)
	}

	// Validate services
	for name, config := range servicesFile.Services {
		if config.Image == "" {
			return nil, fmt.Errorf("service %q is missing required 'image' field", name)
		}
	}

	return servicesFile.Services, nil
}

// findIsoDir searches upward from the current directory to find .iso directory
// Returns the .iso directory path and the project root directory
func findIsoDir() (isoPath string, projectRoot string, found bool) {
	cwd, err := os.Getwd()
	if err != nil {
		return "", "", false
	}

	dir := cwd
	for {
		isoDir := filepath.Join(dir, ".iso")

		// Check if .iso directory exists
		if stat, err := os.Stat(isoDir); err == nil && stat.IsDir() {
			return isoDir, dir, true
		}

		// Move up one directory
		parent := filepath.Dir(dir)
		if parent == dir {
			// Reached root directory
			break
		}
		dir = parent
	}

	return "", "", false
}
