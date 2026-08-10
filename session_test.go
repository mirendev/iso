package iso

import "testing"

// TestIsCacheVolume locks the rule that decides whether cleaning up a session
// deletes a volume. Getting this wrong in the "cache" direction leaves session
// data behind forever; getting it wrong in the "session" direction deletes a
// package cache that other worktrees are still using.
func TestIsCacheVolume(t *testing.T) {
	cases := []struct {
		name   string
		volume string
		detail volumeDetail
		want   bool
	}{
		{
			name:   "labelled cache",
			volume: "proj-cache-go-pkg-mod",
			detail: volumeDetail{Labels: map[string]string{"iso.volume.type": "cache"}},
			want:   true,
		},
		{
			name:   "labelled session",
			volume: "proj-dev-data",
			detail: volumeDetail{Labels: map[string]string{"iso.volume.type": "session"}},
			want:   false,
		},
		{
			// A session that happens to sit under a project whose name
			// contains "cache" must still be treated as a session volume when
			// the label says so.
			name:   "label wins over the name",
			volume: "proj-cache-data",
			detail: volumeDetail{Labels: map[string]string{"iso.volume.type": "session"}},
			want:   false,
		},
		{
			// Volumes created before iso labelled them have to be classified
			// from their name alone.
			name:   "unlabelled cache falls back to the name",
			volume: "proj-cache-go-pkg-mod",
			want:   true,
		},
		{
			name:   "unlabelled session falls back to the name",
			volume: "proj-dev-data",
			want:   false,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := isCacheVolume(tc.volume, tc.detail); got != tc.want {
				t.Fatalf("isCacheVolume(%q) = %v, want %v", tc.volume, got, tc.want)
			}
		})
	}
}

// TestIsAnonymousVolume checks the detection of the names Docker generates for
// volumes an image declares with VOLUME.
func TestIsAnonymousVolume(t *testing.T) {
	const hex64 = "69196412a7fc8aa4f5cd0397f584cdccea279224bd2e6d4b8773813de9c02840"

	cases := []struct {
		name   string
		volume string
		want   bool
	}{
		{"docker generated name", hex64, true},
		{"named volume", "proj-dev-data", false},
		{"too short", hex64[:63], false},
		{"right length but not hex", hex64[:63] + "z", false},
		{"empty", "", false},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := isAnonymousVolume(tc.volume); got != tc.want {
				t.Fatalf("isAnonymousVolume(%q) = %v, want %v", tc.volume, got, tc.want)
			}
		})
	}
}

// TestSessionSize checks that the size reported for a session covers only the
// volumes cleanup would actually remove: shared caches are excluded, because
// they survive the session.
func TestSessionSize(t *testing.T) {
	cases := []struct {
		name      string
		volumes   []VolumeUsage
		want      int64
		wantKnown bool
	}{
		{
			name: "excludes shared cache",
			volumes: []VolumeUsage{
				{Name: "data", Size: 100, SizeKnown: true},
				{Name: "anon", Size: 25, SizeKnown: true, Anonymous: true},
				{Name: "cache", Size: 9000, SizeKnown: true, Cache: true},
			},
			want:      125,
			wantKnown: true,
		},
		{
			name: "unknown size makes the total unknown",
			volumes: []VolumeUsage{
				{Name: "data", Size: 100, SizeKnown: true},
				{Name: "other"},
			},
			want:      100,
			wantKnown: false,
		},
		{
			// A session with nothing but a shared cache frees no space.
			name:      "cache only",
			volumes:   []VolumeUsage{{Name: "cache", Size: 9000, SizeKnown: true, Cache: true}},
			want:      0,
			wantKnown: true,
		},
		{
			name:      "no volumes",
			want:      0,
			wantKnown: true,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			s := Session{Volumes: tc.volumes}

			got, known := s.SessionSize()
			if got != tc.want || known != tc.wantKnown {
				t.Fatalf("SessionSize() = (%d, %v), want (%d, %v)", got, known, tc.want, tc.wantKnown)
			}
		})
	}
}

// TestIsIsoOwnedNetwork guards the check that stops cleanup from deleting a
// network ISO did not create. Cleanup takes networks straight off the
// containers, so a network the user attached an ISO container to by hand is
// indistinguishable from one ISO made unless this check holds it back.
func TestIsIsoOwnedNetwork(t *testing.T) {
	session := Session{ProjectName: "myapp", Session: "dev"}
	defaultSession := Session{ProjectName: "myapp", Session: "default"}

	cases := []struct {
		name    string
		network string
		labels  map[string]string
		session Session
		want    bool
	}{
		{
			// The label is the real signal, and it works even for a peers
			// network whose name came from the user's peers.yml.
			name:    "labelled network with a name ISO would never generate",
			network: "my-custom-test-cluster",
			labels:  map[string]string{"iso.managed": "true"},
			session: session,
			want:    true,
		},
		{
			name:    "unlabelled network matching the session naming",
			network: "myapp-dev-network",
			session: session,
			want:    true,
		},
		{
			name:    "unlabelled network matching the default-session naming",
			network: "myapp-network",
			session: defaultSession,
			want:    true,
		},
		{
			name:    "unlabelled peers network for a session",
			network: "myapp-dev-iso-peers",
			session: session,
			want:    true,
		},
		{
			// The case that motivated the check: no label, no ISO-generated
			// name, so leave it be.
			name:    "foreign network the user attached by hand",
			network: "my-custom-test-cluster",
			session: session,
			want:    false,
		},
		{
			// Belonging to a different project is not ours to remove either.
			name:    "another project's network",
			network: "otherapp-dev-network",
			session: session,
			want:    false,
		},
		{
			name:    "explicitly non-managed label",
			network: "myapp-dev-network",
			labels:  map[string]string{"iso.managed": "false"},
			session: session,
			want:    true, // name still matches what ISO generates
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := isIsoOwnedNetwork(tc.network, tc.labels, tc.session)
			if got != tc.want {
				t.Fatalf("isIsoOwnedNetwork(%q, %v) = %v, want %v",
					tc.network, tc.labels, got, tc.want)
			}
		})
	}
}

// TestIsEphemeral checks that throwaway sessions created by a bare `iso run`
// are distinguishable from ones the user named.
func TestIsEphemeral(t *testing.T) {
	if !(Session{Session: "eph-AbCdEfGh"}).IsEphemeral() {
		t.Fatal("session with the eph- prefix should be ephemeral")
	}
	if (Session{Session: "dev"}).IsEphemeral() {
		t.Fatal("named session should not be ephemeral")
	}
	if (Session{Session: "ephemeral-looking"}).IsEphemeral() {
		t.Fatal("only the eph- prefix marks an ephemeral session")
	}
}
