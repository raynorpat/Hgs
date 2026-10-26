//go:build integration

package integration

import (
	"bytes"
	"context"
	"crypto/sha256"
	"crypto/tls"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/cookiejar"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/gogits/gogs/models"
	"github.com/gogits/gogs/modules/setting"
	"golang.org/x/crypto/ssh"
)

// This is an opt-in destructive-to-its-own-fixtures acceptance test. The caller
// must provide a freshly extracted package; no user storage is ever opened.
func TestPortablePackageRecovery(t *testing.T) {
	if os.Getenv("GOGS_TEST_PACKAGE_DIRECT") != "1" {
		t.Skip("requires an extracted package and direct mode")
	}
	before := operatorConfigSnapshot(t)
	defer func() {
		after := operatorConfigSnapshot(t)
		if fmt.Sprint(before) != fmt.Sprint(after) {
			t.Error("operator-wide Git/Mercurial settings changed")
		}
	}()
	for _, dir := range filepath.SplitList(os.Getenv("GOGS_TEST_SERVER_PATH")) {
		for _, name := range []string{"git", "sshd", "ssh-keygen"} {
			if runtime.GOOS == "windows" {
				name += ".exe"
			}
			if _, err := os.Stat(filepath.Join(dir, name)); !os.IsNotExist(err) {
				t.Fatalf("server PATH contains forbidden utility %s", filepath.Join(dir, name))
			}
		}
	}
	a := newApplication(t, matrix(t)[0])
	want, err := filepath.Abs(os.Getenv("GOGS_TEST_BINARY"))
	if err != nil {
		t.Fatal(err)
	}
	if a.binary != want {
		t.Fatalf("package smoke copied executable: running %q, require actual extracted %q", a.binary, want)
	}
	t.Logf("actual extracted executable=%s; assets=%s; different CWD=%s; server PATH=%s", a.binary, filepath.Dir(a.binary), a.root, os.Getenv("GOGS_TEST_SERVER_PATH"))
	if _, err := os.Stat(filepath.Join(a.root, "public")); !os.IsNotExist(err) {
		t.Fatal("startup created CWD-relative public/avatar cache")
	}
	image := readPackageFile(t, filepath.Join(filepath.Dir(a.binary), "public/img/avatar_default.jpg"))
	cache := filepath.Join(filepath.Dir(a.binary), "public/img/avatar/package-canary.image")
	writePackageFile(t, cache, image)
	httpClient := &http.Client{Transport: &http.Transport{TLSClientConfig: &tls.Config{InsecureSkipVerify: true}, DisableKeepAlives: true}, Timeout: 30 * time.Second}
	defer httpClient.CloseIdleConnections()
	page := func(path, content string) {
		t.Helper()
		resp, err := httpClient.Get("https://" + a.address + path)
		if err != nil {
			t.Fatal(err)
		}
		body, err := io.ReadAll(resp.Body)
		resp.Body.Close()
		if err != nil || resp.StatusCode != 200 || len(body) == 0 || !bytes.Contains(body, []byte(content)) {
			t.Fatalf("page %s: status=%d bytes=%d missing=%q err=%v", path, resp.StatusCode, len(body), content, err)
		}
	}
	page("/avatar/package-canary", "")
	jar, err := cookiejar.New(nil)
	if err != nil {
		t.Fatal(err)
	}
	browser := &http.Client{Transport: httpClient.Transport, Jar: jar, Timeout: 30 * time.Second, CheckRedirect: func(r *http.Request, via []*http.Request) error { return http.ErrUseLastResponse }}
	token := ""
	do := func(method, path string, form url.Values) (int, []byte) {
		t.Helper()
		body := ""
		if form != nil {
			body = form.Encode()
		}
		req, err := http.NewRequest(method, "https://"+a.address+path, strings.NewReader(body))
		if err != nil {
			t.Fatal(err)
		}
		if method == "POST" {
			req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
			req.Header.Set("X-Csrf-Token", token)
		}
		resp, err := browser.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		data, err := io.ReadAll(resp.Body)
		resp.Body.Close()
		if err != nil {
			t.Fatal(err)
		}
		if m := regexp.MustCompile(`<meta name="_csrf" content="([^"]+)"`).FindSubmatch(data); m != nil {
			token = string(m[1])
		}
		return resp.StatusCode, data
	}
	if status, _ := do("GET", "/user/login", nil); status != 200 {
		t.Fatal("login form", status)
	}
	if status, body := do("POST", "/user/login", url.Values{"uname": {"owner"}, "password": {"password"}}); status != 302 {
		t.Fatalf("native-process web login=%d %s", status, body)
	}
	if status, _ := do("GET", "/owner/public/issues/new", nil); status != 200 {
		t.Fatal("issue form", status)
	}
	status, body := do("POST", "/owner/public/issues/new", url.Values{"title": {"portable recovery issue"}, "content": {"cold snapshot content"}})
	var result struct {
		OK bool `json:"ok"`
	}
	if err := json.Unmarshal(body, &result); err != nil || status != 200 || !result.OK {
		t.Fatalf("actual web issue creation=%d %s %v", status, body, err)
	}
	issue, err := models.GetIssueByIndex(a.repos["public"].Id, 1)
	if err != nil || issue.Name != "portable recovery issue" {
		t.Fatalf("web-created issue=%+v %v", issue, err)
	}
	t.Log("actual HTTPS browser login and issue creation/read persisted")
	persistent := map[string][]byte{
		"data/attachments/retained-upload.txt":   []byte("retained issue upload canary"),
		"data/avatars/retained-avatar.png":       image,
		"public/img/avatar/package-canary.image": image,
	}
	for name, body := range persistent {
		writePackageFile(t, filepath.Join(filepath.Dir(a.binary), name), body)
	}
	writePackageFile(t, filepath.Join(a.root, "sessions", "retained-session"), []byte("persistent session canary"))
	hostBytes := readPackageFile(t, filepath.Join(a.root, "hostkey"))
	signer, err := ssh.ParsePrivateKey(hostBytes)
	if err != nil {
		t.Fatal(err)
	}
	fingerprint := ssh.FingerprintSHA256(signer.PublicKey())
	t.Logf("persistent SSH fingerprint=%s", fingerprint)
	assertPackageKeyPrivate(t, filepath.Join(a.root, "hostkey"))
	nodes := map[string]string{}
	exercise := func(phase string) {
		t.Helper()
		for _, transport := range []string{transportHTTP, transportSSH} {
			remote, opts := a.remote(t, transport, "writer", "public")
			local := filepath.Join(a.root, phase+"-"+transport)
			a.client.must(t, "", append(append([]string{}, opts...), "clone", remote, local)...)
			writePackageFile(t, filepath.Join(local, phase+"-"+transport+".txt"), []byte(phase+transport))
			a.client.must(t, local, "add")
			a.client.must(t, local, "commit", "-u", "writer", "-m", phase+transport)
			a.client.must(t, local, append(append([]string{}, opts...), "push", remote)...)
			node := a.client.stdout(t, local, "log", "-r", "tip", "-T", "{node}")
			nodes[phase+"/"+transport] = node
			reader, ropts := a.remote(t, transport, "reader", "public")
			pull := filepath.Join(a.root, phase+"-pull-"+transport)
			a.client.must(t, "", append(append([]string{}, ropts...), "clone", reader, pull)...)
			a.client.must(t, pull, append(append([]string{}, ropts...), "pull", reader)...)
			if got := a.client.stdout(t, pull, "log", "-r", "tip", "-T", "{node}"); got != node {
				t.Fatalf("%s pull node=%s want=%s", transport, got, node)
			}
			writePackageFile(t, filepath.Join(pull, "denied.txt"), []byte("reader"))
			a.client.must(t, pull, "add", "denied.txt")
			a.client.must(t, pull, "commit", "-u", "reader", "-m", "denied")
			store := filepath.Join(a.repoRoot, fmt.Sprint(a.repos["public"].Id))
			snapshot := snapshotRepository(t, a.inspect, store)
			if out, err := a.client.run(context.Background(), pull, append(append([]string{}, ropts...), "push", reader)...); err == nil {
				t.Fatalf("reader write accepted: %s", out)
			}
			if diff := snapshot.diff(snapshotRepository(t, a.inspect, store)); diff != "" {
				t.Fatal("reader denial mutated store: " + diff)
			}
			private, popts := a.remote(t, transport, "stranger", "private")
			if out, err := a.client.run(context.Background(), "", append(append([]string{}, popts...), "clone", private, filepath.Join(a.root, phase+"-denied-"+transport))...); err == nil {
				t.Fatalf("stranger private read accepted: %s", out)
			}
			t.Logf("%s %s: native clone/push/pull, reader write denial, stranger private denial; node=%s", phase, transport, node)
		}
		page("/owner/public", ".txt")
		page("/owner/public/commits", phase)
		page("/owner/public/issues/1", "cold snapshot content")
	}
	exercise("before")
	a.stop()
	if !a.lastCleanStop {
		t.Fatal("restart requires natural clean exit")
	}
	a.start(t)
	page("/owner/public/issues/1", "portable recovery issue")
	for _, node := range nodes {
		a.inspect.must(t, filepath.Join(a.repoRoot, fmt.Sprint(a.repos["public"].Id)), "log", "-r", node, "-T", "{node}")
	}
	// All test requests completed; stop admissions/drain/close in runWeb. Both
	// the actual app DB and the provisioning DB must be closed before copying.
	a.stop()
	if !a.lastCleanStop {
		t.Fatal("cold backup requires natural clean exit")
	}
	if err := models.CloseEngine(); err != nil {
		t.Fatal(err)
	}
	backup := filepath.Join(t.TempDir(), "cold snapshot")
	state := filepath.Join(backup, "state")
	if err := os.MkdirAll(state, 0700); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"application.db", "application.db-wal", "application.db-shm", "application.db-journal", "hostkey", "app.ini", "frontend.pem", "tls-key.pem"} {
		src := filepath.Join(a.root, name)
		body, err := os.ReadFile(src)
		if os.IsNotExist(err) && strings.HasPrefix(name, "application.db-") {
			t.Logf("cold SQLite sidecar absent: %s", name)
			continue
		}
		if err != nil {
			t.Fatal(err)
		}
		writePackageFile(t, filepath.Join(state, name), body)
		t.Logf("cold snapshot file=%s bytes=%d", name, len(body))
	}
	for _, name := range []string{"repos", "sessions"} {
		if err := os.CopyFS(filepath.Join(state, name), os.DirFS(filepath.Join(a.root, name))); err != nil {
			t.Fatal(err)
		}
	}
	pkg := filepath.Dir(a.binary)
	if err := os.CopyFS(filepath.Join(backup, "package"), os.DirFS(pkg)); err != nil {
		t.Fatal(err)
	}
	restored := filepath.Join(t.TempDir(), "restored")
	if err := os.CopyFS(restored, os.DirFS(backup)); err != nil {
		t.Fatal(err)
	}
	oldRoot := a.root
	a.root = filepath.Join(restored, "state")
	restoredPackage := filepath.Join(restored, "package café")
	if err := os.Rename(filepath.Join(restored, "package"), restoredPackage); err != nil {
		t.Fatal(err)
	}
	a.binary = filepath.Join(restoredPackage, filepath.Base(a.binary))
	a.repoRoot = filepath.Join(a.root, "repos")
	a.config = filepath.Join(a.root, "app.ini")
	a.cacerts = filepath.Join(a.root, "frontend.pem")
	config := string(readPackageFile(t, a.config))
	config = strings.ReplaceAll(config, filepath.ToSlash(oldRoot), filepath.ToSlash(a.root))
	config = strings.ReplaceAll(config, oldRoot, a.root)
	config = strings.ReplaceAll(config, filepath.ToSlash(pkg), filepath.ToSlash(filepath.Dir(a.binary)))
	config = strings.ReplaceAll(config, pkg, filepath.Dir(a.binary))
	writePackageFile(t, a.config, []byte(config))
	// Disable the original state paths to catch accidental fallback on restore.
	if err := os.Rename(filepath.Join(oldRoot, "application.db"), filepath.Join(oldRoot, "original-db-disabled")); err != nil {
		t.Fatal(err)
	}
	if err := os.Rename(filepath.Join(oldRoot, "repos"), filepath.Join(oldRoot, "original-repos-disabled")); err != nil {
		t.Fatal(err)
	}
	models.DbCfg.Path = filepath.ToSlash(filepath.Join(a.root, "application.db"))
	setting.RepoRootPath = a.repoRoot
	if err := models.NewEngine(); err != nil {
		t.Fatal(err)
	}
	gotIssue, err := models.GetIssueByIndex(a.repos["public"].Id, 1)
	if err != nil || gotIssue.Name != issue.Name || gotIssue.Content != issue.Content || gotIssue.PosterId != issue.PosterId {
		t.Fatalf("restored issue mismatch: %+v %v", gotIssue, err)
	}
	for _, role := range []string{"owner", "writer", "reader", "stranger"} {
		user, err := models.GetUserByName(role)
		if err != nil || user.Id != a.users[role].Id {
			t.Fatalf("restored account membership %s: %v", role, err)
		}
		access, err := models.RepositoryAccessFor(user.Id, a.repos["private"].Id)
		if err != nil || access.Read != (role != "stranger") || (access.Write || access.Admin) != (role == "owner" || role == "writer") {
			t.Fatalf("restored %s access=%+v err=%v", role, access, err)
		}
	}
	if !bytes.Equal(hostBytes, readPackageFile(t, filepath.Join(a.root, "hostkey"))) {
		t.Fatal("restore changed host-key bytes")
	}
	for name, body := range persistent {
		if !bytes.Equal(body, readPackageFile(t, filepath.Join(filepath.Dir(a.binary), name))) {
			t.Fatal("persistent file lost: " + name)
		}
	}
	if string(readPackageFile(t, filepath.Join(a.root, "sessions/retained-session"))) != "persistent session canary" {
		t.Fatal("persistent session file lost")
	}
	protectPackageKey(t, filepath.Join(a.root, "hostkey"))
	a.start(t)
	assertPackageKeyPrivate(t, filepath.Join(a.root, "hostkey"))
	for _, repo := range a.repos {
		a.inspect.must(t, filepath.Join(a.repoRoot, fmt.Sprint(repo.Id)), "verify", "-q")
	}
	for _, node := range nodes {
		a.inspect.must(t, filepath.Join(a.repoRoot, fmt.Sprint(a.repos["public"].Id)), "log", "-r", node, "-T", "{node}")
	}
	page("/owner/public/issues/1", "cold snapshot content")
	page("/avatar/package-canary", "")
	exercise("restored")
	t.Logf("restore verified: path=%s; original DB/repo paths disabled; issues/accounts/access/files/native nodes/host fingerprint %s", restored, fingerprint)
	a.stop()
	if !a.lastCleanStop {
		t.Fatal("restored app failed clean shutdown")
	}
}

func readPackageFile(t *testing.T, p string) []byte {
	t.Helper()
	b, err := os.ReadFile(p)
	if err != nil {
		t.Fatal(err)
	}
	return b
}
func writePackageFile(t *testing.T, p string, b []byte) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(p), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(p, b, 0600); err != nil {
		t.Fatal(err)
	}
}

func operatorConfigSnapshot(t *testing.T) map[string]string {
	t.Helper()
	home, err := os.UserHomeDir()
	if err != nil {
		t.Fatal(err)
	}
	result := map[string]string{}
	for _, p := range []string{filepath.Join(home, ".gitconfig"), filepath.Join(home, ".hgrc"), filepath.Join(home, "Mercurial.ini"), filepath.Join(home, ".config/git/config"), filepath.Join(home, ".config/hg/hgrc")} {
		b, err := os.ReadFile(p)
		if os.IsNotExist(err) {
			result[p] = "absent"
			continue
		}
		if err != nil {
			t.Fatal(err)
		}
		result[p] = fmt.Sprintf("%x", sha256.Sum256(b))
	}
	return result
}
