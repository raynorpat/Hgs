package hosting

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/gogits/gogs/models"
	"github.com/gogits/gogs/modules/hg"
)

func TestReviewPublishAtomicReservations(t *testing.T) {
	s, owner, repo := fixture(t)
	other, err := models.CreateHgRepository(context.Background(), s.runner, s.RepositoryRoot(), models.CreateHgRepositoryOptions{OwnerID: owner.Id, Name: "other", Private: true})
	if err != nil {
		t.Fatal(err)
	}
	op := models.ReviewPublication{ID: "first-operation-id", ActorID: owner.Id, Payload: "first"}
	if _, err = models.BeginReviewPublication(op, []int64{repo.Id, repo.Id}); err != nil {
		t.Fatal(err)
	}
	rows, err := models.PendingReviewReservations()
	if err != nil || len(rows) != 1 {
		t.Fatalf("dedup: %+v %v", rows, err)
	}
	if _, err = models.BeginReviewPublication(models.ReviewPublication{ID: "second-operation-id", ActorID: owner.Id, Payload: "second"}, []int64{other.Id, repo.Id}); !errors.Is(err, models.ErrReviewReserved) {
		t.Fatalf("overlap accepted: %v", err)
	}
	rows, err = models.PendingReviewReservations()
	if err != nil || len(rows) != 1 {
		t.Fatalf("partial acquisition: %+v %v", rows, err)
	}
	for _, changed := range []models.ReviewPublication{{ID: op.ID, ActorID: owner.Id + 1, Payload: op.Payload}, {ID: op.ID, ActorID: op.ActorID, Payload: "changed"}} {
		if _, err = models.BeginReviewPublication(changed, []int64{repo.Id}); err == nil {
			t.Fatal("operation identity rebound")
		}
	}
}
func TestReviewPublishReservationPreventsDeletion(t *testing.T) {
	s, owner, repo := fixture(t)
	op := models.ReviewPublication{ID: "publication-operation", ActorID: owner.Id, Payload: "prepared"}
	if _, err := models.BeginReviewPublication(op, []int64{repo.Id}); err != nil {
		t.Fatal(err)
	}
	if err := models.DeleteHgRepository(context.Background(), s.RepositoryRoot(), repo.Id, s.DrainRepository); err == nil {
		t.Fatal("reserved store deleted")
	}
	current, err := models.GetRepositoryById(repo.Id)
	if err != nil || current.State != models.RepositoryReady {
		t.Fatalf("reserved repository tombstoned: %+v %v", current, err)
	}
	path, _ := models.RepositoryStoragePath(s.RepositoryRoot(), repo.Id)
	if _, err = os.Stat(path); err != nil {
		t.Fatal(err)
	}
	if err = models.ReconcileHgRepositories(context.Background(), s.RepositoryRoot()); err != nil {
		t.Fatal(err)
	}
	if err = models.AbortReviewPublication(op.ID); err != nil {
		t.Fatal(err)
	}
	if err = models.DeleteHgRepository(context.Background(), s.RepositoryRoot(), repo.Id, s.DrainRepository); err != nil {
		t.Fatal(err)
	}
}
func TestReviewPublishRestartRestoresAdmissionFence(t *testing.T) {
	s, owner, repo := fixture(t)
	if _, err := models.BeginReviewPublication(models.ReviewPublication{ID: "pending-operation", ActorID: owner.Id, Payload: "prepared"}, []int64{repo.Id}); err != nil {
		t.Fatal(err)
	}
	if err := s.Shutdown(context.Background()); err != nil {
		t.Fatal(err)
	}
	restarted, err := New(context.Background(), s.cfg)
	if err != nil {
		t.Fatal(err)
	}
	defer restarted.Shutdown(context.Background())
	if release, err := restarted.RegisterBrowserRead(owner.Id, repo.Id, func() {}); err == nil {
		release()
		t.Fatal("restart admitted reserved repository")
	}
}

func TestReviewPublishAuthorityRequiresBothEndpoints(t *testing.T) {
	s, owner, target := fixture(t)
	source, err := models.CreateHgRepository(context.Background(), s.runner, s.RepositoryRoot(), models.CreateHgRepositoryOptions{OwnerID: owner.Id, Name: "source", Private: true})
	if err != nil {
		t.Fatal(err)
	}
	actor := &models.User{Name: "reviewer", Email: "reviewer@example.test", Passwd: "password", IsActive: true}
	if err = models.CreateUser(actor); err != nil {
		t.Fatal(err)
	}
	if err = models.AddAccess(&models.Access{UserId: actor.Id, RepoId: target.Id, Mode: models.WRITABLE}); err != nil {
		t.Fatal(err)
	}
	if err = models.AddAccess(&models.Access{UserId: actor.Id, RepoId: source.Id, Mode: models.READABLE}); err != nil {
		t.Fatal(err)
	}
	e := MutationExpectation{RepoID: target.Id, Source: MutationEndpoint{RepoID: source.Id}}
	if err = s.mutationAuthority(actor.Id, e); err != nil {
		t.Fatal(err)
	}
	if err = models.DeleteAccess(&models.Access{UserId: actor.Id, RepoId: source.Id}); err != nil {
		t.Fatal(err)
	}
	if err = s.mutationAuthority(actor.Id, e); err == nil {
		t.Fatal("lost source-read authority accepted")
	}
	if err = models.AddAccess(&models.Access{UserId: actor.Id, RepoId: source.Id, Mode: models.READABLE}); err != nil {
		t.Fatal(err)
	}
	if err = models.DeleteAccess(&models.Access{UserId: actor.Id, RepoId: target.Id}); err != nil {
		t.Fatal(err)
	}
	if err = s.mutationAuthority(actor.Id, e); err == nil {
		t.Fatal("lost target-write authority accepted")
	}
}

func TestReviewPublishTerminalStateCannotResume(t *testing.T) {
	s, owner, repo := fixture(t)
	op := models.ReviewPublication{ID: "terminal-operation-id", ActorID: owner.Id, Payload: "immutable"}
	if _, err := models.BeginReviewPublication(op, []int64{repo.Id}); err != nil {
		t.Fatal(err)
	}
	if err := models.StartReviewPublication(op.ID); err != nil {
		t.Fatal(err)
	}
	if err := models.ConcludeRevokedReviewPublication(op.ID, repo.Id, false, false); err != nil {
		t.Fatal(err)
	}
	if err := models.StartReviewPublication(op.ID); !errors.Is(err, models.ErrReviewPublicationTerminal) {
		t.Fatalf("terminal start: %v", err)
	}
	if err := models.CompleteReviewPublication(op.ID); err == nil {
		t.Fatal("late completion resurrected failed operation")
	}
	if _, err := models.BeginReviewPublication(op, []int64{repo.Id}); !errors.Is(err, models.ErrReviewPublicationTerminal) {
		t.Fatalf("terminal replay: %v", err)
	}
	current, err := models.ReviewPublicationStatus(op.ID)
	if err != nil || !current.Failed || current.Complete {
		t.Fatalf("terminal state changed: %+v %v", current, err)
	}
	_ = s
}
func TestReviewPublishStartupAbortsRevokedUndispatchedOperation(t *testing.T) {
	s, owner, repo := fixture(t)
	node := hg.NodeID(strings.Repeat("1", 40))
	e := MutationExpectation{RepoID: repo.Id, OperationID: "undispatched-operation", Node: node, Source: MutationEndpoint{RepoID: repo.Id, Node: node}, Prepared: hg.PreparedMerge{Source: node, Target: node}}
	payload, err := json.Marshal(e)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = models.BeginReviewPublication(models.ReviewPublication{ID: e.OperationID, ActorID: owner.Id, Payload: string(payload)}, []int64{repo.Id}); err != nil {
		t.Fatal(err)
	}
	owner.IsActive = false
	if err = models.UpdateUser(owner); err != nil {
		t.Fatal(err)
	}
	if err = s.Shutdown(context.Background()); err != nil {
		t.Fatal(err)
	}
	restarted, err := New(context.Background(), s.cfg)
	if err != nil {
		t.Fatal(err)
	}
	defer restarted.Shutdown(context.Background())
	current, err := models.ReviewPublicationStatus(e.OperationID)
	if err != nil || !current.Aborted || current.Started {
		t.Fatalf("undispatched state: %+v %v", current, err)
	}
	rows, err := models.PendingReviewReservations()
	if err != nil || len(rows) != 0 {
		t.Fatalf("startup retained revoked undispatched fence: %+v %v", rows, err)
	}
}

func TestReviewPublishResultInspectionFailureQuarantinesTarget(t *testing.T) {
	for _, tc := range []struct {
		name, output string
		repair       bool
	}{
		{"command_failure", "EXIT", true}, {"malformed_output", "{", true}, {"missing_node", "", false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			s, owner, target := fixture(t)
			source, err := models.CreateHgRepository(context.Background(), s.runner, s.RepositoryRoot(), models.CreateHgRepositoryOptions{OwnerID: owner.Id, Name: "source", Private: true})
			if err != nil {
				t.Fatal(err)
			}
			oldNode, resultNode := hg.NodeID(strings.Repeat("1", 40)), hg.NodeID(strings.Repeat("2", 40))
			selection := hg.ReviewSelection{Ref: hg.RefSelection{Kind: hg.RefBookmark, Name: "selected"}}
			e := MutationExpectation{RepoID: target.Id, Node: oldNode, Selection: selection, OperationID: "inspection-failure-operation", Source: MutationEndpoint{RepoID: source.Id, Node: resultNode}, Prepared: hg.PreparedMerge{Source: resultNode, Target: oldNode, Result: resultNode, Mode: "fast_forward", DestinationBranch: "default", Bookmark: "selected"}}
			for name, data := range map[string]string{"fake-review-result": tc.output, "fake-review-references": `{"rev":0,"node":"` + string(oldNode) + `","branch":"default","bookmarks":["selected"],"tags":[]}`} {
				if err = os.WriteFile(filepath.Join(filepath.Dir(s.cfg.Mercurial.ConfigPath), "review-inspection", name), []byte(data), 0600); err != nil {
					t.Fatal(err)
				}
			}
			path, _ := models.RepositoryStoragePath(s.RepositoryRoot(), target.Id)
			endpoint, err := s.inspector.InspectReviewEndpoint(context.Background(), path, selection)
			if err != nil || endpoint.Node != oldNode {
				t.Fatalf("independent reference inspection: %+v %v", endpoint, err)
			}
			payload, err := json.Marshal(e)
			if err != nil {
				t.Fatal(err)
			}
			// A no-longer-existing actor has no authority. Reconciliation may
			// observe facts, but cannot publish or recover on that actor's behalf.
			actorID := owner.Id + 1000
			op := models.ReviewPublication{ID: e.OperationID, ActorID: actorID, Payload: string(payload)}
			if _, err = models.BeginReviewPublication(op, []int64{source.Id, target.Id}); err != nil {
				t.Fatal(err)
			}
			if err = models.StartReviewPublication(op.ID); err != nil {
				t.Fatal(err)
			}
			s.sessions.reservations[source.Id], s.sessions.reservations[target.Id] = op.ID, op.ID
			if err = s.WithRepositoryMutation(context.Background(), actorID, e, func(context.Context, map[string]string) error {
				t.Fatal("revoked operation invoked publication")
				return nil
			}); err == nil {
				t.Fatal("revoked operation accepted")
			}
			current, err := models.ReviewPublicationStatus(op.ID)
			if err != nil || !current.Failed || current.Complete || current.RepairRequired != tc.repair {
				t.Fatalf("terminal state: %+v %v", current, err)
			}
			rows, err := models.PendingReviewReservations()
			if err != nil {
				t.Fatal(err)
			}
			if tc.repair {
				if len(rows) != 1 || rows[0].RepoID != target.Id || rows[0].OperationID != op.ID {
					t.Fatalf("target quarantine missing: %+v", rows)
				}
				if release, err := s.RegisterBrowserRead(owner.Id, target.Id, func() {}); err == nil {
					release()
					t.Fatal("uncertain target admitted")
				}
			} else if len(rows) != 0 {
				t.Fatalf("confirmed absent result retained fences: %+v", rows)
			}
			release, err := s.RegisterBrowserRead(owner.Id, source.Id, func() {})
			if err != nil {
				t.Fatalf("distinct source still blocked: %v", err)
			}
			release()
		})
	}
}
