package cmd

import (
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"testing"

	"github.com/gogits/gogs/modules/hg"
	"github.com/gogits/gogs/modules/setting"
)

// readFake answers read-service requests with canned JSON by operation name.
// An operation without a handler is an INTERNAL error.
type readFake struct {
	mu       sync.Mutex
	handlers map[string]string
	ops      []string

	// manifest lists the repository's files as "hg manifest -v" does, and
	// answers Tree and /raw. content is what /raw serves for a listed file.
	manifest, content string
	// raw changes /raw: "large" streams endlessly-big content, "fail" answers
	// INTERNAL before any byte if content is empty, else cuts the body short.
	raw string
	// diff is what /diff serves.
	diff string
}

// fakeReadAPI returns a client for a fake read service serving handlers. It
// expects repositories under setting.RepoRootPath.
func fakeReadAPI(t *testing.T, handlers map[string]string) *hg.ReadClient {
	c, _ := newFakeReadAPI(t, handlers)
	return c
}

// newFakeReadAPI is fakeReadAPI that also returns the fake, to change handlers
// and count requests.
func newFakeReadAPI(t *testing.T, handlers map[string]string) (*hg.ReadClient, *readFake) {
	t.Helper()
	f := &readFake{handlers: map[string]string{}}
	f.set(handlers)
	srv := httptest.NewServer(http.HandlerFunc(f.serve))
	t.Cleanup(srv.Close)
	if err := os.MkdirAll(setting.RepoRootPath, 0o755); err != nil {
		t.Fatal(err)
	}
	root, err := filepath.EvalSymlinks(setting.RepoRootPath)
	if err != nil {
		t.Fatal(err)
	}
	c := hg.NewReadClient(root)
	c.SetEndpoint(strings.TrimPrefix(srv.URL, "http://"), "")
	return c, f
}

func (f *readFake) serve(w http.ResponseWriter, r *http.Request) {
	if r.URL.Path == "/raw" {
		f.serveRaw(w, r)
		return
	}
	if r.URL.Path == "/diff" {
		f.mu.Lock()
		f.ops = append(f.ops, "diff")
		diff := f.diff
		f.mu.Unlock()
		io.WriteString(w, diff)
		return
	}
	body, _ := io.ReadAll(r.Body)
	var q struct {
		OperationName string
		Variables     struct{ Path string }
	}
	json.Unmarshal(body, &q)
	f.mu.Lock()
	f.ops = append(f.ops, q.OperationName)
	resp, ok := f.handlers[q.OperationName]
	f.mu.Unlock()
	if !ok && q.OperationName == "Tree" {
		resp, ok = f.treeAnswer(q.Variables.Path)
	}
	if !ok {
		resp = readError("INTERNAL")
	}
	fmt.Fprint(w, resp)
}

// setManifest sets the files the fake lists and serves.
func (f *readFake) setManifest(manifest string) {
	f.mu.Lock()
	f.manifest = manifest
	f.mu.Unlock()
}

// setContent sets what /raw serves for any listed file.
func (f *readFake) setContent(content string) {
	f.mu.Lock()
	f.content = content
	f.mu.Unlock()
}

// setDiff sets what /diff serves.
func (f *readFake) setDiff(diff string) {
	f.mu.Lock()
	f.diff = diff
	f.mu.Unlock()
}

// setRaw sets the /raw mode; see the raw field.
func (f *readFake) setRaw(mode string) {
	f.mu.Lock()
	f.raw = mode
	f.mu.Unlock()
}

// treeAnswer is the Tree answer for path from the manifest: a directory's
// children, or the entry of a file. NOT_FOUND if neither exists.
func (f *readFake) treeAnswer(path string) (string, bool) {
	f.mu.Lock()
	manifest := f.manifest
	f.mu.Unlock()
	prefix := path
	if path != "" {
		prefix += "/"
	}
	entry := func(name string, dir, exec, link bool) string {
		return fmt.Sprintf(`{"nameBase64":%q,"directory":%t,"executable":%t,"symlink":%t}`, base64.StdEncoding.EncodeToString([]byte(name)), dir, exec, link)
	}
	byName, names := map[string]string{}, []string{}
	for _, line := range strings.Split(manifest, "\n") {
		if len(line) < 7 {
			continue
		}
		flag, name := line[:6], line[6:]
		if path != "" && name == path {
			e := entry(name[strings.LastIndexByte(name, '/')+1:], false, flag == "755 * ", flag == "644 @ ")
			return `{"data":{"repo":{"tree":{"directory":false,"entries":[` + e + `]}}}}`, true
		}
		if !strings.HasPrefix(name, prefix) {
			continue
		}
		child, _, dir := strings.Cut(name[len(prefix):], "/")
		if _, seen := byName[child]; !seen {
			byName[child] = entry(child, dir, !dir && flag == "755 * ", !dir && flag == "644 @ ")
			names = append(names, child)
		}
	}
	if path != "" && len(names) == 0 {
		return readError("NOT_FOUND"), true
	}
	sort.Strings(names)
	entries := []string{}
	for _, n := range names {
		entries = append(entries, byName[n])
	}
	return `{"data":{"repo":{"tree":{"directory":true,"entries":[` + strings.Join(entries, ",") + `]}}}}`, true
}

// serveRaw answers /raw for a file in the manifest.
func (f *readFake) serveRaw(w http.ResponseWriter, r *http.Request) {
	f.mu.Lock()
	f.ops = append(f.ops, "raw")
	manifest, content, mode := f.manifest, f.content, f.raw
	f.mu.Unlock()
	listed := false
	for _, line := range strings.Split(manifest, "\n") {
		listed = listed || (len(line) > 6 && line[6:] == r.URL.Query().Get("path"))
	}
	switch {
	case !listed:
		w.WriteHeader(404)
		fmt.Fprint(w, readError("NOT_FOUND"))
	case mode == "large":
		chunk := strings.Repeat("x", 32768)
		for i := 0; i < 4096; i++ {
			if _, err := io.WriteString(w, chunk); err != nil {
				return
			}
		}
	case mode == "fail" && content == "":
		w.WriteHeader(500)
		fmt.Fprint(w, readError("INTERNAL"))
	case mode == "fail":
		// Promise more than is sent, as a service that died mid-body does.
		w.Header().Set("Content-Length", fmt.Sprint(len(content)+1))
		io.WriteString(w, content)
	default:
		io.WriteString(w, content)
	}
}

// set adds or replaces handlers.
func (f *readFake) set(handlers map[string]string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	for k, v := range handlers {
		f.handlers[k] = v
	}
}

// count returns how many requests named one of operations came in.
func (f *readFake) count(operations ...string) int {
	f.mu.Lock()
	defer f.mu.Unlock()
	n := 0
	for _, op := range f.ops {
		for _, want := range operations {
			if op == want {
				n++
			}
		}
	}
	return n
}

// total returns how many requests came in.
func (f *readFake) total() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return len(f.ops)
}

func readError(code string) string {
	return `{"errors":[{"message":"fake","extensions":{"code":"` + code + `"}}]}`
}

// viewRefs is the Refs answer for refs.
func viewRefs(refs ...hg.Reference) string {
	type gql struct {
		Kind, Name, NameBase64, Node string
		Revision                     int64
		Closed                       bool
	}
	out := []gql{}
	for _, ref := range refs {
		out = append(out, gql{strings.ToUpper(string(ref.Kind)), ref.Name, base64.StdEncoding.EncodeToString([]byte(ref.Name)), string(ref.Node), ref.Revision, ref.Closed})
	}
	b, _ := json.Marshal(map[string]interface{}{"data": map[string]interface{}{"repo": map[string]interface{}{"refs": out}}})
	return string(b)
}

// viewChangeset is the Changeset answer for a root changeset.
func viewChangeset(node string, revision int, comments string) string {
	return fmt.Sprintf(`{"data":{"repo":{"changeset":{"id":%q,"revision":%d,"branch":"default","user":"Author <a@example.test>","comments":%q,"time":{"time":1,"tz":0},"parents":[]}}}}`, node, revision, comments)
}

// viewServed is the Served answer for a served changeset.
func viewServed(node string) string {
	return fmt.Sprintf(`{"data":{"repo":{"changeset":{"id":%q}}}}`, node)
}

// viewHistory is the History answer for a page of changesets from revision
// high down to low, with revision text "revision N".
func viewHistory(snapshot string, snapshotRevision, nextBefore int, hasMore bool, high, low int) string {
	entries := []string{}
	for rev := high; rev >= low; rev-- {
		entries = append(entries, fmt.Sprintf(`{"id":%q,"revision":%d,"branch":"default","user":"Author <a@example.test>","comments":"revision %d","time":{"time":1,"tz":0},"parents":[]}`, snapshot, rev, rev))
	}
	return fmt.Sprintf(`{"data":{"repo":{"history":{"snapshot":%q,"snapshotRevision":%d,"nextBefore":%d,"hasMore":%t,"entries":[%s]}}}}`, snapshot, snapshotRevision, nextBefore, hasMore, strings.Join(entries, ","))
}
