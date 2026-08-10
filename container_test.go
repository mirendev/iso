package iso

import (
	"strings"
	"testing"
)

// TestServiceContainerNamesAreDeterministic locks the invariant that a
// persistent session's service container name is stable and carries no per-run
// component. `iso run` on a persistent session reuses these deterministically
// named containers, so if a run id (or any nondeterministic suffix) ever leaked
// into the name, repeated runs would create duplicate service containers that
// collide on the shared service DNS alias (e.g. two `etcd`) and hang every
// client — the failure mode fixed by routing persistent sessions through the
// deterministic service path instead of the per-run "fresh" path.
func TestServiceContainerNamesAreDeterministic(t *testing.T) {
	cases := []struct {
		name    string
		session string
		service string
		want    string
	}{
		{"named session", "dev", "etcd", "proj-dev_etcd"},
		{"default session", "default", "etcd", "proj_etcd"},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			cm := &containerManager{projectName: "proj", session: tc.session}

			got := cm.getServiceContainerName(tc.service)
			if got != tc.want {
				t.Fatalf("getServiceContainerName(%q) = %q, want %q", tc.service, got, tc.want)
			}

			// Stable across calls: no timestamp/random component.
			if again := cm.getServiceContainerName(tc.service); again != got {
				t.Fatalf("service name not deterministic: %q then %q", got, again)
			}

			if strings.Contains(got, "fresh") {
				t.Fatalf("persistent service name must not use the per-run fresh scheme: %q", got)
			}
		})
	}
}

// TestPeerContainerNamesAreSessionScoped locks the property that two workspaces
// of the same repo cannot land on one another's peer containers.
//
// worktreeProjectName is not the isolation it looks like: it falls back to the
// directory basename whenever git worktree detection fails, and that detection
// shells out to git, so it fails for every jj workspace. Two checkouts both
// named "runtime" therefore produce the same worktreeProjectName, and before the
// session was part of the name the second `iso peers up` silently adopted the
// first's containers, /src mount and all.
//
// The default case keeps the historical name so standalone use is unchanged.
func TestPeerContainerNamesAreSessionScoped(t *testing.T) {
	cases := []struct {
		name    string
		session string
		want    string
	}{
		{"default peers session keeps the legacy name", PeersDefaultSession, "runtime-iso-peer-coordinator"},
		{"named session is scoped", "rig-a-runtime", "runtime-rig-a-runtime-iso-peer-coordinator"},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			cm := &containerManager{worktreeProjectName: "runtime", session: tc.session}

			if got := cm.getPeerContainerName("coordinator"); got != tc.want {
				t.Fatalf("getPeerContainerName = %q, want %q", got, tc.want)
			}
		})
	}

	// The point of the whole exercise: same project, different sessions, no collision.
	a := (&containerManager{worktreeProjectName: "runtime", session: "rig-a-runtime"}).getPeerContainerName("coordinator")
	b := (&containerManager{worktreeProjectName: "runtime", session: "rig-b-runtime"}).getPeerContainerName("coordinator")
	if a == b {
		t.Fatalf("two sessions produced the same peer container name: %q", a)
	}
}

// TestDefaultPeersNetworkNameIsSessionScoped covers the network alongside the
// containers. It is derived from project and session rather than from peers.yml
// so that teardown can still name the network to remove after the config has
// been deleted, which is exactly when a stale network would otherwise survive.
func TestDefaultPeersNetworkNameIsSessionScoped(t *testing.T) {
	if got, want := defaultPeersNetworkName("runtime", PeersDefaultSession), "runtime-iso-peers"; got != want {
		t.Fatalf("default session: got %q, want %q", got, want)
	}
	if got, want := defaultPeersNetworkName("runtime", "rig-a-runtime"), "runtime-rig-a-runtime-iso-peers"; got != want {
		t.Fatalf("named session: got %q, want %q", got, want)
	}
	if defaultPeersNetworkName("runtime", "rig-a-runtime") == defaultPeersNetworkName("runtime", "rig-b-runtime") {
		t.Fatal("two sessions produced the same peers network name")
	}
}
