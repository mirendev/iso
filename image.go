package iso

import (
	"bufio"
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"hash"
	"io"
	"io/fs"
	"log/slog"
	"os"
	"path"
	"path/filepath"
	"sort"
	"strings"
)

// imageTag names the image a Dockerfile builds by the content that goes into
// it: the Dockerfile itself plus every local file it COPYs or ADDs from the
// build context. An edited Dockerfile then yields a tag that doesn't exist yet,
// so the next run builds it instead of reusing a stale image, and two worktrees
// with different Dockerfiles get different images even when their directories
// share a name.
//
// A build input that can't be read is noted by name and skipped rather than
// failing: Docker will report it if the build actually needs it, and a stray
// root-owned file in the workspace shouldn't block every iso command.
func imageTag(contextDir, dockerfilePath string) (string, error) {
	dockerfile, err := os.ReadFile(dockerfilePath)
	if err != nil {
		return "", fmt.Errorf("reading Dockerfile: %w", err)
	}

	h := sha256.New()
	h.Write(fmt.Appendf(nil, "dockerfile\x00%d\x00", len(dockerfile)))
	h.Write(dockerfile)

	for _, rel := range copySourceFiles(contextDir, copySources(dockerfile)) {
		if err := hashFile(h, contextDir, rel); err != nil {
			slog.Debug("skipping unreadable build input in image tag", "error", err)
			h.Write(fmt.Appendf(nil, "unreadable\x00%s\x00", filepath.ToSlash(rel)))
		}
	}

	return hex.EncodeToString(h.Sum(nil))[:12], nil
}

func hashFile(h hash.Hash, contextDir, rel string) error {
	path := filepath.Join(contextDir, rel)
	info, err := os.Lstat(path)
	if err != nil {
		return fmt.Errorf("hashing build input %s: %w", rel, err)
	}
	h.Write(fmt.Appendf(nil, "file\x00%s\x00%o\x00", filepath.ToSlash(rel), info.Mode()))

	if info.Mode()&fs.ModeSymlink != 0 {
		target, err := os.Readlink(path)
		if err != nil {
			return fmt.Errorf("hashing build input %s: %w", rel, err)
		}
		h.Write(fmt.Appendf(nil, "%s\x00", target))
		return nil
	}

	f, err := os.Open(path)
	if err != nil {
		return fmt.Errorf("hashing build input %s: %w", rel, err)
	}
	defer func() { _ = f.Close() }()
	h.Write(fmt.Appendf(nil, "%d\x00", info.Size()))
	if _, err := io.Copy(h, f); err != nil {
		return fmt.Errorf("hashing build input %s: %w", rel, err)
	}
	return nil
}

// copySources returns the build-context sources of the Dockerfile's COPY and
// ADD instructions. Copies from another stage or image (--from) and remote ADD
// sources aren't in the build context, so they're left out.
func copySources(dockerfile []byte) []string {
	var sources []string
	for _, line := range logicalLines(dockerfile) {
		fields := strings.Fields(line)
		if len(fields) < 2 {
			continue
		}
		instr := strings.ToUpper(fields[0])
		if instr != "COPY" && instr != "ADD" {
			continue
		}

		args := fields[1:]
		fromStage := false
		for len(args) > 0 && strings.HasPrefix(args[0], "--") {
			if strings.HasPrefix(args[0], "--from=") {
				fromStage = true
			}
			args = args[1:]
		}
		if fromStage {
			continue
		}

		rest := strings.TrimSpace(strings.Join(args, " "))
		if strings.HasPrefix(rest, "[") {
			var exec []string
			if err := json.Unmarshal([]byte(rest), &exec); err == nil {
				args = exec
			}
		}
		if len(args) < 2 {
			continue
		}

		for _, src := range args[:len(args)-1] {
			if strings.Contains(src, "://") || strings.HasPrefix(src, "git@") {
				continue
			}
			sources = append(sources, src)
		}
	}
	return sources
}

// logicalLines joins backslash-continued lines and drops comments and blank
// lines.
func logicalLines(dockerfile []byte) []string {
	var lines []string
	var cur strings.Builder
	scanner := bufio.NewScanner(bytes.NewReader(dockerfile))
	scanner.Buffer(make([]byte, 0, 64*1024), 1024*1024)
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if cur.Len() == 0 && (line == "" || strings.HasPrefix(line, "#")) {
			continue
		}
		if strings.HasSuffix(line, "\\") {
			cur.WriteString(strings.TrimSuffix(line, "\\"))
			cur.WriteString(" ")
			continue
		}
		cur.WriteString(line)
		lines = append(lines, cur.String())
		cur.Reset()
	}
	if cur.Len() > 0 {
		lines = append(lines, cur.String())
	}
	return lines
}

// copySourceFiles expands sources (globs and directories) into a sorted,
// de-duplicated list of files relative to contextDir. A source that matches
// nothing, or a directory that can't be read, is skipped: the build itself will
// report it. A copy of the whole context is skipped too (see below), and VCS
// metadata and the iso binary extracted into .iso are left out of directory
// copies, so iso's own first run doesn't retag.
func copySourceFiles(contextDir string, sources []string) []string {
	seen := map[string]bool{}
	for _, src := range sources {
		// A copy of the whole context would put every edit and every build
		// artifact in the workspace into the tag, rebuilding on nearly every
		// run. iso bind-mounts the workspace anyway, so the copy's contents
		// matter less than a stable image; the Dockerfile line still counts.
		if path.Clean("/"+filepath.ToSlash(src)) == "/" {
			continue
		}
		pattern := filepath.Join(contextDir, filepath.FromSlash(strings.TrimPrefix(src, "/")))
		matches, err := filepath.Glob(pattern)
		if err != nil {
			continue
		}
		for _, match := range matches {
			_ = filepath.WalkDir(match, func(walked string, d fs.DirEntry, err error) error {
				rel, relErr := filepath.Rel(contextDir, walked)
				if relErr != nil {
					return nil
				}
				if d != nil && d.IsDir() {
					if d.Name() == ".git" || d.Name() == ".jj" {
						return filepath.SkipDir
					}
					if err != nil {
						return filepath.SkipDir
					}
					return nil
				}
				if d != nil && d.Name() == ".git" {
					return nil // a worktree's .git file
				}
				if filepath.Dir(rel) == ".iso" && strings.HasPrefix(filepath.Base(rel), "iso-linux-") {
					return nil
				}
				// An unreadable entry is still listed, so hashFile records it
				// by name.
				seen[rel] = true
				return nil
			})
		}
	}

	files := make([]string, 0, len(seen))
	for f := range seen {
		files = append(files, f)
	}
	sort.Strings(files)
	return files
}
