package version

import "testing"

func TestAtLeast(t *testing.T) {
	for _, tc := range []struct {
		current, minimum string
		want             bool
	}{
		{"0.5.1", "0.5.1", true}, {"0.10.0", "0.9.9", true}, {"1.0.0", "0.9.9", true},
		{"0.5.0", "0.5.1", false}, {"0.4.99", "0.5.0", false}, {"", "0.5.1", false}, {"x.5.1", "0.5.1", false},
	} {
		if got := AtLeast(tc.current, tc.minimum); got != tc.want {
			t.Errorf("AtLeast(%q, %q)=%v", tc.current, tc.minimum, got)
		}
	}
}
