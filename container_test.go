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
