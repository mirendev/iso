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
