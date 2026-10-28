package collaboration

import (
	"fmt"
	"os"
	"os/exec"
	"testing"

	"github.com/gogits/gogs/modules/hgbridge"
)

// Hosting invokes this executable for hooks. Forward to the real application,
// never let the testing binary interpret hook arguments as a successful test run.
func TestMain(m *testing.M) {
	executable, err := os.Executable()
	if err != nil {
		panic(err)
	}
	hook := executable + ".hook.exe"
	if len(os.Args) > 1 && os.Args[1] == hgbridge.HookCommand {
		cmd := exec.Command(hook, os.Args[1:]...)
		cmd.Stdout, cmd.Stderr = os.Stdout, os.Stderr
		if err := cmd.Run(); err != nil {
			if exit, ok := err.(*exec.ExitError); ok {
				os.Exit(exit.ExitCode())
			}
			os.Exit(1)
		}
		os.Exit(0)
	}
	if os.Getenv("GOGS_TEST_HG") != "" {
		cmd := exec.Command("go", "build", "-o", hook, "github.com/gogits/gogs")
		if out, err := cmd.CombinedOutput(); err != nil {
			fmt.Fprintf(os.Stderr, "build native hook: %v\n%s", err, out)
			os.Exit(1)
		}
	}
	code := m.Run()
	os.Remove(hook)
	os.Exit(code)
}
