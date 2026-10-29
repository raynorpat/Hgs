package models

import (
	"bytes"
	"context"
	"crypto/sha256"
	"errors"
	"fmt"
	"github.com/go-xorm/xorm"
	"strings"
	"sync"
	"testing"
	"time"
)

func draftDatabase(t *testing.T) (int64, int64, time.Time) {
	t.Helper()
	newTestDatabase(t)
	u := &User{Name: "draft-owner", LowerName: "draft-owner", Email: "draft@example.invalid", IsActive: true}
	if _, err := x.Insert(u); err != nil {
		t.Fatal(err)
	}
	r := &Repository{OwnerId: u.Id, Name: "draft", LowerName: "draft", State: RepositoryReady}
	if _, err := x.Insert(r); err != nil {
		t.Fatal(err)
	}
	return u.Id, r.Id, time.Date(2026, 10, 28, 12, 0, 0, 0, time.UTC)
}

func TestEditorDraftNeverLogsPayload(t *testing.T) {
	a, r, now := draftDatabase(t)
	var output bytes.Buffer
	x.Logger = xorm.NewSimpleLogger(&output)
	x.ShowSQL = true
	x.ShowDebug = true
	x.ShowErr = true
	s := NewEditorDraftStore()
	ctx := context.Background()
	row := draftRow(a, r, "log-page", now)
	row.PayloadJSON = `{"Body":"DRAFT-PRIVATE-CONTENT"}`
	if err := s.Create(ctx, row, 10, now); err != nil {
		t.Fatal(err)
	}
	err := s.Create(ctx, row, 10, now)
	if err == nil {
		t.Fatal("duplicate unexpectedly inserted")
	}
	if !errors.Is(err, ErrEditorDraftStorage) {
		t.Fatalf("database error was not sanitized: %v", err)
	}
	if strings.Contains(output.String(), "DRAFT-PRIVATE-CONTENT") || strings.Contains(err.Error(), "DRAFT-PRIVATE-CONTENT") {
		t.Fatal("draft text reached logging or error")
	}
}
func draftRow(actor, repo int64, page string, now time.Time) *EditorDraft {
	secret := sha256.Sum256([]byte("page-secret"))
	disposal := sha256.Sum256([]byte("disposal-token"))
	return &EditorDraft{PageID: page, CollectionID: "collection", ActorID: actor, RepositoryID: repo, Kind: "issue_new", PageSecretHash: secret[:], DisposalTokenHash: disposal[:], PayloadJSON: `{"Title":"","Body":"","Metadata":{}}`, ExpiresAt: now.Add(30 * time.Minute)}
}

func TestEditorDraftTimeZoneComparisons(t *testing.T) {
	ctx := context.Background()
	for _, hour := range []int{0, 23} {
		instant := time.Date(2026, 10, 28, hour, 30, 0, 0, time.UTC)
		for _, zone := range []struct {
			name   string
			offset int
		}{{"UTC", 0}, {"positive", 8 * 60 * 60}, {"negative", -7 * 60 * 60}} {
			t.Run(fmt.Sprintf("%02dUTC/%s", hour, zone.name), func(t *testing.T) {
				now := instant.In(time.FixedZone(zone.name, zone.offset))
				for _, live := range []bool{true, false} {
					t.Run(fmt.Sprint("cap_live=", live), func(t *testing.T) {
						actor, repo, _ := draftDatabase(t)
						seed := draftRow(actor, repo, "seed", instant)
						if !live {
							seed.ExpiresAt = instant.Add(-time.Second)
						}
						if _, err := x.Insert(seed); err != nil {
							t.Fatal(err)
						}
						err := NewEditorDraftStore().Create(ctx, draftRow(actor, repo, "next", instant), 1, now)
						if live && !errors.Is(err, ErrEditorDraftLimitExceeded) {
							t.Fatalf("live row did not count toward cap: %v", err)
						}
						if !live && err != nil {
							t.Fatalf("expired row still counted toward cap: %v", err)
						}
					})
				}
				t.Run("save", func(t *testing.T) {
					actor, repo, _ := draftDatabase(t)
					row := draftRow(actor, repo, "live", instant)
					if _, err := x.Insert(row); err != nil {
						t.Fatal(err)
					}
					store := NewEditorDraftStore()
					if err := store.SaveIfLive(ctx, row.PageID, 0, 1, `{"Body":"saved"}`, instant.Add(30*time.Minute), now); err != nil {
						t.Fatalf("same live instant refused save: %v", err)
					}
					stored, err := store.Find(ctx, row.PageID)
					if err != nil || stored.Version != 1 || stored.PayloadJSON != `{"Body":"saved"}` || !stored.ExpiresAt.Equal(instant.Add(30*time.Minute)) {
						t.Fatal("live save changed the version, payload or expiry instant")
					}
					if err := store.SaveIfLive(ctx, row.PageID, 0, 1, `{"Body":"stale"}`, instant.Add(time.Hour), now); !errors.Is(err, ErrEditorDraftStaleVersion) {
						t.Fatalf("stale save was accepted: %v", err)
					}
					if err := store.SaveIfLive(ctx, row.PageID, 1, 2, `{"Body":"late"}`, instant.Add(time.Hour), now.Add(30*time.Minute)); !errors.Is(err, ErrEditorDraftExpired) {
						t.Fatalf("expired save was accepted: %v", err)
					}
				})
				t.Run("purge", func(t *testing.T) {
					actor, repo, _ := draftDatabase(t)
					for _, seed := range []struct {
						page   string
						expiry time.Time
					}{{"expired", instant.Add(-time.Second)}, {"boundary", instant}, {"live", instant.Add(time.Second)}} {
						row := draftRow(actor, repo, seed.page, instant)
						row.ExpiresAt = seed.expiry
						if _, err := x.Insert(row); err != nil {
							t.Fatal(err)
						}
					}
					if err := NewEditorDraftStore().PurgeExpired(ctx, now); err != nil {
						t.Fatal(err)
					}
					for _, page := range []string{"expired", "boundary", "live"} {
						found, err := x.Id(page).Get(new(EditorDraft))
						if err != nil || found != (page == "live") {
							t.Errorf("purge retained wrong row set for %s: found=%t err=%v", page, found, err)
						}
					}
				})
			})
		}
	}
}

func TestEditorDraftLimitsAndExpiry(t *testing.T) {
	a, r, now := draftDatabase(t)
	s := NewEditorDraftStore()
	ctx := context.Background()
	for i := 0; i < 10; i++ {
		if err := s.Create(ctx, draftRow(a, r, fmt.Sprint(i), now), 10, now); err != nil {
			t.Fatal(err)
		}
	}
	if err := s.Create(ctx, draftRow(a, r, "overflow", now), 10, now); !errors.Is(err, ErrEditorDraftLimitExceeded) {
		t.Fatalf("cap: %v", err)
	}
	if err := s.SaveIfLive(ctx, "0", 0, 1, `{"Body":"late"}`, now.Add(time.Hour), now.Add(30*time.Minute)); !errors.Is(err, ErrEditorDraftExpired) {
		t.Fatalf("expired save: %v", err)
	}
	if err := s.PurgeExpired(ctx, now.Add(30*time.Minute)); err != nil {
		t.Fatal(err)
	}
	var rows []EditorDraft
	if err := x.Find(&rows); err != nil {
		t.Fatal(err)
	}
	if len(rows) != 0 {
		t.Fatal("purge retained rows")
	}
}
func TestEditorDraftVersionAndDiscard(t *testing.T) {
	a, r, now := draftDatabase(t)
	s := NewEditorDraftStore()
	ctx := context.Background()
	row := draftRow(a, r, "page", now)
	if err := s.Create(ctx, row, 10, now); err != nil {
		t.Fatal(err)
	}
	if err := s.SaveIfLive(ctx, "page", 0, 1, `{"Body":"new"}`, now.Add(30*time.Minute), now); err != nil {
		t.Fatal(err)
	}
	if err := s.SaveIfLive(ctx, "page", 0, 1, `{"Body":"old"}`, now.Add(30*time.Minute), now); !errors.Is(err, ErrEditorDraftStaleVersion) {
		t.Fatalf("stale CAS: %v", err)
	}
	bad := sha256.Sum256([]byte("wrong"))
	if err := s.DiscardIfToken(ctx, "page", bad[:]); !errors.Is(err, ErrEditorDraftForbidden) {
		t.Fatalf("wrong deletion token: %v", err)
	}
	if err := s.DiscardIfToken(ctx, "page", row.DisposalTokenHash); err != nil {
		t.Fatal(err)
	}
	if err := s.SaveIfLive(ctx, "page", 1, 2, `{"Body":"resurrect"}`, now.Add(30*time.Minute), now); !errors.Is(err, ErrEditorDraftDiscarded) {
		t.Fatalf("resurrection: %v", err)
	}
	stored, err := s.Find(ctx, "page")
	if err != nil || stored.PayloadJSON != "" || !stored.Discarded {
		t.Fatalf("terminal row: %+v %v", stored, err)
	}
}
func TestEditorDraftAtomicCap(t *testing.T) {
	a, r, now := draftDatabase(t)
	s := NewEditorDraftStore()
	ctx := context.Background()
	for i := 0; i < 9; i++ {
		if err := s.Create(ctx, draftRow(a, r, fmt.Sprint(i), now), 10, now); err != nil {
			t.Fatal(err)
		}
	}
	var wg sync.WaitGroup
	start := make(chan struct{})
	errs := make(chan error, 8)
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			<-start
			errs <- s.Create(ctx, draftRow(a, r, fmt.Sprintf("race-%d", i), now), 10, now)
		}(i)
	}
	close(start)
	wg.Wait()
	close(errs)
	success := 0
	for err := range errs {
		if err == nil {
			success++
		}
	}
	if success != 1 {
		t.Fatalf("concurrent cap admitted %d, want 1", success)
	}
	n, err := x.Where("actor_id = ? AND discarded = ?", a, false).Count(new(EditorDraft))
	if err != nil || n != 10 {
		t.Fatalf("stored %d: %v", n, err)
	}
}
func TestEditorDraftOwnerAndPermission(t *testing.T) {
	a, r, now := draftDatabase(t)
	s := NewEditorDraftStore()
	ctx := context.Background()
	row := draftRow(a, r, "page", now)
	if err := s.Create(ctx, row, 10, now); err != nil {
		t.Fatal(err)
	}
	if _, err := x.Id(r).Cols("state").Update(&Repository{State: RepositoryDeleting}); err != nil {
		t.Fatal(err)
	}
	if err := s.Create(ctx, draftRow(a, r, "orphan", now), 10, now); !errors.Is(err, ErrEditorDraftForbidden) {
		t.Fatalf("recreated after deletion: %v", err)
	}
	if err := s.SaveIfLive(ctx, "page", 0, 1, `{"Body":"orphan"}`, now.Add(30*time.Minute), now); !errors.Is(err, ErrEditorDraftForbidden) {
		t.Fatalf("save after deletion: %v", err)
	}
}

func TestEditorDraftDeletionCleanup(t *testing.T) {
	_, repo, now := draftDatabase(t)
	actor := &User{Name: "draft-reader", LowerName: "draft-reader", Email: "reader@example.invalid", IsActive: true}
	if _, err := x.Insert(actor); err != nil {
		t.Fatal(err)
	}
	s := NewEditorDraftStore()
	ctx := context.Background()
	if err := s.Create(ctx, draftRow(actor.Id, repo, "deleted-account", now), 10, now); err != nil {
		t.Fatal(err)
	}
	if err := DeleteUser(actor); err != nil {
		t.Fatal(err)
	}
	var row EditorDraft
	ok, err := x.Id("deleted-account").Get(&row)
	if err != nil || ok {
		t.Fatalf("account payload retained: %v %v", ok, err)
	}
	if err := s.Create(ctx, draftRow(actor.Id, repo, "after-deletion", now), 10, now); !errors.Is(err, ErrEditorDraftForbidden) {
		t.Fatalf("orphan recreated: %v", err)
	}
}

func TestEditorDraftIssueWriter(t *testing.T) {
	owner, repo, now := draftDatabase(t)
	writer := &User{Name: "draft-writer", LowerName: "draft-writer", Email: "writer@example.invalid", IsActive: true}
	if _, err := x.Insert(writer); err != nil {
		t.Fatal(err)
	}
	if _, err := x.Insert(&Access{UserId: writer.Id, RepoId: repo, Mode: WRITABLE}); err != nil {
		t.Fatal(err)
	}
	issue := &Issue{RepoId: repo, Index: 1, PosterId: owner, Name: "saved", Content: "saved body"}
	if _, err := x.Insert(issue); err != nil {
		t.Fatal(err)
	}
	r := draftRow(writer.Id, repo, "writer", now)
	r.Kind, r.ItemID = "issue_edit", issue.Id
	if err := NewEditorDraftStore().Create(context.Background(), r, 10, now); err != nil {
		t.Fatalf("canonical issue writer refused: %v", err)
	}
}

func TestEditorDraftIssueAssignee(t *testing.T) {
	owner, repo, now := draftDatabase(t)
	reader := &User{Name: "draft-assignee", LowerName: "draft-assignee", Email: "assignee@example.invalid", IsActive: true}
	if _, err := x.Insert(reader); err != nil {
		t.Fatal(err)
	}
	if _, err := x.Insert(&Access{UserId: reader.Id, RepoId: repo, Mode: READABLE}); err != nil {
		t.Fatal(err)
	}
	r := draftRow(owner, repo, "assignee", now)
	r.PayloadJSON = fmt.Sprintf(`{"Metadata":{"assigneeid":"%d"}}`, reader.Id)
	if err := NewEditorDraftStore().Create(context.Background(), r, 10, now); err != nil {
		t.Fatalf("canonical collaborator assignee refused: %v", err)
	}
}

func TestEditorDraftSourceDeletionCleanup(t *testing.T) {
	a, target, now := draftDatabase(t)
	source := &Repository{OwnerId: a, Name: "draft-source", LowerName: "draft-source", State: RepositoryReady}
	if _, err := x.Insert(source); err != nil {
		t.Fatal(err)
	}
	p := &PullRequest{SourceRepoID: source.Id, TargetRepoID: target, AuthorID: a, Number: 1, State: PullOpen, Title: "review"}
	if _, err := x.Insert(p); err != nil {
		t.Fatal(err)
	}
	r := draftRow(a, target, "source-dependent", now)
	r.Kind, r.ItemID = "pull_comment", p.ID
	s := NewEditorDraftStore()
	ctx := context.Background()
	if err := s.Create(ctx, r, 10, now); err != nil {
		t.Fatal(err)
	}
	if r.SourceRepositoryID != source.Id {
		t.Fatal("source dependency not recorded")
	}
	if _, err := x.Id(source.Id).Cols("state").Update(&Repository{State: RepositoryDeleting}); err != nil {
		t.Fatal(err)
	}
	tx := x.NewSession()
	defer tx.Close()
	if err := tx.Begin(); err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback()
	if err := deleteRepositoryPullRows(tx, source.Id); err != nil {
		t.Fatal(err)
	}
	if err := tx.Commit(); err != nil {
		t.Fatal(err)
	}
	var stored EditorDraft
	ok, err := x.Id(r.PageID).Get(&stored)
	if err != nil || ok {
		t.Fatalf("source-dependent draft retained: %v %v", ok, err)
	}
	if err := s.SaveIfLive(ctx, r.PageID, 0, 1, `{"Body":"recreate"}`, now.Add(time.Hour), now); !errors.Is(err, ErrEditorDraftExpired) {
		t.Fatalf("cleanup followed by orphan save: %v", err)
	}
}

// Fail one real SQLite query at each policy read boundary, with no production hook.
type draftFailRead struct {
	Engine
	at, count int
}

func (e *draftFailRead) Where(q string, args ...interface{}) *xorm.Session {
	e.count++
	if e.count == e.at {
		return x.Where("draft_outage_missing_column = 1")
	}
	return e.Engine.Where(q, args...)
}
func (e *draftFailRead) Id(id interface{}) *xorm.Session {
	e.count++
	if e.count == e.at {
		return x.Where("draft_outage_missing_column = 1")
	}
	return e.Engine.Id(id)
}
func TestEditorDraftPolicyOutage(t *testing.T) {
	a, repo, now := draftDatabase(t)
	p := &PullRequest{SourceRepoID: repo, TargetRepoID: repo, AuthorID: a, Number: 1, State: PullOpen, Title: "review"}
	if _, err := x.Insert(p); err != nil {
		t.Fatal(err)
	}
	for _, kind := range []string{"pull_new", "pull_comment"} {
		r := draftRow(a, repo, kind, now)
		r.Kind, r.SourceRepositoryID = kind, repo
		if kind != "pull_new" {
			r.ItemID = p.ID
		}
		baseline := &draftFailRead{Engine: x}
		if err := draftAuthority(baseline, r, editorDraftPayload{}, false); err != nil {
			t.Fatal(err)
		}
		for n := 1; n <= baseline.count; n++ {
			e := &draftFailRead{Engine: x, at: n}
			err := draftStoreError(draftAuthority(e, r, editorDraftPayload{}, false))
			if !errors.Is(err, ErrEditorDraftStorage) {
				t.Errorf("%s query %d outage classified as %v", kind, n, err)
			}
		}
	}
}

func TestEditorDraftInactiveDeletionSet(t *testing.T) {
	for _, activate := range []bool{false, true} {
		t.Run(fmt.Sprint("activate=", activate), func(t *testing.T) {
			a, repo, now := draftDatabase(t)
			b := &User{Name: "later", LowerName: "later", Email: "later@example.invalid", IsActive: true}
			if _, err := x.Insert(b); err != nil {
				t.Fatal(err)
			}
			for _, id := range []int64{a, b.Id} {
				if _, err := x.Insert(draftRow(id, repo, fmt.Sprint(id), now)); err != nil {
					t.Fatal(err)
				}
			}
			if _, err := x.Id(a).Cols("is_active").Update(&User{IsActive: false}); err != nil {
				t.Fatal(err)
			}
			trigger := fmt.Sprintf("CREATE TRIGGER draft_cleanup_interleave BEFORE DELETE ON editor_draft WHEN OLD.actor_id = %d BEGIN UPDATE user SET is_active = 0 WHERE id = %d; END", a, b.Id)
			if activate {
				trigger = fmt.Sprintf("CREATE TRIGGER draft_cleanup_interleave BEFORE UPDATE OF id ON user WHEN OLD.id = %d BEGIN UPDATE user SET is_active = 1 WHERE id = %d; END", a, a)
			}
			if _, err := x.Exec(trigger); err != nil {
				t.Fatal(err)
			}
			if err := DeleteInactivateUsers(); err != nil {
				t.Fatal(err)
			}
			var first, second User
			hasA, err := x.Id(a).Get(&first)
			if err != nil {
				t.Fatal(err)
			}
			hasB, err := x.Id(b.Id).Get(&second)
			if err != nil {
				t.Fatal(err)
			}
			if !hasB {
				t.Error("account outside fenced set was deleted")
			}
			if hasA != activate {
				t.Errorf("reactivated account preservation=%v want %v", hasA, activate)
			}
			var row EditorDraft
			hasDraft, err := x.Id(fmt.Sprint(a)).Get(&row)
			if err != nil {
				t.Fatal(err)
			}
			if hasDraft != activate {
				t.Errorf("draft cleanup did not match rechecked deletion set: %v", hasDraft)
			}
		})
	}
}

func TestEditorDraftCandidateVersusKnownSource(t *testing.T) {
	a, target, now := draftDatabase(t)
	if _, err := x.Id(target).Cols("is_private").Update(&Repository{IsPrivate: true}); err != nil {
		t.Fatal(err)
	}
	owner := &User{Name: "source-owner", LowerName: "source-owner", IsActive: true, Email: "source@example.invalid"}
	if _, err := x.Insert(owner); err != nil {
		t.Fatal(err)
	}
	source := &Repository{OwnerId: owner.Id, Name: "source", LowerName: "source", IsPrivate: true, State: RepositoryReady}
	if _, err := x.Insert(source); err != nil {
		t.Fatal(err)
	}
	if _, err := x.Insert(&ForkRelation{RepositoryID: source.Id, SourceID: target, FamilyID: target}); err != nil {
		t.Fatal(err)
	}
	if _, err := x.Insert(&Access{UserId: a, RepoId: source.Id, Mode: WRITABLE}); err != nil {
		t.Fatal(err)
	}
	store := NewEditorDraftStore()
	ctx := context.Background()
	known := draftRow(a, target, "known-source", now)
	known.Kind = "pull_new"
	known.PayloadJSON = `{"Body":"saved","Metadata":{"source_repository":"source-owner/source"}}`
	if err := store.Create(ctx, known, 10, now); err != nil {
		t.Fatal(err)
	}
	fresh := draftRow(a, target, "new-candidate", now)
	fresh.Kind = "pull_new"
	if err := store.Create(ctx, fresh, 10, now); err != nil {
		t.Fatal(err)
	}
	if _, err := x.Where("user_id = ? AND repo_id = ?", a, source.Id).Delete(new(Access)); err != nil {
		t.Fatal(err)
	}
	for _, candidate := range []string{"source-owner/source", "missing/source"} {
		payload := fmt.Sprintf(`{"Body":"local","Metadata":{"source_repository":%q}}`, candidate)
		if err := store.SaveIfLive(ctx, fresh.PageID, 0, 1, payload, now.Add(time.Hour), now); !errors.Is(err, ErrEditorDraftInvalidPayload) {
			t.Errorf("new candidate %s: %v", candidate, err)
		}
	}
	if _, err := x.Id(source.Id).Cols("state").Update(&Repository{State: RepositoryDeleting}); err != nil {
		t.Fatal(err)
	}
	if err := store.SaveIfLive(ctx, fresh.PageID, 0, 1, known.PayloadJSON, now.Add(time.Hour), now); !errors.Is(err, ErrEditorDraftInvalidPayload) {
		t.Fatalf("different candidate ready-lock denial: %v", err)
	}
	var unchanged EditorDraft
	if _, err := x.Id(fresh.PageID).Get(&unchanged); err != nil {
		t.Fatal(err)
	}
	if unchanged.Version != 0 || unchanged.SourceRepositoryID != 0 || unchanged.PayloadJSON != fresh.PayloadJSON {
		t.Fatal("invalid candidate wrote payload/dependency")
	}
	if _, err := store.Find(ctx, known.PageID); !errors.Is(err, ErrEditorDraftForbidden) {
		t.Fatalf("known source revocation: %v", err)
	}
	if err := store.SaveIfLive(ctx, known.PageID, 0, 1, `{"Metadata":{"source_repository":"missing/source"}}`, now.Add(time.Hour), now); !errors.Is(err, ErrEditorDraftForbidden) {
		t.Fatalf("invalid replacement bypassed prior revocation: %v", err)
	}
}
