package collaboration

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/gogits/gogs/models"
)

// Windows directory junctions usually need no symlink privilege. All paths
// belong to this test; cleanup unlinks each junction itself before TempDir's
// cleanup, and never recursively traverses it to the sentinel target.
func TestPullArtifactsWindowsJunctionOwnership(t *testing.T) {
	root, outside := t.TempDir(), t.TempDir()
	sentinel := filepath.Join(outside, "sentinel")
	if err := os.WriteFile(sentinel, []byte("preserve outside target"), 0600); err != nil {
		t.Fatal(err)
	}
	junction := func(link, target string) {
		t.Helper()
		cmd := exec.Command("powershell.exe", "-NoProfile", "-NonInteractive", "-Command", "New-Item -ItemType Junction -Path $env:GOGS_TEST_JUNCTION -Target $env:GOGS_TEST_TARGET | Out-Null")
		cmd.Env = append(os.Environ(), "GOGS_TEST_JUNCTION="+link, "GOGS_TEST_TARGET="+target)
		if output, err := cmd.CombinedOutput(); err != nil {
			t.Skipf("temporary junction unavailable: %v %s", err, output)
		}
		t.Cleanup(func() {
			if err := os.Remove(link); err != nil && !os.IsNotExist(err) {
				t.Error(err)
			}
		})
	}
	checkSentinel := func() {
		t.Helper()
		b, err := os.ReadFile(sentinel)
		if err != nil || string(b) != "preserve outside target" {
			t.Fatalf("junction target changed: %q %v", b, err)
		}
	}
	linkedRoot := filepath.Join(root, "linked-root")
	junction(linkedRoot, outside)
	if err := prepareComparisonRoot(filepath.Join(linkedRoot, "nested")); err == nil {
		t.Fatal("junction root ancestor accepted")
	}
	if _, err := os.Stat(filepath.Join(outside, "nested")); !os.IsNotExist(err) {
		t.Fatalf("created through junction: %v", err)
	}
	checkSentinel()
	c := models.PullComparison{ID: 1, PullID: 1, ArtifactID: strings.Repeat("e", 64), OwnershipNonce: strings.Repeat("f", 64)}
	// Even a matching ownership marker behind a junction conveys no ownership.
	if err := os.WriteFile(filepath.Join(outside, comparisonOwnerFile), comparisonOwnerBytes(c), 0600); err != nil {
		t.Fatal(err)
	}
	junction(filepath.Join(root, c.ArtifactID), outside)
	if err := removeComparisonAllocation(root, c); err == nil {
		t.Fatal("junction wrapper claimed")
	}
	checkSentinel()
	c.ArtifactID = strings.Repeat("a", 64)
	if err := allocateComparison(root, c); err != nil {
		t.Fatal(err)
	}
	junction(filepath.Join(root, c.ArtifactID, "snapshot"), outside)
	if err := removeComparisonAllocation(root, c); err == nil {
		t.Fatal("nested snapshot junction removed")
	}
	checkSentinel()
	if _, err := os.Stat(filepath.Join(root, c.ArtifactID, comparisonOwnerFile)); err != nil {
		t.Fatalf("ownership marker removed on refusal: %v", err)
	}
}
