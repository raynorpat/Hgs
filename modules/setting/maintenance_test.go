package setting

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestMaintenanceConfigurationBootstrap(t *testing.T) {
	if root := os.Getenv("GOGS_SETTING_MAINTENANCE_CHILD"); root != "" {
		os.Args[0] = filepath.Join(root, "fake.exe")
		CustomConf = filepath.Join(root, "custom.ini")
		err := LoadConfig()
		expected := os.Getenv("GOGS_SETTING_MAINTENANCE_ERROR")
		if expected != "" {
			if err == nil || !strings.Contains(err.Error(), expected) {
				t.Fatalf("wanted %s got %v", expected, err)
			}
			return
		}
		if err != nil {
			t.Fatal(err)
		}
		if !Maintenance.Enabled || Maintenance.RetryAfterSeconds != 300 {
			t.Fatalf("invalid loaded defaults %+v", Maintenance)
		}
		if _, err = os.Stat(filepath.Join(root, "runtime-store")); !os.IsNotExist(err) {
			t.Fatal("configuration initialized storage")
		}
		return
	}
	exe, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct{ name, extra, want string }{{"defaults", "", ""}, {"unsafe-root", "ROOT_URL = http://localhost//external", "invalid ROOT_URL"}, {"unicode", "MAINTENANCE_MESSAGE = " + strings.Repeat("ü", 300), ""}, {"long", "MAINTENANCE_MESSAGE = " + strings.Repeat("ü", 301), "300 Unicode"}, {"low", "MAINTENANCE_RETRY_AFTER = 59", "60 and 86400"}, {"high", "MAINTENANCE_RETRY_AFTER = 86401", "60 and 86400"}, {"number", "MAINTENANCE_RETRY_AFTER = later", "invalid MAINTENANCE_RETRY_AFTER"}, {"boolean", "MAINTENANCE_MODE = sometimes", "invalid MAINTENANCE_MODE"}} {
		t.Run(tc.name, func(t *testing.T) {
			root := t.TempDir()
			if err := os.Mkdir(filepath.Join(root, "conf"), 0700); err != nil {
				t.Fatal(err)
			}
			for name, body := range map[string]string{"fake.exe": "fixture", "conf/app.ini": "[server]\nMAINTENANCE_MODE = true\n", "custom.ini": fmt.Sprintf("[server]\n%s\n[repository]\nROOT = %s\n", tc.extra, filepath.ToSlash(filepath.Join(root, "runtime-store")))} {
				if err := os.WriteFile(filepath.Join(root, name), []byte(body), 0700); err != nil {
					t.Fatal(err)
				}
			}
			cmd := exec.Command(exe, "-test.run=^TestMaintenanceConfigurationBootstrap$")
			cmd.Env = append(os.Environ(), "GOGS_SETTING_MAINTENANCE_CHILD="+root, "GOGS_SETTING_MAINTENANCE_ERROR="+tc.want)
			if out, err := cmd.CombinedOutput(); err != nil {
				t.Fatalf("%v %s", err, out)
			}
		})
	}
}
