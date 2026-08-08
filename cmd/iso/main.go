package main

import (
	"bufio"
	"crypto/rand"
	_ "embed"
	"encoding/base64"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"os"
	"os/exec"
	"os/signal"
	"sort"
	"strings"
	"syscall"
	"time"

	"miren.dev/iso"
	"miren.dev/mflags"
	"miren.dev/trifle"
)

//go:embed agent-help.md
var agentHelpContent string

// Version information, set by ldflags at build time
var (
	version = "dev"
	commit  = "unknown"
)

// ExitError carries an exit code
type ExitError struct {
	Code int
}

func (e *ExitError) Error() string {
	return fmt.Sprintf("exit code %d", e.Code)
}

func main() {

	level := slog.LevelInfo
	if lvlStr, ok := os.LookupEnv("DEBUG"); ok && lvlStr != "0" {
		level = slog.LevelDebug
	}

	// Set up slog with trifle
	slog.SetDefault(slog.New(trifle.New(os.Stderr, &slog.HandlerOptions{
		Level: level,
	})))

	if err := run(); err != nil {
		var exitErr *ExitError
		if errors.As(err, &exitErr) {
			os.Exit(exitErr.Code)
		}
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		os.Exit(1)
	}
}

func run() error {
	dispatcher := mflags.NewDispatcher("iso")

	// Register commands
	registerRunCommand(dispatcher)
	registerBuildCommand(dispatcher)
	registerStartCommand(dispatcher)
	registerStopCommand(dispatcher)
	registerResetCommand(dispatcher)
	registerStatusCommand(dispatcher)
	registerListCommand(dispatcher)
	registerPruneCommand(dispatcher)
	registerCleanupCommand(dispatcher)
	registerInitCommand(dispatcher)
	registerInternalInitCommand(dispatcher)
	registerInEnvCommand(dispatcher)
	registerAgentHelpCommand(dispatcher)
	registerVersionCommand(dispatcher)

	// Peers commands
	registerPeersUpCommand(dispatcher)
	registerPeersDownCommand(dispatcher)
	registerPeersExecCommand(dispatcher)
	registerPeersShellCommand(dispatcher)
	registerPeersStatusCommand(dispatcher)

	// Execute the dispatcher
	return dispatcher.Execute(os.Args[1:])
}

// getSession returns the session name and whether it's ephemeral
// If no session is specified, returns an ephemeral session ID
func getSession(flagValue string) (string, bool) {
	if flagValue != "" {
		return flagValue, false
	}
	if envSession := os.Getenv("ISO_SESSION"); envSession != "" {
		return envSession, false
	}

	buf := make([]byte, 8)
	rand.Read(buf)

	suffix := base64.RawURLEncoding.EncodeToString(buf)

	// No session specified - create ephemeral session
	ephemeralID := "eph-" + suffix
	return ephemeralID, true
}

// getPeersSession resolves the session for peers commands. It deliberately does
// not fall back to an ephemeral session the way getSession does, because peers
// have to stay addressable across separate invocations. Honouring ISO_SESSION is
// what keeps two workspaces of the same repo off each other's peer containers.
func getPeersSession() string {
	if envSession := os.Getenv("ISO_SESSION"); envSession != "" {
		return envSession
	}
	return iso.PeersDefaultSession
}

// isValidEnvVarName checks if a string is a valid environment variable name
// Valid names contain only uppercase letters, lowercase letters, digits, and underscores
// and must start with a letter or underscore
func isValidEnvVarName(name string) bool {
	if len(name) == 0 {
		return false
	}

	// First character must be a letter or underscore
	firstChar := name[0]
	if !((firstChar >= 'a' && firstChar <= 'z') || (firstChar >= 'A' && firstChar <= 'Z') || firstChar == '_') {
		return false
	}

	// Remaining characters must be letters, digits, or underscores
	for i := 1; i < len(name); i++ {
		c := name[i]
		if !((c >= 'a' && c <= 'z') || (c >= 'A' && c <= 'Z') || (c >= '0' && c <= '9') || c == '_') {
			return false
		}
	}

	return true
}

// registerRunCommand registers the 'run' command
func registerRunCommand(dispatcher *mflags.Dispatcher) {
	fs := mflags.NewFlagSet("run")

	session := fs.String("session", 's', "", "Session name (default: ISO_SESSION env var or ephemeral)")

	// Allow unknown flags to pass through to the command
	fs.AllowUnknownFlags(true)

	handler := func(fs *mflags.FlagSet, args []string) error {
		// Combine positional args and unknown flags to form the command
		// Unknown flags come after positional args
		command := append(args, fs.UnknownFlags()...)

		// Parse environment variables from the command
		// Environment variables are KEY=VALUE at the start of the command
		var envVars []string
		var actualCommand []string

		for i, arg := range command {
			// Check if this looks like an environment variable (KEY=VALUE)
			if strings.Contains(arg, "=") && !strings.HasPrefix(arg, "-") {
				// Additional validation: check if it's at the start or after other env vars
				// and the part before = looks like a valid variable name
				parts := strings.SplitN(arg, "=", 2)
				if len(parts) == 2 && isValidEnvVarName(parts[0]) {
					envVars = append(envVars, arg)
					continue
				}
			}
			// Everything else is part of the actual command
			actualCommand = command[i:]
			break
		}

		sessionName, isEphemeral := getSession(*session)
		client, err := iso.New(sessionName)
		if err != nil {
			return err
		}
		defer client.Close()

		// Set up signal handling for graceful cleanup on interrupt
		// This ensures ephemeral resources are cleaned up even if Ctrl+C is pressed
		var cleanupDone bool
		cleanupOnce := func() {
			if cleanupDone {
				return
			}
			cleanupDone = true

			// If ephemeral session, clean up everything
			if isEphemeral {
				if stopErr := client.Stop(); stopErr != nil {
					slog.Warn("failed to clean up ephemeral session", "error", stopErr)
				}
			}
		}

		// Ensure cleanup runs on exit
		defer cleanupOnce()

		// Set up signal handler for interrupts
		sigChan := make(chan os.Signal, 1)
		signal.Notify(sigChan, os.Interrupt, syscall.SIGTERM)
		defer signal.Stop(sigChan)

		// Run command in a goroutine so we can handle signals
		type result struct {
			exitCode int
			err      error
		}
		resultChan := make(chan result, 1)

		go func() {
			exitCode, err := client.Run(actualCommand, envVars, isEphemeral)
			resultChan <- result{exitCode: exitCode, err: err}
		}()

		// Wait for either command completion or signal
		select {
		case res := <-resultChan:
			// Command completed normally
			if res.err != nil {
				return res.err
			}
			if res.exitCode != 0 {
				return &ExitError{Code: res.exitCode}
			}
			return nil

		case sig := <-sigChan:
			// Received interrupt signal - cleanup will happen via defer
			slog.Debug("received signal, cleaning up", "signal", sig)
			return &ExitError{Code: 130} // Standard exit code for SIGINT
		}
	}

	cmd := mflags.NewCommand(fs, handler,
		mflags.WithUsage("Run a command in the isolated environment"),
	)

	dispatcher.Dispatch("run", cmd)
}

// registerBuildCommand registers the 'build' command
func registerBuildCommand(dispatcher *mflags.Dispatcher) {
	fs := mflags.NewFlagSet("build")

	rebuild := fs.Bool("rebuild", 'r', false, "Force rebuild even if image exists")
	session := fs.String("session", 's', "", "Session name (default: ISO_SESSION env var or ephemeral)")

	handler := func(fs *mflags.FlagSet, args []string) error {
		doRebuild := *rebuild

		sessionName, _ := getSession(*session)
		client, err := iso.New(sessionName)
		if err != nil {
			return err
		}
		defer client.Close()

		if doRebuild {
			return client.Rebuild()
		}
		return client.Build()
	}

	cmd := mflags.NewCommand(fs, handler,
		mflags.WithUsage("Build the Docker image"),
	)

	dispatcher.Dispatch("build", cmd)
}

// registerStartCommand registers the 'start' command
func registerStartCommand(dispatcher *mflags.Dispatcher) {
	fs := mflags.NewFlagSet("start")

	session := fs.String("session", 's', "", "Session name (required, or use ISO_SESSION env var)")

	handler := func(fs *mflags.FlagSet, args []string) error {
		// For start command, session is required
		var sessionName string
		if *session != "" {
			sessionName = *session
		} else if envSession := os.Getenv("ISO_SESSION"); envSession != "" {
			sessionName = envSession
		} else {
			return fmt.Errorf("session is required for 'iso start' - use --session flag or set ISO_SESSION env var")
		}

		client, err := iso.New(sessionName)
		if err != nil {
			return err
		}
		defer client.Close()

		return client.Start()
	}

	cmd := mflags.NewCommand(fs, handler,
		mflags.WithUsage("Start a persistent session (requires --session)"),
	)

	dispatcher.Dispatch("start", cmd)
}

// registerStopCommand registers the 'stop' command
func registerStopCommand(dispatcher *mflags.Dispatcher) {
	fs := mflags.NewFlagSet("stop")

	all := fs.Bool("all", 'a', false, "Stop all ISO-managed containers across all projects")
	allSessions := fs.Bool("all-sessions", 'S', false, "Stop all sessions for the current project")
	session := fs.String("session", 's', "", "Session name (required for stopping specific session, or use ISO_SESSION env var)")

	handler := func(fs *mflags.FlagSet, args []string) error {
		if *all {
			return iso.StopAll()
		}

		if *allSessions {
			return iso.StopAllSessions()
		}

		// For stopping a specific session, require session name
		var sessionName string
		if *session != "" {
			sessionName = *session
		} else if envSession := os.Getenv("ISO_SESSION"); envSession != "" {
			sessionName = envSession
		} else {
			return fmt.Errorf("session is required for 'iso stop' - use --session flag, set ISO_SESSION env var, or use --all/--all-sessions")
		}

		client, err := iso.New(sessionName)
		if err != nil {
			return err
		}
		defer client.Close()

		return client.Stop()
	}

	cmd := mflags.NewCommand(fs, handler,
		mflags.WithUsage("Stop and remove a persistent session (requires --session, or use --all/--all-sessions)"),
	)

	dispatcher.Dispatch("stop", cmd)
}

// registerResetCommand registers the 'reset' command
func registerResetCommand(dispatcher *mflags.Dispatcher) {
	fs := mflags.NewFlagSet("reset")

	session := fs.String("session", 's', "", "Session name (required, or use ISO_SESSION env var)")

	handler := func(fs *mflags.FlagSet, args []string) error {
		// For reset command, session is required
		var sessionName string
		if *session != "" {
			sessionName = *session
		} else if envSession := os.Getenv("ISO_SESSION"); envSession != "" {
			sessionName = envSession
		} else {
			return fmt.Errorf("session is required for 'iso reset' - use --session flag or set ISO_SESSION env var")
		}

		client, err := iso.New(sessionName)
		if err != nil {
			return err
		}
		defer client.Close()

		return client.Reset()
	}

	cmd := mflags.NewCommand(fs, handler,
		mflags.WithUsage("Reset a persistent session's container (requires --session)"),
	)

	dispatcher.Dispatch("reset", cmd)
}

// registerStatusCommand registers the 'status' command
func registerStatusCommand(dispatcher *mflags.Dispatcher) {
	fs := mflags.NewFlagSet("status")

	session := fs.String("session", 's', "", "Session name (required, or use ISO_SESSION env var)")

	handler := func(fs *mflags.FlagSet, args []string) error {
		// For status command, session is required
		var sessionName string
		if *session != "" {
			sessionName = *session
		} else if envSession := os.Getenv("ISO_SESSION"); envSession != "" {
			sessionName = envSession
		} else {
			return fmt.Errorf("session is required for 'iso status' - use --session flag or set ISO_SESSION env var")
		}

		client, err := iso.New(sessionName)
		if err != nil {
			return err
		}
		defer client.Close()

		status, err := client.Status()
		if err != nil {
			return err
		}

		imageStatus := "does not exist"
		if status.ImageExists {
			imageStatus = "exists"
		}

		slog.Info("image status", "image", status.ImageName, "status", imageStatus)
		slog.Info("container status", "container", status.ContainerName, "status", status.ContainerState)

		return nil
	}

	cmd := mflags.NewCommand(fs, handler,
		mflags.WithUsage("Show status of a session (requires --session)"),
	)

	dispatcher.Dispatch("status", cmd)
}

// formatBytes renders a byte count in the largest unit that keeps it readable.
func formatBytes(n int64) string {
	const unit = 1024
	if n < unit {
		return fmt.Sprintf("%d B", n)
	}

	div, exp := int64(unit), 0
	for size := n / unit; size >= unit && exp < 4; size /= unit {
		div *= unit
		exp++
	}

	value := float64(n) / float64(div)
	format := "%.1f %cB"
	if value >= 10 {
		format = "%.0f %cB"
	}

	return fmt.Sprintf(format, value, "KMGTP"[exp])
}

// formatAge renders a duration as a coarse human-readable age.
func formatAge(t time.Time) string {
	if t.IsZero() {
		return "unknown"
	}

	d := time.Since(t)
	switch {
	case d < time.Minute:
		return "just now"
	case d < time.Hour:
		return fmt.Sprintf("%dm ago", int(d.Minutes()))
	case d < 24*time.Hour:
		return fmt.Sprintf("%dh ago", int(d.Hours()))
	default:
		return fmt.Sprintf("%dd ago", int(d.Hours()/24))
	}
}

// containerVolumeSize totals the volumes a single container mounts, looking up
// each one in the session's volume list. Returns false if any size is unknown.
func containerVolumeSize(c iso.IsoContainer, volumes []iso.VolumeUsage) (int64, bool) {
	sizes := make(map[string]iso.VolumeUsage, len(volumes))
	for _, v := range volumes {
		sizes[v.Name] = v
	}

	var total int64
	for _, name := range c.Volumes {
		v, ok := sizes[name]
		if !ok || !v.SizeKnown {
			return 0, false
		}
		total += v.Size
	}

	return total, true
}

// registerListCommand registers the 'list' command
func registerListCommand(dispatcher *mflags.Dispatcher) {
	fs := mflags.NewFlagSet("list")

	orphaned := fs.Bool("orphaned", 'o', false, "Show only orphaned sessions (project directory missing)")
	noSizes := fs.Bool("no-sizes", 'n', false, "Skip volume size calculation (much faster with large caches)")

	handler := func(fs *mflags.FlagSet, args []string) error {
		withSizes := !*noSizes

		if *orphaned {
			return listOrphaned(withSizes)
		}

		sessions, err := iso.ListSessions(withSizes)
		if err != nil {
			return err
		}

		if len(sessions) == 0 {
			fmt.Println("No ISO containers found")
			return nil
		}

		// Group sessions by project, preserving the sorted order ListSessions
		// returns.
		var projectOrder []string
		projectSessions := make(map[string][]iso.Session)
		projectDirs := make(map[string]string)
		for _, s := range sessions {
			if _, seen := projectSessions[s.ProjectName]; !seen {
				projectOrder = append(projectOrder, s.ProjectName)
			}
			projectSessions[s.ProjectName] = append(projectSessions[s.ProjectName], s)
			projectDirs[s.ProjectName] = s.ProjectDir
		}

		for _, projectName := range projectOrder {
			fmt.Printf("\n%s (%s):\n", projectName, projectDirs[projectName])
			fmt.Printf("  %-12s %-15s %-20s %10s  %s\n",
				"CONTAINER ID", "NAME", "SESSION", "VOLUMES", "STATUS")

			// Deduplicate volumes across the project's sessions - cache
			// volumes are shared, so they show up under several of them.
			projectVolumes := make(map[string]iso.VolumeUsage)

			for _, s := range projectSessions[projectName] {
				for _, v := range s.Volumes {
					projectVolumes[v.Name] = v
				}

				for _, c := range s.Containers {
					status := c.Status
					if c.IsService {
						status += " (service: " + c.ServiceName + ")"
					}

					volumeCol := "-"
					if len(c.Volumes) > 0 {
						if size, known := containerVolumeSize(c, s.Volumes); known {
							volumeCol = formatBytes(size)
						} else if len(c.Volumes) == 1 {
							volumeCol = "1 volume"
						} else {
							volumeCol = fmt.Sprintf("%d volumes", len(c.Volumes))
						}
					}

					fmt.Printf("  %-12s %-15s %-20s %10s  %s\n",
						c.ShortID,
						c.ShortName,
						c.Session,
						volumeCol,
						status,
					)
				}
			}

			printProjectVolumes(projectVolumes)
		}
		fmt.Println()

		return nil
	}

	cmd := mflags.NewCommand(fs, handler,
		mflags.WithUsage("List all ISO-managed containers"),
	)

	dispatcher.Dispatch("list", cmd)
}

// displayVolumeName shortens Docker's 64-character generated names, which
// would otherwise swamp the column.
func displayVolumeName(v iso.VolumeUsage) string {
	if v.Anonymous {
		return v.Name[:12] + "..."
	}
	return v.Name
}

// volumeKind labels a volume with what cleaning up the session does to it.
func volumeKind(v iso.VolumeUsage) string {
	switch {
	case v.Cache:
		return "cache (shared, kept until 'iso prune')"
	case v.Anonymous:
		return "anonymous (image-declared)"
	default:
		return "session"
	}
}

// printProjectVolumes prints the deduplicated volume breakdown for a project,
// separating shared cache volumes from per-session ones.
func printProjectVolumes(volumes map[string]iso.VolumeUsage) {
	if len(volumes) == 0 {
		return
	}

	names := make([]string, 0, len(volumes))
	for name := range volumes {
		names = append(names, name)
	}
	sort.Strings(names)

	var cacheTotal, sessionTotal int64
	allKnown := true
	nameWidth := 0
	for _, v := range volumes {
		if w := len(displayVolumeName(v)); w > nameWidth {
			nameWidth = w
		}
		if !v.SizeKnown {
			allKnown = false
			continue
		}
		if v.Cache {
			cacheTotal += v.Size
		} else {
			sessionTotal += v.Size
		}
	}

	header := "\n  Volumes:"
	if allKnown {
		header = fmt.Sprintf("\n  Volumes: %s total (%s shared cache, %s in sessions)",
			formatBytes(cacheTotal+sessionTotal),
			formatBytes(cacheTotal),
			formatBytes(sessionTotal))
	}
	fmt.Println(header)

	for _, name := range names {
		v := volumes[name]

		size := "-"
		if v.SizeKnown {
			size = formatBytes(v.Size)
		}

		fmt.Printf("    %10s  %-*s  %s\n", size, nameWidth, displayVolumeName(v), volumeKind(v))
	}
}

func listOrphaned(withSizes bool) error {
	sessions, err := iso.ListSessions(withSizes)
	if err != nil {
		return err
	}

	var orphaned []iso.Session
	for _, s := range sessions {
		if s.Orphaned {
			orphaned = append(orphaned, s)
		}
	}

	if len(orphaned) == 0 {
		fmt.Println("No orphaned ISO sessions found")
		return nil
	}

	fmt.Println("Orphaned ISO sessions (project directory no longer exists):")

	totalContainers := 0
	var reclaimable int64
	reclaimableKnown := true

	for _, session := range orphaned {
		fmt.Printf("\n%s (session: %s):\n", session.ProjectDir, session.Session)
		fmt.Printf("  %-12s %-35s %s\n", "CONTAINER ID", "NAME", "STATUS")

		for _, c := range session.Containers {
			status := c.Status
			if c.IsService {
				status += " (service: " + c.ServiceName + ")"
			}

			fmt.Printf("  %-12s %-35s %s\n", c.ShortID, c.ShortName, status)
			totalContainers++
		}

		if size, known := session.SessionSize(); known {
			reclaimable += size
			if size > 0 {
				fmt.Printf("  Volumes: %s\n", formatBytes(size))
			}
		} else {
			reclaimableKnown = false
		}
	}

	fmt.Printf("\nTotal orphaned containers: %d\n", totalContainers)
	if reclaimableKnown && reclaimable > 0 {
		fmt.Printf("Reclaimable volume space: %s\n", formatBytes(reclaimable))
	}
	fmt.Println("To clean up orphaned sessions, run: iso cleanup --orphaned")

	return nil
}

// registerPruneCommand registers the 'prune' command
func registerPruneCommand(dispatcher *mflags.Dispatcher) {
	fs := mflags.NewFlagSet("prune")

	handler := func(fs *mflags.FlagSet, args []string) error {
		// Prune doesn't use a specific session since cache volumes are shared
		// We just need a client to access the project configuration
		sessionName, _ := getSession("")
		client, err := iso.New(sessionName)
		if err != nil {
			return err
		}
		defer client.Close()

		return client.Prune()
	}

	cmd := mflags.NewCommand(fs, handler,
		mflags.WithUsage("Remove all cache volumes for the project"),
	)

	dispatcher.Dispatch("prune", cmd)
}

// registerCleanupCommand registers the 'cleanup' command
func registerCleanupCommand(dispatcher *mflags.Dispatcher) {
	fs := mflags.NewFlagSet("cleanup")

	orphaned := fs.Bool("orphaned", 'o', false, "Clean up orphaned sessions (project directory missing)")
	allProjects := fs.Bool("all-projects", 'A', false, "Consider sessions from every project, not just this one")
	sessionList := fs.String("session", 's', "", "Comma-separated session names to remove without prompting")
	stopped := fs.Bool("stopped", 'x', false, "Only consider sessions with no running containers")
	assumeYes := fs.Bool("yes", 'y', false, "Remove every matching session without prompting")
	interactive := fs.Bool("interactive", 'i', false, "Ask for confirmation per session")
	dryRun := fs.Bool("dry-run", 'd', false, "Show what would be cleaned without doing it")
	noSizes := fs.Bool("no-sizes", 'n', false, "Skip volume size calculation (much faster with large caches)")

	handler := func(fs *mflags.FlagSet, args []string) error {
		named := splitSessionNames(*sessionList)

		// Selecting orphans or naming sessions explicitly says which sessions
		// to act on, so those default to running straight through. A bare
		// `iso cleanup` prompts for each session instead.
		prompt := *interactive || (!*orphaned && len(named) == 0 && !*assumeYes)

		candidates, err := gatherCleanupCandidates(cleanupFilter{
			orphaned:    *orphaned,
			allProjects: *allProjects,
			named:       named,
			stopped:     *stopped,
			withSizes:   !*noSizes,
		})
		if err != nil {
			return err
		}

		if len(candidates) == 0 {
			fmt.Println("No sessions to clean up")
			return nil
		}

		selected := candidates
		if prompt {
			selected = promptForSessions(candidates)
			if len(selected) == 0 {
				fmt.Println("No sessions selected for cleanup")
				return nil
			}
		}

		return removeSessions(selected, *dryRun)
	}

	cmd := mflags.NewCommand(fs, handler,
		mflags.WithUsage("Clean up sessions you are done with (interactive by default)"),
	)

	dispatcher.Dispatch("cleanup", cmd)
}

// cleanupFilter describes which sessions `iso cleanup` should consider.
type cleanupFilter struct {
	orphaned    bool
	allProjects bool
	named       []string
	stopped     bool
	withSizes   bool
}

// splitSessionNames parses the comma-separated --session value.
func splitSessionNames(value string) []string {
	var names []string
	for _, name := range strings.Split(value, ",") {
		if name = strings.TrimSpace(name); name != "" {
			names = append(names, name)
		}
	}
	return names
}

// gatherCleanupCandidates returns the sessions matching the filter. Orphaned
// sessions always come from every project, since by definition we cannot be
// standing in their project directory.
func gatherCleanupCandidates(f cleanupFilter) ([]iso.Session, error) {
	var sessions []iso.Session
	var err error

	if f.orphaned || f.allProjects {
		sessions, err = iso.ListSessions(f.withSizes)
	} else {
		sessions, err = iso.ListProjectSessions("", f.withSizes)
	}
	if err != nil {
		return nil, err
	}

	named := make(map[string]bool, len(f.named))
	for _, name := range f.named {
		named[name] = true
	}

	var candidates []iso.Session
	for _, s := range sessions {
		if f.orphaned && !s.Orphaned {
			continue
		}
		if len(named) > 0 && !named[s.Session] {
			continue
		}
		if f.stopped && s.Running {
			continue
		}
		candidates = append(candidates, s)
	}

	// Report session names that matched nothing so a typo is not mistaken for
	// a successful cleanup.
	for _, name := range f.named {
		found := false
		for _, s := range candidates {
			if s.Session == name {
				found = true
				break
			}
		}
		if !found {
			return nil, fmt.Errorf("no session named %q found", name)
		}
	}

	return candidates, nil
}

// describeSession prints a one-block summary of a session for the cleanup UI.
func describeSession(s iso.Session) {
	label := fmt.Sprintf("%s / %s", s.ProjectName, s.Session)
	if s.IsEphemeral() {
		label += "  (ephemeral leftover)"
	}
	fmt.Printf("Session: %s\n", label)
	fmt.Printf("  Project directory: %s", s.ProjectDir)
	if s.Orphaned {
		fmt.Print("  (missing)")
	}
	fmt.Println()

	serviceCount := 0
	runningCount := 0
	for _, c := range s.Containers {
		if c.IsService {
			serviceCount++
		}
		if c.Running {
			runningCount++
		}
	}

	fmt.Printf("  Containers: %d", len(s.Containers))
	if serviceCount > 0 {
		fmt.Printf(" (%d service(s))", serviceCount)
	}
	if runningCount > 0 {
		fmt.Printf(" - %d running", runningCount)
	}
	fmt.Println()

	fmt.Printf("  Created: %s\n", formatAge(s.Created))

	if size, known := s.SessionSize(); known {
		fmt.Printf("  Volumes to remove: %s\n", formatBytes(size))
	} else if len(s.Volumes) > 0 {
		fmt.Printf("  Volumes to remove: %d\n", len(s.Volumes))
	}
}

// promptForSessions asks about each candidate session and returns the ones the
// user accepted.
func promptForSessions(sessions []iso.Session) []iso.Session {
	fmt.Printf("Found %d session(s):\n\n", len(sessions))

	reader := bufio.NewReader(os.Stdin)
	var selected []iso.Session

	for _, s := range sessions {
		describeSession(s)

		if s.Running {
			fmt.Print("  Session is RUNNING. Stop and remove it? [y/N]: ")
		} else {
			fmt.Print("  Remove this session? [y/N]: ")
		}

		response, err := reader.ReadString('\n')
		if err != nil && response == "" {
			// stdin closed - treat as declining the rest
			fmt.Println()
			break
		}

		switch strings.ToLower(strings.TrimSpace(response)) {
		case "y", "yes":
			selected = append(selected, s)
			fmt.Println("  ✓ Marked for cleanup")
		case "q", "quit":
			fmt.Println("  Stopping here")
			fmt.Println()
			return selected
		default:
			fmt.Println("  Skipped")
		}
		fmt.Println()
	}

	return selected
}

// removeSessions performs (or, for a dry run, describes) the cleanup.
func removeSessions(sessions []iso.Session, dryRun bool) error {
	if dryRun {
		for _, s := range sessions {
			describeSession(s)
			fmt.Println()
		}
	}

	report, err := iso.RemoveSessions(sessions, dryRun)
	if err != nil {
		return err
	}

	verb := "Cleaned up"
	if dryRun {
		verb = "[DRY RUN] Would clean up"
	}

	summary := fmt.Sprintf("%s %d session(s): %d container(s), %d volume(s), %d network(s)",
		verb, report.Sessions, report.Containers, report.Volumes, report.Networks)
	if report.Bytes > 0 {
		summary += fmt.Sprintf(", %s reclaimed", formatBytes(report.Bytes))
	}
	fmt.Println(summary)

	return nil
}

// reapZombies reaps any zombie child processes
func reapZombies() {
	for {
		var wstatus syscall.WaitStatus
		pid, err := syscall.Wait4(-1, &wstatus, syscall.WNOHANG, nil)
		if err != nil || pid <= 0 {
			// No more children to reap
			break
		}
		slog.Debug("reaped child process", "pid", pid, "exit_status", wstatus.ExitStatus())
	}
}

// registerInitCommand registers the 'init' command for project initialization
func registerInitCommand(dispatcher *mflags.Dispatcher) {
	fs := mflags.NewFlagSet("init")

	handler := func(fs *mflags.FlagSet, args []string) error {
		return iso.InitProject()
	}

	cmd := mflags.NewCommand(fs, handler,
		mflags.WithUsage("Initialize .iso directory with AI-generated Dockerfile and services.yml"),
	)

	dispatcher.Dispatch("init", cmd)
}

// registerInternalInitCommand registers the '_internal-init' command for container init process
func registerInternalInitCommand(dispatcher *mflags.Dispatcher) {
	fs := mflags.NewFlagSet("_internal-init")

	handler := func(fs *mflags.FlagSet, args []string) error {
		// Set up signal handling
		sigChan := make(chan os.Signal, 1)
		signal.Notify(sigChan, syscall.SIGTERM, syscall.SIGINT, syscall.SIGCHLD)

		slog.Info("init process started, waiting for signals")

		// Sleep loop with zombie reaping
		ticker := time.NewTicker(1 * time.Second)
		defer ticker.Stop()

		for {
			select {
			case sig := <-sigChan:
				if sig == syscall.SIGCHLD {
					// Reap zombie processes
					reapZombies()
				} else {
					slog.Info("received signal, exiting", "signal", sig)
					return nil
				}
			case <-ticker.C:
				// Periodically reap zombies in case we missed a SIGCHLD
				reapZombies()
			}
		}
	}

	cmd := mflags.NewCommand(fs, handler,
		mflags.WithUsage("Run as init process in container (internal use only)"),
	)

	dispatcher.Dispatch("_internal-init", cmd)
}

// waitForServices waits for all services in ISO_SERVICES to be reachable
func waitForServices(isoServices string) error {
	services := strings.Split(isoServices, ",")

	for _, serviceSpec := range services {
		parts := strings.Split(serviceSpec, ":")
		if len(parts) != 2 {
			return fmt.Errorf("invalid service spec: %s (expected format: service:port)", serviceSpec)
		}

		host := parts[0]
		port := parts[1]
		address := net.JoinHostPort(host, port)

		slog.Debug("waiting for service", "service", host, "address", address)

		// Try to connect with retries
		maxAttempts := 30
		for attempt := 1; attempt <= maxAttempts; attempt++ {
			conn, err := net.DialTimeout("tcp", address, 1*time.Second)
			if err == nil {
				conn.Close()
				slog.Debug("service ready", "service", host, "address", address)
				break
			}

			if attempt == maxAttempts {
				return fmt.Errorf("service %s not ready after %d attempts", host, maxAttempts)
			}

			time.Sleep(1 * time.Second)
		}
	}

	return nil
}

// registerInEnvCommand registers the 'in-env run' command
func registerInEnvCommand(dispatcher *mflags.Dispatcher) {
	fs := mflags.NewFlagSet("in-env run")
	fs.AllowUnknownFlags(true)

	handler := func(fs *mflags.FlagSet, args []string) error {
		// Combine positional args and unknown flags to form the command
		command := append(args, fs.UnknownFlags()...)

		if len(command) == 0 {
			return fmt.Errorf("no command specified")
		}

		// Get workdir from environment (defaults to /workspace)
		workDir := os.Getenv("ISO_WORKDIR")
		if workDir == "" {
			workDir = "/workspace"
		}

		// Wait for services to be ready if ISO_SERVICES is set
		if isoServices := os.Getenv("ISO_SERVICES"); isoServices != "" {
			if err := waitForServices(isoServices); err != nil {
				return err
			}
		}

		// Execute pre-run.sh if it exists
		preRunScript := fmt.Sprintf("%s/.iso/pre-run.sh", workDir)
		if _, err := os.Stat(preRunScript); err == nil {
			// Script exists, execute it
			cmd := exec.Command("bash", preRunScript)
			cmd.Stdout = os.Stdout
			cmd.Stderr = os.Stderr
			cmd.Stdin = os.Stdin

			if err := cmd.Run(); err != nil {
				if exitErr, ok := err.(*exec.ExitError); ok {
					return &ExitError{Code: exitErr.ExitCode()}
				}
				return fmt.Errorf("failed to execute pre-run.sh: %w", err)
			}
		}

		// Execute the main command
		mainCmd := exec.Command(command[0], command[1:]...)
		mainCmd.Stdout = os.Stdout
		mainCmd.Stderr = os.Stderr
		mainCmd.Stdin = os.Stdin

		mainExitCode := 0
		if err := mainCmd.Run(); err != nil {
			if exitErr, ok := err.(*exec.ExitError); ok {
				mainExitCode = exitErr.ExitCode()
			} else {
				return fmt.Errorf("failed to execute command: %w", err)
			}
		}

		// Execute post-run.sh if it exists
		postRunScript := fmt.Sprintf("%s/.iso/post-run.sh", workDir)
		if _, err := os.Stat(postRunScript); err == nil {
			// Script exists, execute it
			cmd := exec.Command("bash", postRunScript)
			cmd.Stdout = os.Stdout
			cmd.Stderr = os.Stderr
			cmd.Stdin = os.Stdin

			if err := cmd.Run(); err != nil {
				if exitErr, ok := err.(*exec.ExitError); ok {
					slog.Warn("post-run.sh exited with non-zero code", "exit_code", exitErr.ExitCode())
				} else {
					slog.Warn("failed to execute post-run.sh", "error", err)
				}
			}
		}

		// Return the main command's exit code
		if mainExitCode != 0 {
			return &ExitError{Code: mainExitCode}
		}

		return nil
	}

	cmd := mflags.NewCommand(fs, handler,
		mflags.WithUsage("Run a command with pre/post hooks (internal use inside container)"),
	)

	dispatcher.Dispatch("in-env run", cmd)
}

// registerAgentHelpCommand registers the 'agent-help' command
func registerAgentHelpCommand(dispatcher *mflags.Dispatcher) {
	fs := mflags.NewFlagSet("agent-help")

	handler := func(fs *mflags.FlagSet, args []string) error {
		fmt.Print(agentHelpContent)
		return nil
	}

	cmd := mflags.NewCommand(fs, handler,
		mflags.WithUsage("Output markdown documentation for AI agents"),
	)

	dispatcher.Dispatch("agent-help", cmd)
}

// registerVersionCommand registers the 'version' command
func registerVersionCommand(dispatcher *mflags.Dispatcher) {
	fs := mflags.NewFlagSet("version")

	handler := func(fs *mflags.FlagSet, args []string) error {
		if version == "dev" {
			fmt.Printf("iso %s (commit: %s)\n", version, commit)
		} else {
			fmt.Printf("iso %s\n", version)
		}
		return nil
	}

	cmd := mflags.NewCommand(fs, handler,
		mflags.WithUsage("Show version information"),
	)

	dispatcher.Dispatch("version", cmd)
}

// registerPeersUpCommand registers the 'peers up' command
func registerPeersUpCommand(dispatcher *mflags.Dispatcher) {
	fs := mflags.NewFlagSet("peers up")

	handler := func(fs *mflags.FlagSet, args []string) error {
		client, err := iso.New(getPeersSession())
		if err != nil {
			return err
		}
		defer client.Close()

		return client.PeersUp(args)
	}

	cmd := mflags.NewCommand(fs, handler,
		mflags.WithUsage("Start all or specific peer containers"),
	)

	dispatcher.Dispatch("peers up", cmd)
}

// registerPeersDownCommand registers the 'peers down' command
func registerPeersDownCommand(dispatcher *mflags.Dispatcher) {
	fs := mflags.NewFlagSet("peers down")

	handler := func(fs *mflags.FlagSet, args []string) error {
		client, err := iso.New(getPeersSession())
		if err != nil {
			return err
		}
		defer client.Close()

		return client.PeersDown()
	}

	cmd := mflags.NewCommand(fs, handler,
		mflags.WithUsage("Stop and remove all peer containers"),
	)

	dispatcher.Dispatch("peers down", cmd)
}

// registerPeersExecCommand registers the 'peers exec' command
func registerPeersExecCommand(dispatcher *mflags.Dispatcher) {
	fs := mflags.NewFlagSet("peers exec")

	all := fs.Bool("all", 'a', false, "Execute command on all peers")

	// Allow unknown flags to pass through to the command
	fs.AllowUnknownFlags(true)

	handler := func(fs *mflags.FlagSet, args []string) error {
		client, err := iso.New(getPeersSession())
		if err != nil {
			return err
		}
		defer client.Close()

		// Combine args and unknown flags
		fullArgs := append(args, fs.UnknownFlags()...)

		if *all {
			// Execute on all peers - command comes after --all
			if len(fullArgs) == 0 {
				return fmt.Errorf("no command specified")
			}
			return client.PeersExecAll(fullArgs, nil)
		}

		// Single peer execution: peers exec <peer> -- <command>
		if len(fullArgs) < 2 {
			return fmt.Errorf("usage: iso peers exec <peer> -- <command>")
		}

		peerName := fullArgs[0]
		command := fullArgs[1:]

		exitCode, err := client.PeersExec(peerName, command, nil)
		if err != nil {
			return err
		}
		if exitCode != 0 {
			return &ExitError{Code: exitCode}
		}
		return nil
	}

	cmd := mflags.NewCommand(fs, handler,
		mflags.WithUsage("Execute a command in a peer container"),
	)

	dispatcher.Dispatch("peers exec", cmd)
}

// registerPeersShellCommand registers the 'peers shell' command
func registerPeersShellCommand(dispatcher *mflags.Dispatcher) {
	fs := mflags.NewFlagSet("peers shell")

	handler := func(fs *mflags.FlagSet, args []string) error {
		if len(args) != 1 {
			return fmt.Errorf("usage: iso peers shell <peer>")
		}

		peerName := args[0]

		client, err := iso.New(getPeersSession())
		if err != nil {
			return err
		}
		defer client.Close()

		exitCode, err := client.PeersShell(peerName)
		if err != nil {
			return err
		}
		if exitCode != 0 {
			return &ExitError{Code: exitCode}
		}
		return nil
	}

	cmd := mflags.NewCommand(fs, handler,
		mflags.WithUsage("Open an interactive shell in a peer container"),
	)

	dispatcher.Dispatch("peers shell", cmd)
}

// registerPeersStatusCommand registers the 'peers status' command
func registerPeersStatusCommand(dispatcher *mflags.Dispatcher) {
	fs := mflags.NewFlagSet("peers status")

	handler := func(fs *mflags.FlagSet, args []string) error {
		client, err := iso.New(getPeersSession())
		if err != nil {
			return err
		}
		defer client.Close()

		if !client.HasPeers() {
			fmt.Println("No peers configured - create .iso/peers.yml first")
			return nil
		}

		statuses, err := client.PeersStatus()
		if err != nil {
			return err
		}

		if len(statuses) == 0 {
			fmt.Println("No peers defined")
			return nil
		}

		fmt.Printf("%-15s %-20s %-12s %s\n", "PEER", "HOSTNAME", "CONTAINER", "STATE")
		for _, status := range statuses {
			containerID := status.ContainerID
			if containerID == "" {
				containerID = "-"
			}
			fmt.Printf("%-15s %-20s %-12s %s\n",
				status.Name,
				status.Hostname,
				containerID,
				status.State,
			)
		}

		return nil
	}

	cmd := mflags.NewCommand(fs, handler,
		mflags.WithUsage("Show peer container status"),
	)

	dispatcher.Dispatch("peers status", cmd)
}
