//go:build integration

package hosting

import (
	"context"
	"errors"
	"github.com/gogits/gogs/models"
	"github.com/gogits/gogs/modules/hg"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestContributionReviewPublication(t *testing.T) {
	exe := os.Getenv("GOGS_TEST_HG")
	if exe == "" {
		t.Skip("GOGS_TEST_HG unavailable")
	}
	binary := nativeHookBinary(t)
	for _, mode := range []string{"normal", "recovered", "partial", "repair"} {
		t.Run(mode, func(t *testing.T) {
			ctx := context.Background()
			s, owner, target := fixtureConfig(t, func(c *Config) { c.Mercurial.Executable = exe; c.Mercurial.MaxProcesses = 1 })
			s.hookExecutable = binary
			source, err := models.CreateHgRepository(ctx, s.Runner(), s.RepositoryRoot(), models.CreateHgRepositoryOptions{OwnerID: owner.Id, Name: "source"})
			if err != nil {
				t.Fatal(err)
			}
			sp, _ := models.RepositoryStoragePath(s.RepositoryRoot(), source.Id)
			tp, _ := models.RepositoryStoragePath(s.RepositoryRoot(), target.Id)
			nativeRun(t, s.runner, sp, "debugbuilddag", "+2")
			nativeRun(t, s.runner, tp, "pull", "-r", "0", sp)
			nativeRun(t, s.runner, tp, "bookmark", "-r", "0", "main")
			src, err := s.runner.ResolveReview(ctx, sp, hg.ReviewSelection{Ref: hg.RefSelection{Kind: hg.RefBranch, Name: "default"}})
			if err != nil {
				t.Fatal(err)
			}
			dst, err := s.runner.ResolveReview(ctx, tp, hg.ReviewSelection{Ref: hg.RefSelection{Kind: hg.RefBookmark, Name: "main"}})
			if err != nil {
				t.Fatal(err)
			}
			snapshot, err := s.runner.SnapshotReview(ctx, src, dst, filepath.Join(t.TempDir(), "snapshot"))
			if err != nil {
				t.Fatal(err)
			}
			prepared, err := s.runner.PrepareReviewMerge(ctx, snapshot, filepath.Join(t.TempDir(), "prepared"), "fixture", "merge")
			if err != nil {
				t.Fatal(err)
			}
			if prepared.Mode != "fast_forward" {
				t.Fatal("fixture unexpectedly requires a working merge checkout")
			}
			e := MutationExpectation{RepoID: target.Id, Selection: dst.Selection, Node: dst.Node, OperationID: "contribution-review-" + mode, Source: MutationEndpoint{RepoID: source.Id, Selection: src.Selection, Node: src.Node}, Prepared: prepared}
			signals := 0
			s.cfg.RepositoryPublished = func(ctx context.Context, id int64) error {
				op, err := models.ReviewPublicationStatus(e.OperationID)
				if err != nil || !op.Complete {
					return errors.New("signal before durable complete")
				}
				if id != target.Id {
					return errors.New("wrong publication repository")
				}
				probe, cancel := context.WithTimeout(ctx, time.Second)
				defer cancel()
				// Re-entry would deadlock if called under the registry mutex or fence.
				entered := make(chan error, 1)
				go func() {
					entered <- s.WithMaintenanceRead(probe, id, func(context.Context, string) error { return nil })
				}()
				select {
				case err = <-entered:
					if err != nil {
						return err
					}
				case <-probe.Done():
					return probe.Err()
				}
				signals++
				return models.EnqueueContribution(ctx, "repository", id)
			}
			lost := errors.New("owned fixture response lost")
			err = s.WithRepositoryMutation(ctx, owner.Id, e, func(ctx context.Context, env map[string]string) error {
				if mode == "partial" || mode == "repair" {
					nativeRun(t, s.runner, tp, "pull", sp)
					if mode == "repair" {
						if err := os.WriteFile(filepath.Join(tp, ".hg", "store", "journal"), []byte("owned interrupted fixture"), 0600); err != nil {
							return err
						}
					}
					return lost
				}
				if err := s.runner.PublishReview(ctx, s.mutationRequest(e), env); err != nil {
					return err
				}
				if mode == "recovered" {
					return lost
				}
				return nil
			})
			if mode == "normal" {
				if err != nil || signals != 1 {
					t.Fatalf("normal complete notification: signals=%d err=%v", signals, err)
				}
				if err = s.WithRepositoryMutation(ctx, owner.Id, e, func(context.Context, map[string]string) error {
					t.Fatal("already-complete publication replayed native write")
					return nil
				}); err != nil || signals != 2 {
					t.Fatalf("already-complete: signals=%d err=%v", signals, err)
				}
				return
			}
			if !errors.Is(err, lost) || signals != 0 {
				t.Fatalf("unknown outcome notified: signals=%d err=%v", signals, err)
			}
			owner.IsActive = false
			if err = models.UpdateUser(owner); err != nil {
				t.Fatal(err)
			}
			op, err := models.ReviewPublicationStatus(e.OperationID)
			if err != nil {
				t.Fatal(err)
			}
			if err = s.finishRevokedPublication(ctx, e, op); err != nil {
				t.Fatal(err)
			}
			op, err = models.ReviewPublicationStatus(e.OperationID)
			if err != nil {
				t.Fatal(err)
			}
			if mode == "recovered" {
				if !op.Complete || signals != 1 {
					t.Fatalf("recovered completion: %+v signals=%d", op, signals)
				}
			} else {
				if op.Complete || signals != 0 || !op.Failed || op.RepairRequired != (mode == "repair") {
					t.Fatalf("partial/repair wrongly notified: %+v signals=%d", op, signals)
				}
			}
		})
	}
}
