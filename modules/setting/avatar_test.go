package setting

import "testing"

func TestGravatarConfiguration(t *testing.T) {
	for _, tc := range []struct{ input, want string }{
		{"gravatar", "https://gravatar.com/avatar/"}, {"https://gravatar.com/avatar/", "https://gravatar.com/avatar/"}, {"duoshuo", "https://gravatar.duoshuo.com/avatar/"}, {"https://gravatar.duoshuo.com/avatar/", "https://gravatar.duoshuo.com/avatar/"},
		{"", ""}, {"other", ""}, {"http://gravatar.duoshuo.com/avatar/", ""}, {"//gravatar.com/avatar/", ""}, {"https://user@gravatar.com/avatar/", ""}, {"https://gravatar.com:443/avatar/", ""}, {"https://gravatar.com/avatar/?x=1", ""}, {"https://elsewhere.invalid/avatar/", ""},
	} {
		got, err := ResolveGravatarSource(tc.input)
		if got != tc.want || (err != nil) != (tc.want == "") {
			t.Errorf("source %q => %q, %v", tc.input, got, err)
		}
	}
}
