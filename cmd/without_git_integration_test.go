//go:build integration

package cmd

import (
	"bytes"
	"context"
	"fmt"
	"net/http/httptest"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/gogits/gogs/models"
)

func TestHostingWithoutGitOrSSHUtilities(t *testing.T) {
	executable := os.Getenv("GOGS_TEST_HG")
	if executable == "" {
		t.Fatal("GOGS_TEST_HG must name native Mercurial")
	}
	// Keep native runtime DLL directories (including Python on Windows),
	// removing every PATH entry that supplies Git or SSH utilities.
	var keep []string
	for _, directory := range filepath.SplitList(os.Getenv("PATH")) {
		blocked := false
		for _, command := range []string{"git", "ssh", "sshd", "ssh-keygen"} {
			if _, err := exec.LookPath(filepath.Join(directory, command)); err == nil {
				blocked = true
				break
			}
		}
		if !blocked {
			keep = append(keep, directory)
		}
	}
	t.Setenv("PATH", strings.Join(keep, string(os.PathListSeparator)))
	for _, command := range []string{"git", "ssh", "sshd", "ssh-keygen"} {
		if path, err := exec.LookPath(command); err == nil {
			t.Fatalf("%s remains reachable: %s", command, path)
		}
	}
	a := newViewAppHg(t, executable)
	browser := a.browser(t, "alice")
	if w := browser.do(t, "GET", "/gogs/repo/create", nil); w.Code != 200 {
		t.Fatalf("create form: %d", w.Code)
	}
	w := browser.do(t, "POST", "/gogs/repo/create", url.Values{"uid": {fmt.Sprint(a.alice.Id)}, "repo_name": {"native"}, "private": {"true"}})
	if w.Code != 302 {
		t.Fatalf("create: %d %s", w.Code, w.Body)
	}
	repo, err := models.GetRepositoryByName(a.alice.Id, "native")
	if err != nil {
		t.Fatal(err)
	}
	if w := browser.do(t, "GET", "/gogs/alice/native/issues/new", nil); w.Code != 200 {
		t.Fatalf("issue form: %d", w.Code)
	}
	w = browser.do(t, "POST", "/gogs/alice/native/issues/new", url.Values{"title": {"remains open"}, "content": {"retained issue"}})
	if w.Code != 200 {
		t.Fatalf("issue: %d %s", w.Code, w.Body)
	}
	feeds, err := models.GetFeeds(a.alice.Id, 0, false)
	if err != nil {
		t.Fatal(err)
	}
	server := httptest.NewTLSServer(a.handler)
	defer server.Close()
	remote, _ := url.Parse(server.URL + "/gogs/hg/alice/native")
	remote.User = url.UserPassword("alice", "password")
	source := filepath.Join(a.root, "source")
	runner := a.holder.Service().Runner()
	run := func(path string, args ...string) {
		t.Helper()
		var out bytes.Buffer
		if err := runner.Run(context.Background(), path, append([]string{"--config", "http_proxy.host="}, args...), nil, &out); err != nil {
			t.Fatalf("hg %v: %v %s", args, err, out.String())
		}
	}
	run("", "init", source)
	if err := os.WriteFile(filepath.Join(source, "hello.txt"), []byte("native Mercurial\n"), 0600); err != nil {
		t.Fatal(err)
	}
	run(source, "add", "hello.txt")
	run(source, "commit", "-u", "alice", "-m", "Closes #1")
	run(source, "push", "--insecure", remote.String())
	run("", "clone", "--insecure", remote.String(), filepath.Join(a.root, "clone"))
	w = browser.do(t, "GET", "/gogs/alice/native", nil)
	if w.Code != 200 || !strings.Contains(w.Body.String(), "hello.txt") {
		t.Fatalf("browse: %d %s", w.Code, w.Body)
	}
	w = browser.do(t, "GET", "/gogs/alice/native/issues/1", nil)
	if w.Code != 200 || !strings.Contains(w.Body.String(), "remains open") {
		t.Fatalf("issue view: %d %s", w.Code, w.Body)
	}
	issue, err := models.GetIssueByIndex(repo.Id, 1)
	if err != nil || issue.IsClosed {
		t.Fatalf("push changed issue state: %+v %v", issue, err)
	}
	after, err := models.GetFeeds(a.alice.Id, 0, false)
	if err != nil || len(after) != len(feeds) {
		t.Fatalf("push created an activity feed: before=%d after=%d err=%v", len(feeds), len(after), err)
	}
	t.Log("native Mercurial startup, web create, HTTPS push/clone, browse and issues passed with Git and SSH utilities absent from PATH")
}
