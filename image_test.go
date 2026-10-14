package iso

import (
	"os"
	"path/filepath"
	"reflect"
	"testing"
)

func writeFile(t *testing.T, path, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

// TestImageTagTracksBuildInputs locks in what the tag is for: an edit to the
// Dockerfile, or to a file it copies in, must produce a tag no image has yet,
// so the next run builds instead of reusing what the old Dockerfile built.
func TestImageTagTracksBuildInputs(t *testing.T) {
	root := t.TempDir()
	dockerfile := filepath.Join(root, ".iso", "Dockerfile")
	writeFile(t, dockerfile, "FROM alpine\nCOPY ./hack/config.toml /etc/config.toml\n")
	writeFile(t, filepath.Join(root, "hack", "config.toml"), "a = 1\n")
	writeFile(t, filepath.Join(root, "unrelated.go"), "package main\n")

	tag := func() string {
		t.Helper()
		got, err := imageTag(root, dockerfile)
		if err != nil {
			t.Fatal(err)
		}
		return got
	}

	first := tag()
	if len(first) != 12 {
		t.Fatalf("tag %q should be 12 hex characters", first)
	}
	if again := tag(); again != first {
		t.Fatalf("tag not stable: %q then %q", first, again)
	}

	writeFile(t, filepath.Join(root, "unrelated.go"), "package main // edited\n")
	if got := tag(); got != first {
		t.Fatalf("editing a file the Dockerfile doesn't copy changed the tag: %q -> %q", first, got)
	}

	writeFile(t, filepath.Join(root, "hack", "config.toml"), "a = 2\n")
	copied := tag()
	if copied == first {
		t.Fatal("editing a copied file didn't change the tag")
	}

	writeFile(t, dockerfile, "FROM alpine:3.21\nCOPY ./hack/config.toml /etc/config.toml\n")
	if got := tag(); got == copied {
		t.Fatal("editing the Dockerfile didn't change the tag")
	}
}

func TestCopySources(t *testing.T) {
	dockerfile := `FROM golang AS build
# COPY commented/out /nope
COPY go.mod go.sum /src/
COPY --chown=1000:1000 ./hack/config.toml \
     /etc/config.toml
ADD ["with space.txt", "/dst/"]
COPY --from=build /out/bin /usr/local/bin/bin
ADD https://example.com/file.tgz /tmp/
copy lower.txt /lower.txt
RUN echo COPY not-an-instruction /x
`
	got := copySources([]byte(dockerfile))
	want := []string{"go.mod", "go.sum", "./hack/config.toml", "with space.txt", "lower.txt"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("copySources = %q, want %q", got, want)
	}
}

func TestCopySourceFilesExpandsGlobsAndDirs(t *testing.T) {
	root := t.TempDir()
	writeFile(t, filepath.Join(root, "conf", "a.toml"), "")
	writeFile(t, filepath.Join(root, "conf", "nested", "b.toml"), "")
	writeFile(t, filepath.Join(root, "x.sh"), "")
	writeFile(t, filepath.Join(root, "y.sh"), "")

	got := copySourceFiles(root, []string{"conf", "*.sh", "missing.txt", "x.sh"})
	want := []string{
		filepath.Join("conf", "a.toml"),
		filepath.Join("conf", "nested", "b.toml"),
		"x.sh",
		"y.sh",
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("copySourceFiles = %q, want %q", got, want)
	}
}

// TestImageTagIgnoresVCSAndIsoBinary keeps a directory copy that happens to
// contain .git or .iso stable: commits and fetches rewrite .git, and iso
// extracts its own binary into .iso on first run.
func TestImageTagIgnoresVCSAndIsoBinary(t *testing.T) {
	root := t.TempDir()
	dockerfile := filepath.Join(root, ".iso", "Dockerfile")
	writeFile(t, dockerfile, "FROM alpine\nCOPY ./ /src\nCOPY .iso /iso\nCOPY sub /sub\n")
	writeFile(t, filepath.Join(root, "sub", "main.go"), "package main\n")
	writeFile(t, filepath.Join(root, "sub", ".git"), "gitdir: elsewhere\n")
	writeFile(t, filepath.Join(root, ".git", "HEAD"), "ref: refs/heads/main\n")

	before, err := imageTag(root, dockerfile)
	if err != nil {
		t.Fatal(err)
	}

	writeFile(t, filepath.Join(root, ".git", "HEAD"), "ref: refs/heads/other\n")
	writeFile(t, filepath.Join(root, "sub", ".git"), "gitdir: moved\n")
	writeFile(t, filepath.Join(root, ".iso", "iso-linux-amd64"), "binary")
	after, err := imageTag(root, dockerfile)
	if err != nil {
		t.Fatal(err)
	}
	if after != before {
		t.Fatalf("tag changed on .git or iso binary changes: %q -> %q", before, after)
	}
}

// TestImageTagSkipsWholeContextCopy keeps `COPY . /src` from retagging on
// every edit or build artifact in the bind-mounted workspace.
func TestImageTagSkipsWholeContextCopy(t *testing.T) {
	root := t.TempDir()
	dockerfile := filepath.Join(root, ".iso", "Dockerfile")
	writeFile(t, dockerfile, "FROM alpine\nCOPY . /src\n")
	writeFile(t, filepath.Join(root, "main.go"), "package main\n")

	before, err := imageTag(root, dockerfile)
	if err != nil {
		t.Fatal(err)
	}
	writeFile(t, filepath.Join(root, "main.go"), "package main // edited\n")
	writeFile(t, filepath.Join(root, "bin", "app"), "built")
	after, err := imageTag(root, dockerfile)
	if err != nil {
		t.Fatal(err)
	}
	if after != before {
		t.Fatalf("whole-context copy retagged on a workspace edit: %q -> %q", before, after)
	}
}

// TestImageTagToleratesUnreadableInputs keeps a stray unreadable file (say,
// root-owned output a container left in the workspace) from failing every iso
// command, including the ones used to recover.
func TestImageTagToleratesUnreadableInputs(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("root can read anything")
	}
	root := t.TempDir()
	dockerfile := filepath.Join(root, ".iso", "Dockerfile")
	writeFile(t, dockerfile, "FROM alpine\nCOPY conf /conf\n")
	writeFile(t, filepath.Join(root, "conf", "ok.toml"), "")
	locked := filepath.Join(root, "conf", "locked.toml")
	writeFile(t, locked, "secret")
	if err := os.Chmod(locked, 0); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chmod(locked, 0o644) })

	if _, err := imageTag(root, dockerfile); err != nil {
		t.Fatalf("imageTag failed on an unreadable input: %v", err)
	}
}
