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
