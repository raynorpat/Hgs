//go:build integration

package cmd

import (
	"bytes"
	"context"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"testing"

	"github.com/gogits/gogs/models"
)

// Native Mercurial withholds URL credentials until challenged. Public reads
// must remain anonymous while a write negotiates credentials before any worker.
func TestHostingPublicPushAuthentication(t *testing.T) {
	exe := os.Getenv("GOGS_TEST_HG")
	if exe == "" {
		t.Fatal("GOGS_TEST_HG must name native Mercurial")
	}
	a := newViewAppHg(t, exe)
	server := httptest.NewTLSServer(a.handler)
	defer server.Close()
	remote, _ := url.Parse(server.URL + "/gogs/hg/alice/project")
	run := func(t *testing.T, path string, args ...string) string {
		t.Helper()
		var out bytes.Buffer
		if err := a.holder.Service().Runner().Run(context.Background(), path, append([]string{"--config", "http_proxy.host="}, args...), nil, &out); err != nil {
			t.Fatalf("native command %s: %v %s", args[0], err, out.String())
		}
		return out.String()
	}
	source := filepath.Join(a.root, "public-source")
	run(t, "", "clone", "--insecure", remote.String(), source)
	token := &models.AccessToken{Uid: a.alice.Id, Name: "native"}
	if err := models.NewAccessToken(token); err != nil {
		t.Fatal(err)
	}
	for i, credentials := range []*url.Userinfo{url.UserPassword("alice", "password"), url.UserPassword(token.Sha1, "x")} {
		t.Run([]string{"password", "token"}[i], func(t *testing.T) {
			if err := os.WriteFile(filepath.Join(source, "data"), []byte{byte('a' + i)}, 0600); err != nil {
				t.Fatal(err)
			}
			run(t, source, "add", "data")
			run(t, source, "commit", "-u", "alice", "-m", "public push")
			remote.User = credentials
			run(t, source, "push", "--insecure", remote.String())
		})
	}
	remote.User = nil
	clone := filepath.Join(a.root, "anonymous-clone")
	run(t, "", "clone", "--insecure", remote.String(), clone)
	if got := run(t, clone, "log", "-T", "{rev} "); got != "1 0 " {
		t.Fatalf("anonymous clone revisions: %q", got)
	}
}
