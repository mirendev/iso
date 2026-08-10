package main

import "testing"

// TestFormatBytes locks the unit selection and the switch from one decimal
// place to none, which is what keeps the size column aligned.
func TestFormatBytes(t *testing.T) {
	cases := []struct {
		bytes int64
		want  string
	}{
		{0, "0 B"},
		{512, "512 B"},
		{1023, "1023 B"},
		{1024, "1.0 KB"},
		{20 * 1024 * 1024, "20 MB"},
		{1024 * 1024 * 1024, "1.0 GB"},
		// Values below 10 keep a decimal place so 5.3 GB does not read as 5 GB.
		{5_690_831_667, "5.3 GB"},
		// At 10 and above the decimal is dropped to keep the column narrow.
		{15 * 1024 * 1024 * 1024, "15 GB"},
	}

	for _, tc := range cases {
		if got := formatBytes(tc.bytes); got != tc.want {
			t.Errorf("formatBytes(%d) = %q, want %q", tc.bytes, got, tc.want)
		}
	}
}

// TestSplitSessionNames checks the parsing of the comma-separated --session
// value, including the spacing people naturally type.
func TestSplitSessionNames(t *testing.T) {
	cases := []struct {
		name  string
		value string
		want  []string
	}{
		{"empty", "", nil},
		{"single", "dev", []string{"dev"}},
		{"multiple", "dev,staging", []string{"dev", "staging"}},
		{"tolerates spaces", " dev , staging ", []string{"dev", "staging"}},
		{"drops empties", "dev,,staging,", []string{"dev", "staging"}},
		{"only separators", ",,", nil},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := splitSessionNames(tc.value)

			if len(got) != len(tc.want) {
				t.Fatalf("splitSessionNames(%q) = %v, want %v", tc.value, got, tc.want)
			}
			for i := range got {
				if got[i] != tc.want[i] {
					t.Fatalf("splitSessionNames(%q) = %v, want %v", tc.value, got, tc.want)
				}
			}
		})
	}
}
