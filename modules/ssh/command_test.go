// Copyright 2026 The Gogs Authors. All rights reserved.
// Use of this source code is governed by a MIT-style
// license that can be found in the LICENSE file.

package ssh

import "testing"

// Remote commands as the SSH server receives them. Both client families
// build the same string; the platform's own quoting is for the local shell
// that starts ssh and is gone by the time the command reaches the server.
var nativeCommands = []struct {
	source, command string
}{
	// Observed: Mercurial 7.2.4 on Windows, recorded through ui.ssh
	// (TestNativeClientCommandShape) and accepted end to end over OpenSSH.
	{"7.2.4 Windows, observed", "hg -R owner/repo serve --stdio"},
	// Observed: 7.2.4 Windows with ui.remotecmd set to a path that needs
	// quoting; sshpeer._serverquote quotes it for sh, as on every platform.
	{"7.2.4 Windows remotecmd, observed", `'C:\Program Files\Mercurial\hg.exe' -R owner/repo serve --stdio`},
	// Source-derived: 7.2.4 sshpeer._makeconnection builds
	// "%s -R %s serve --stdio" from _serverquote(remotecmd) and
	// _serverquote(path) on every platform; posix.shellquote only quotes it
	// for the local sh.
	{"7.2.4 Unix, source-derived", "hg -R owner/repo serve --stdio"},
	{"7.2.4 Unix remotecmd, source-derived", "/usr/local/bin/hg -R owner/repo serve --stdio"},
	{"7.2.4 Unix quoted remotecmd, source-derived", "'/opt/my hg/bin/hg' -R owner/repo serve --stdio"},
	// _serverquote's quoted form of the path, accepted although valid owner
	// and repository names never need it.
	{"7.2.4 quoted path, source-derived", "hg -R 'owner/repo' serve --stdio"},
	// Source-derived: 2.0.2 sshrepo.validate_repo builds
	// '%s %s "%s -R %s serve --stdio"' with no quoting of remotecmd or path;
	// the double quotes belong to the local shell on Unix and Windows alike.
	{"2.0.2 Unix and Windows, source-derived", "hg -R owner/repo serve --stdio"},
	{"names with dots, dashes, underscores", "hg -R my.org-1/re_po.x serve --stdio"},
}

var nativeTargets = map[string]string{
	"hg -R my.org-1/re_po.x serve --stdio": "my.org-1/re_po.x",
}

func TestParseNativeHGCommand(t *testing.T) {
	for _, tc := range nativeCommands {
		owner, repo, err := ParseCommand(tc.command)
		if err != nil {
			t.Errorf("%s: ParseCommand(%q): %v", tc.source, tc.command, err)
			continue
		}
		want := nativeTargets[tc.command]
		if want == "" {
			want = "owner/repo"
		}
		if owner+"/"+repo != want {
			t.Errorf("%s: ParseCommand(%q) = %q, %q", tc.source, tc.command, owner, repo)
		}
	}

	if _, _, err := ParseCommand("hg -R owner/repo serve --stdio; whoami"); err == nil {
		t.Fatal("SSH command parser accepted trailing execution")
	}
	for _, cmd := range rejectedCommands {
		if owner, repo, err := ParseCommand(cmd); err == nil {
			t.Errorf("ParseCommand(%q) accepted %q/%q", cmd, owner, repo)
		}
	}
}

var rejectedCommands = []string{
	"",
	"hg",
	"serve --stdio",
	"-R owner/repo serve --stdio",
	" hg -R owner/repo serve --stdio",
	"hg -R owner/repo serve --stdio ",
	"hg  -R owner/repo serve --stdio",
	"hg\t-R owner/repo serve --stdio",
	"hg -R owner/repo serve --stdio --hidden",
	"hg --repository owner/repo serve --stdio",
	"hg -Rowner/repo serve --stdio",
	"hg serve --stdio -R owner/repo",
	"hg -R owner/repo --stdio serve",
	"hg -R owner/repo serve",
	"hg -R owner/repo serve --stdio --config hooks.pretxnopen=true",
	"hg --config hooks.pretxnopen=true -R owner/repo serve --stdio",
	"hg -R owner/repo --config=ui.foo=1 serve --stdio",
	"hg --cwd /tmp -R owner/repo serve --stdio",
	"hg -R owner/repo serve --cmdserver pipe",
	"hg -R owner/repo serve --stdio --debugger",
	"hg --debugger -R owner/repo serve --stdio",
	"hg -R owner/repo -R other/repo serve --stdio",
	"hg -R owner/repo other/repo serve --stdio",
	"hg -R owner/repo serve --stdio owner/repo",
	"hg -R owner serve --stdio",
	"hg -R owner/repo/extra serve --stdio",
	"hg -R /owner/repo serve --stdio",
	"hg -R owner/repo/ serve --stdio",
	"hg -R owner//repo serve --stdio",
	"hg -R /etc serve --stdio",
	"hg -R ../owner/repo serve --stdio",
	"hg -R owner/.. serve --stdio",
	"hg -R ./repo serve --stdio",
	"hg -R owner/. serve --stdio",
	`hg -R owner\repo serve --stdio`,
	`hg -R C:/owner/repo serve --stdio`,
	`hg -R C:owner/repo serve --stdio`,
	`hg -R \\server\share serve --stdio`,
	"hg -R owner/repo.hg serve --stdio\x00",
	"hg -R owner/re\x00po serve --stdio",
	"hg -R owner/repo serve --stdio\n",
	"hg -R owner/repo serve --stdio\r\nwhoami",
	"hg -R owner/repo serve --stdio\nwhoami",
	"hg -R owner/repo serve --stdio && whoami",
	"hg -R owner/repo serve --stdio || whoami",
	"hg -R owner/repo serve --stdio | whoami",
	"hg -R owner/repo serve --stdio & whoami",
	"hg -R owner/repo serve --stdio > /tmp/x",
	"hg -R owner/repo serve --stdio < /etc/passwd",
	"hg -R `whoami`/repo serve --stdio",
	"hg -R $(whoami)/repo serve --stdio",
	"hg -R $HOME/repo serve --stdio",
	"hg -R owner/repo* serve --stdio",
	"hg -R owner/re%PATH%po serve --stdio",
	"hg -R owner/repo^ serve --stdio",
	"whoami; hg -R owner/repo serve --stdio",
	"(hg -R owner/repo serve --stdio)",
	`hg -R "owner/repo" serve --stdio`,
	`"hg" -R owner/repo serve --stdio`,
	"hg -R 'owner/repo serve --stdio",
	"hg -R owner/repo' serve --stdio",
	"hg -R 'owner'/repo serve --stdio",
	"hg -R 'own'\\''er/repo' serve --stdio",
	"hg -R '' serve --stdio",
	"hg '-R' owner/repo serve --stdio",
	"hg -R owner/repo 'serve' --stdio",
	"hg -R owner/repo serve '--stdio'",
	"'' -R owner/repo serve --stdio",
	"'hg -R owner/repo serve --stdio'",
	"hg; -R owner/repo serve --stdio",
	"h\x7fg -R owner/repo serve --stdio",
	"hg -R owner/rep\u00e9 serve --stdio",
	"git-upload-pack 'owner/repo'",
	"hg -R owner/repo init",
}
