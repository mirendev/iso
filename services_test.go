package iso

import "testing"

// TestSanitizeProjectName locks the invariant that a project name derived from a
// directory is a valid Docker image repository name. Docker rejects uppercase
// letters in repository names, so a directory like "MyProject" must be
// lowercased before it becomes an image tag.
func TestSanitizeProjectName(t *testing.T) {
	cases := []struct {
		in   string
		want string
	}{
		{"myproject", "myproject"},
		{"MyProject", "myproject"},
		{"MY_PROJECT", "my_project"},
		{"My Project", "my-project"},
		{"café", "caf"}, // non-ascii letter becomes '-', then trailing '-' is trimmed
		{"weird!@#chars", "weird---chars"},
		{"-leading.trailing-", "leading.trailing"},
		{"...", "project"},
		{"", "project"},
	}

	for _, tc := range cases {
		if got := sanitizeProjectName(tc.in); got != tc.want {
			t.Errorf("sanitizeProjectName(%q) = %q, want %q", tc.in, got, tc.want)
		}
	}
}
