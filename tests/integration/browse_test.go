//go:build integration

package integration

import (
	"crypto/tls"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestHostingBrowseMeasurements(t *testing.T) {
	a := newApplication(t, matrix(t)[0])
	remote, opts := a.remote(t, transportHTTP, "owner", "public")
	local := filepath.Join(a.root, "large-tree")
	a.client.must(t, "", append(append([]string{}, opts...), "clone", remote, local)...)
	if err := os.Mkdir(filepath.Join(local, "large"), 0700); err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 200; i++ {
		if err := os.WriteFile(filepath.Join(local, "large", fmt.Sprintf("f%03d.txt", i)), []byte(fmt.Sprintf("file %d\n", i)), 0600); err != nil {
			t.Fatal(err)
		}
	}
	a.client.must(t, local, "add", "large")
	a.client.must(t, local, "commit", "-u", "owner", "-m", "large tree")
	a.client.must(t, local, append(append([]string{}, opts...), "push", remote)...)
	node := a.inspect.stdout(t, local, "log", "-r", "tip", "-T", "{node}")
	client := &http.Client{Transport: &http.Transport{TLSClientConfig: &tls.Config{InsecureSkipVerify: true}}, Timeout: 30 * time.Second}
	for _, page := range []struct{ name, path, contains string }{
		{"home", "/owner/public", "large"},
		{"large directory", "/owner/public/src/" + node + "/large", "f199.txt"},
		{"history", "/owner/public/commits", "large tree"},
		{"diff", "/owner/public/commit/" + node, "f199.txt"},
	} {
		start := time.Now()
		response, err := client.Get("https://" + a.address + page.path)
		if err != nil {
			t.Fatal(err)
		}
		body, err := io.ReadAll(response.Body)
		response.Body.Close()
		if err != nil || response.StatusCode != 200 || !strings.Contains(string(body), page.contains) {
			t.Fatalf("%s page status=%d expected content=%q err=%v", page.name, response.StatusCode, page.contains, err)
		}
		t.Logf("%s: %s, response bytes=%d (200 files; no scalability claim)", page.name, time.Since(start), len(body))
	}
	client.CloseIdleConnections()
}
