//go:build integration

package integration

import (
	"context"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// Start the real application with unavailable dependencies and disposable config.
// No model engine or transport is seeded by this fixture.
func maintenanceProcess(t *testing.T) (string, string) {
	t.Helper()
	root := t.TempDir()
	assets, _ := filepath.Abs("../..")
	binary := filepath.Join(root, filepath.Base(gogsExe))
	b, err := os.ReadFile(gogsExe)
	if err != nil {
		t.Fatal(err)
	}
	if err = os.WriteFile(binary, b, 0700); err != nil {
		t.Fatal(err)
	}
	if err = os.CopyFS(filepath.Join(root, "conf"), os.DirFS(filepath.Join(assets, "conf"))); err != nil {
		t.Fatal(err)
	}
	address, ssh := freeAddress(t), freeAddress(t)
	_, port, _ := net.SplitHostPort(address)
	config := fmt.Sprintf("RUN_USER = deliberately-not-current-user\n[server]\nROOT_URL = http://%s/code/\nHTTP_ADDR = 127.0.0.1\nHTTP_PORT = %s\nSTATIC_ROOT_PATH = %s\nSSH_ADDRESS = %s\nMAINTENANCE_MODE = true\nMAINTENANCE_MESSAGE = Scheduled maintenance <safe>\n[security]\nINSTALL_LOCK = true\n[database]\nDB_TYPE = mysql\nHOST = 127.0.0.1:1\nNAME = unavailable\n[mailer]\nENABLED = true\nHOST = 127.0.0.1:1\n[session]\nPROVIDER = mysql\nPROVIDER_CONFIG = invalid-unavailable-session\n[mercurial]\nEXECUTABLE = nonexistent-hg-executable\n[repository]\nROOT = %s\n", address, port, filepath.ToSlash(assets), ssh, filepath.ToSlash(filepath.Join(root, "must-not-exist")))
	conf := filepath.Join(root, "maintenance.ini")
	if err = os.WriteFile(conf, []byte(config), 0600); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	command := exec.CommandContext(ctx, binary, "web", "--config", conf)
	command.Dir = root
	logFile, err := os.Create(filepath.Join(root, "startup.log"))
	if err != nil {
		t.Fatal(err)
	}
	command.Stdout, command.Stderr = logFile, logFile
	if err = command.Start(); err != nil {
		cancel()
		logFile.Close()
		t.Fatal(err)
	}
	done := make(chan error, 1)
	go func() { done <- command.Wait() }()
	t.Cleanup(func() { cancel(); <-done; logFile.Close() })
	client := http.Client{Timeout: time.Second}
	deadline := time.Now().Add(15 * time.Second)
	var lastResult string
	for {
		res, e := client.Get("http://" + address + "/code/")
		lastResult = fmt.Sprint(e)
		if e == nil {
			lastResult = res.Status
			res.Body.Close()
			if res.StatusCode == 503 {
				break
			}
		}
		if time.Now().After(deadline) {
			b, _ := os.ReadFile(filepath.Join(root, "startup.log"))
			t.Fatalf("maintenance failed to start (%s): %s", lastResult, b)
		}
		time.Sleep(40 * time.Millisecond)
	}
	if _, err = os.Stat(filepath.Join(root, "must-not-exist")); !os.IsNotExist(err) {
		t.Fatal("maintenance initialized repository storage")
	}
	if c, err := net.DialTimeout("tcp", ssh, time.Second); err == nil {
		c.Close()
		t.Fatal("maintenance started SSH")
	}
	return "http://" + address, root
}
func TestMaintenanceUnavailableDependencies(t *testing.T) {
	base, root := maintenanceProcess(t)
	for _, method := range []string{"GET", "HEAD", "POST"} {
		for _, path := range []string{"/code/", "/code/api/v1/repos", "/code/hg/private/secret?cmd=unbundle"} {
			req, _ := http.NewRequest(method, base+path, nil)
			res, err := http.DefaultClient.Do(req)
			if err != nil {
				t.Fatal(err)
			}
			b, _ := io.ReadAll(res.Body)
			res.Body.Close()
			if res.StatusCode != 503 || res.Header.Get("Retry-After") != "300" || res.Header.Get("Cache-Control") != "no-store" {
				t.Fatalf("%s %s: %d", method, path, res.StatusCode)
			}
			if method == "HEAD" && len(b) != 0 {
				t.Fatal("HEAD body")
			}
			if strings.Contains(string(b), "private/secret") {
				t.Fatal("request detail leaked")
			}
		}
	}
	cmd := exec.Command(filepath.Join(root, filepath.Base(gogsExe)), "internal-hg-hook", "check", "pretxnopen")
	cmd.Env = []string{"PATH=" + os.Getenv("PATH"), "SYSTEMROOT=" + os.Getenv("SYSTEMROOT")}
	if out, err := cmd.CombinedOutput(); err == nil || !strings.Contains(string(out), "no write session context") {
		t.Fatalf("hook fail closed: %v %s", err, out)
	}
}
func TestMaintenanceBrowserFixture(t *testing.T) {
	if os.Getenv("UI_STATUS_MAINTENANCE_FIXTURE") != "1" {
		t.Skip("owned opt-in browser fixture")
	}
	base, root := maintenanceProcess(t)
	t.Logf("STATUS_MAINTENANCE_URL=%s/code/ CONFIG=%s/maintenance.ini STOP_FILE=%s/stop lifetime=8m", base, root, root)
	deadline := time.Now().Add(8 * time.Minute)
	for time.Now().Before(deadline) {
		if _, err := os.Stat(filepath.Join(root, "stop")); err == nil {
			return
		}
		time.Sleep(200 * time.Millisecond)
	}
}
