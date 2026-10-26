//go:build integration

package cmd

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"html"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/gogits/gogs/models"
	"github.com/gogits/gogs/modules/hg"
)

type nativeViewFixture struct {
	app                *viewApp
	node, base, newest hg.NodeID
	revision           int64
}

func buildNativeViewFixture(t *testing.T) *nativeViewFixture {
	t.Helper()
	exe := os.Getenv("GOGS_TEST_HG")
	if exe == "" {
		t.Fatal("GOGS_TEST_HG must name native Mercurial")
	}
	a := newViewAppHg(t, exe)
	s := a.holder.Service()
	create := func(name string, private bool) *models.Repository {
		r, err := models.CreateHgRepository(context.Background(), s.Runner(), s.RepositoryRoot(), models.CreateHgRepositoryOptions{OwnerID: a.alice.Id, Name: name, Private: private})
		if err != nil {
			t.Fatal(err)
		}
		return r
	}
	populated := create("populated", false)
	private := create("private", true)
	runner := s.Runner()
	work := filepath.Join(a.root, "source")
	run := func(args ...string) string {
		t.Helper()
		var out bytes.Buffer
		if err := runner.Run(context.Background(), work, args, nil, &out); err != nil {
			t.Fatalf("hg %v: %v", args, err)
		}
		return strings.TrimSpace(out.String())
	}
	if err := runner.Run(context.Background(), "", []string{"init", work}, nil, nil); err != nil {
		t.Fatal(err)
	}
	write := func(name, body string) {
		t.Helper()
		p := filepath.Join(work, filepath.FromSlash(name))
		os.MkdirAll(filepath.Dir(p), 0700)
		if err := os.WriteFile(p, []byte(body), 0600); err != nil {
			t.Fatal(err)
		}
	}
	for i := 0; i < 200; i++ {
		write(fmt.Sprintf("many/f%03d.txt", i), "line\n")
	}
	write("README.md", "# Mercurial repository\n\n<script>alert(1)</script>\n")
	write("source.txt", "<script>alert(1)</script>\n")
	write("binary.dat", "\x00\x01binary")
	write("large.txt", strings.Repeat("a", hg.MaxRenderedFileBytes+1))
	run("add", "-q")
	run("commit", "-q", "-u", "Preview <preview@example.test>", "-m", "base")
	base := hg.NodeID(run("log", "-r", ".", "-T", "{node}"))
	run("branch", "feature/slash")
	write("feature.txt", "feature\n")
	run("add", "-q")
	run("commit", "-q", "-u", "Preview", "-m", "feature")
	run("update", "-q", "-r", string(base))
	write("main.txt", "main\n")
	run("add", "-q")
	run("commit", "-q", "-u", "Preview", "-m", "main")
	run("merge", "-q", "feature/slash")
	run("commit", "-q", "-u", "Preview", "-m", "merge <script>alert(99)</script>")
	node := hg.NodeID(run("log", "-r", ".", "-T", "{node}"))
	cs, err := runner.Changeset(context.Background(), work, node)
	if err != nil {
		t.Fatal(err)
	}
	run("bookmark", "same")
	run("tag", "-u", "Preview", "same")
	run("update", "-q", "feature/slash")
	write("feature.txt", "newest other head\n")
	run("commit", "-q", "-u", "Preview", "-m", "newest visible other branch")
	newest := hg.NodeID(run("log", "-r", ".", "-T", "{node}"))
	for _, repo := range []*models.Repository{populated, private} {
		storage, _ := models.RepositoryStoragePath(s.RepositoryRoot(), repo.Id)
		run("push", "-q", "--force", "--new-branch", storage)
	}
	return &nativeViewFixture{a, node, base, newest, cs.Revision}
}
func TestMercurialNativeViews(t *testing.T) {
	f := buildNativeViewFixture(t)
	a := f.app
	paths := []string{"/gogs/alice/project", "/gogs/alice/populated", "/gogs/alice/populated/src/" + string(f.node) + "/many", "/gogs/alice/populated/src/" + string(f.node) + "/binary.dat", "/gogs/alice/populated/src/" + string(f.node) + "/large.txt", "/gogs/alice/populated/commit/" + string(f.node) + "?parent=0", "/gogs/alice/populated/commit/" + string(f.node) + "?parent=1"}
	for _, p := range paths {
		start := time.Now()
		w := a.request(t, "GET", p, "", "")
		t.Logf("native page %s latency %s status %d", p, time.Since(start), w.Code)
		if w.Code != 200 {
			t.Fatal(p, w.Code, w.Body.String())
		}
	}
	w := a.request(t, "GET", "/gogs/alice/populated/src/"+string(f.node)+"/source.txt", "", "")
	re := regexp.MustCompile(`href="([^"]+/commits\?cursor=[^"]+)"`)
	m := re.FindStringSubmatch(w.Body.String())
	if m == nil {
		t.Fatal("file history link absent")
	}
	u, _ := url.Parse(html.UnescapeString(m[1]))
	b, err := base64.RawURLEncoding.DecodeString(u.Query().Get("cursor"))
	if err != nil {
		t.Fatal(err)
	}
	var c struct {
		Node        string `json:"node"`
		Rev, Before int64
		Path        string
	}
	if err = json.Unmarshal(b, &c); err != nil {
		t.Fatal(err)
	}
	if c.Node != string(f.node) || c.Rev != f.revision || c.Before != -1 || c.Path != "c291cmNlLnR4dA" {
		t.Fatalf("file history not pinned to viewed changeset: %s", b)
	}
	w = a.request(t, "GET", u.String(), "", "")
	if w.Code != 200 {
		t.Fatal("pinned history", w.Code, w.Body.String())
	}
	w = a.request(t, "GET", "/gogs/alice/populated/commits", "", "")
	if w.Code != 200 || !strings.Contains(w.Body.String(), string(f.newest)) || !strings.Contains(w.Body.String(), "newest visible other branch") {
		t.Fatal("global history omitted newest other head", w.Code, w.Body.String())
	}
	w = a.request(t, "GET", "/gogs/alice/private", "", "")
	if w.Code != 404 {
		t.Fatal("private anonymous", w.Code)
	}
	owner := a.browser(t, "alice")
	w = owner.do(t, "GET", "/gogs/alice/private", nil)
	if w.Code != 200 {
		t.Fatal("private owner", w.Code, w.Body.String())
	}
}

// Opt-in temporary native preview, owned by this test until the stop file exists.
func TestMercurialViewPreview(t *testing.T) {
	destination := os.Getenv("GOGS_VIEW_PREVIEW")
	if destination == "" {
		t.Skip("set GOGS_VIEW_PREVIEW to an absolute preview metadata path")
	}
	f := buildNativeViewFixture(t)
	server := httptest.NewServer(f.app.handler)
	defer server.Close()
	stop := destination + ".stop"
	os.Remove(stop)
	info := map[string]interface{}{"url": server.URL, "login": "alice", "password": "password", "node": f.node, "empty": "/gogs/alice/project", "populated": "/gogs/alice/populated", "tree": "/gogs/alice/populated/src/" + string(f.node) + "/many", "binary": "/gogs/alice/populated/src/" + string(f.node) + "/binary.dat", "merge": "/gogs/alice/populated/commit/" + string(f.node), "private": "/gogs/alice/private", "stop_file": stop}
	b, _ := json.MarshalIndent(info, "", "  ")
	if err := os.WriteFile(destination, b, 0600); err != nil {
		t.Fatal(err)
	}
	t.Log(string(b))
	deadline := time.Now().Add(12 * time.Minute)
	for time.Now().Before(deadline) {
		if _, err := os.Stat(stop); err == nil {
			return
		}
		time.Sleep(100 * time.Millisecond)
	}
}
