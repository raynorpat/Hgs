package contributions

import "testing"

func TestContributionVerifiedAuthor(t *testing.T) {
	for _, tc := range []struct{ input, want string }{
		{" Alice <ALICE+tag@Example.Invalid> ", "alice+tag@example.invalid"},
		{"bob@example.invalid", "bob@example.invalid"},
		{"Alice", ""}, {"Alice <a@example.invalid>, Bob <b@example.invalid>", ""},
		{"Alice <a@example.invalid> Co-authored-by: b@example.invalid", ""},
	} {
		if got := authorEmail(tc.input); got != tc.want {
			t.Errorf("author parsing: got %q want %q", got, tc.want)
		}
	}
}
