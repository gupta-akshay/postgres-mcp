package config

import "testing"

func TestIsRestricted(t *testing.T) {
	cases := []struct {
		mode string
		want bool
	}{
		{"restricted", true},
		{"", true},         // zero-value is treated as restricted
		{"anything", true}, // any non-"unrestricted" value is restricted
		{"unrestricted", false},
	}

	for _, tc := range cases {
		cfg := Config{AccessMode: tc.mode}
		if got := cfg.IsRestricted(); got != tc.want {
			t.Errorf("AccessMode=%q: IsRestricted() = %v, want %v", tc.mode, got, tc.want)
		}
	}
}
