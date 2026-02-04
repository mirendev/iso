package iso

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"gopkg.in/yaml.v3"
)

// Config defines configuration for the ISO environment
type Config struct {
	Privileged  bool              `yaml:"privileged"`
	WorkDir     string            `yaml:"workdir"`
	Volumes     []string          `yaml:"volumes"`
	Cache       []string          `yaml:"cache"`
	Binds       []string          `yaml:"binds"`
	Environment map[string]string `yaml:"environment"`
	ExtraHosts  []string          `yaml:"extra_hosts"`
}

// ServiceConfig defines configuration for a service container
type ServiceConfig struct {
	Image       string            `yaml:"image"`
	Environment map[string]string `yaml:"environment"`
	Command     []string          `yaml:"command,omitempty"`
	Port        int               `yaml:"port,omitempty"`
	ExtraHosts  []string          `yaml:"extra_hosts"`
}

// ServicesFile represents the structure of services.yml
type ServicesFile struct {
	Services map[string]ServiceConfig `yaml:"services"`
}

// PeerConfig defines configuration for a single peer container
type PeerConfig struct {
	Hostname    string            `yaml:"hostname"`
	Environment map[string]string `yaml:"environment,omitempty"`
	Ports       []string          `yaml:"ports,omitempty"`
}

// PeersFile represents the structure of peers.yml
type PeersFile struct {
	Network string                `yaml:"network"`
	Peers   map[string]PeerConfig `yaml:"peers"`
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

// loadPeersFile loads and parses the .iso/peers.yml file
// Returns nil if the file doesn't exist (peers are optional)
func loadPeersFile(isoDir string) (*PeersFile, error) {
	peersPath := filepath.Join(isoDir, "peers.yml")

	// Check if file exists
	if _, err := os.Stat(peersPath); os.IsNotExist(err) {
		// No peers file is OK - return nil
		return nil, nil
	}

	// Read the file
	data, err := os.ReadFile(peersPath)
	if err != nil {
		return nil, fmt.Errorf("failed to read peers file: %w", err)
	}

	// Parse YAML
	var peersFile PeersFile
	if err := yaml.Unmarshal(data, &peersFile); err != nil {
		return nil, fmt.Errorf("failed to parse peers file: %w", err)
	}

	// Validate peers
	if len(peersFile.Peers) == 0 {
		return nil, fmt.Errorf("peers.yml must define at least one peer")
	}

	for name, config := range peersFile.Peers {
		if config.Hostname == "" {
			return nil, fmt.Errorf("peer %q is missing required 'hostname' field", name)
		}
	}

	return &peersFile, nil
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

// detectGitWorktree checks if the project is in a git worktree and returns project names
// Returns baseProjectName (for shared caches) and worktreeProjectName (for isolated resources)
func detectGitWorktree(projectRoot string) (baseProjectName, worktreeProjectName string) {
	// Default: use the directory name as both base and worktree project name
	worktreeProjectName = filepath.Base(projectRoot)
	baseProjectName = worktreeProjectName

	// Try to detect if we're in a git worktree
	// A worktree has a different git-dir than git-common-dir
	gitCommonCmd := exec.Command("git", "-C", projectRoot, "rev-parse", "--git-common-dir")
	gitCommonOut, err := gitCommonCmd.Output()
	if err != nil {
		// Not a git repo or git not available - use default
		return baseProjectName, worktreeProjectName
	}

	gitDirCmd := exec.Command("git", "-C", projectRoot, "rev-parse", "--git-dir")
	gitDirOut, err := gitDirCmd.Output()
	if err != nil {
		// Can't determine git-dir - use default
		return baseProjectName, worktreeProjectName
	}

	gitCommonDir := strings.TrimSpace(string(gitCommonOut))
	gitDir := strings.TrimSpace(string(gitDirOut))

	// If git-common-dir is different from git-dir, we're in a worktree
	if gitCommonDir != gitDir {
		// Resolve the common dir to absolute path
		var commonPath string
		if filepath.IsAbs(gitCommonDir) {
			commonPath = gitCommonDir
		} else {
			// Relative to projectRoot
			commonPath = filepath.Join(projectRoot, gitCommonDir)
		}

		// Clean the path to resolve any .. or .
		commonPath = filepath.Clean(commonPath)

		// The base repo directory is the parent of the .git directory
		// e.g., /home/user/myproject/.git -> /home/user/myproject
		if strings.HasSuffix(commonPath, "/.git") || strings.HasSuffix(commonPath, "\\.git") {
			baseRepoDir := filepath.Dir(commonPath)
			baseProjectName = filepath.Base(baseRepoDir)
		}
	}

	return baseProjectName, worktreeProjectName
}
