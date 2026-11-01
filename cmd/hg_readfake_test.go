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
	if r.URL.Path == "/close" {
		fmt.Fprint(w, "{}")
		return
	}
	body, _ := io.ReadAll(r.Body)
	var q struct{ OperationName string }
	json.Unmarshal(body, &q)
	f.mu.Lock()
	f.ops = append(f.ops, q.OperationName)
	resp, ok := f.handlers[q.OperationName]
	f.mu.Unlock()
	if !ok {
		resp = readError("INTERNAL")
	}
	fmt.Fprint(w, resp)
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
