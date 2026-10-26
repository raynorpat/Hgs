package hg

import "testing"

func TestHostingTransportPaths(t *testing.T) {
	for _, tc := range []struct {
		path, sub, owner, repo string
		ok                     bool
	}{
		{"/hg/owner/repo", "", "owner", "repo", true},
		{"/gogs/hg/owner/repo/", "/gogs", "owner", "repo", true},
		{"/hg/ow%6eer/repo", "", "owner", "repo", true},
		{"/hg/owner/re%2fpo", "", "", "", false},
		{"/hg//owner/repo", "", "", "", false},
		{"/hg/owner/..", "", "", "", false},
		{"/gogs2/hg/owner/repo", "/gogs", "", "", false},
		{"/hg/owner/repo/extra", "", "", "", false},
		{"/hg/owner/repo", "/gogs", "owner", "repo", true},
		{"/hg/owner/repo", "/hg", "owner", "repo", true},
		{"/g%C3%B6gs/hg/owner/repo", "/gögs", "owner", "repo", true},
	} {
		owner, repo, ok := transportPath(tc.path, tc.sub)
		if ok != tc.ok || owner != tc.owner || repo != tc.repo {
			t.Errorf("%s: %q %q %v", tc.path, owner, repo, ok)
		}
	}
}
