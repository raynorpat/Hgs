package editordraft

import (
	"context"
	"crypto/sha256"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/gogits/gogs/models"
)

// Store behavior is independently exercised against SQLite in models. This
// small double lets service authorization and expiry run under a fake clock.
type memoryStore struct {
	rows map[string]*models.EditorDraft
}

func TestEditorDraftAuthorizationOutagePreservesSnapshot(t *testing.T) {
	m := &memoryStore{rows: map[string]*models.EditorDraft{}}
	var unavailable bool
	s := NewService(m, func(context.Context, int64, Target) error {
		if unavailable {
			return models.ErrEditorDraftStorage
		}
		return nil
	}, time.Now)
	g, err := s.Open(context.Background(), 1, "collection", Target{1, "pull_new", 0})
	if err != nil {
		t.Fatal(err)
	}
	if _, err = s.Save(context.Background(), 1, "collection", g.PageID, g.PageSecret, 0, Payload{Body: "saved"}); err != nil {
		t.Fatal(err)
	}
	unavailable = true
	if _, err = s.Save(context.Background(), 1, "collection", g.PageID, g.PageSecret, 1, Payload{Body: "latest"}); !errors.Is(err, models.ErrEditorDraftStorage) {
		t.Fatal(err)
	}
	if m.rows[g.PageID].Discarded || m.rows[g.PageID].Version != 1 {
		t.Fatal("outage disposed or advanced snapshot")
	}
	unavailable = false
	snapshot, err := s.Restore(context.Background(), 1, "collection", g.PageID, g.PageSecret)
	if err != nil || snapshot.Payload.Body != "saved" {
		t.Fatalf("outage lost previous snapshot: %+v %v", snapshot, err)
	}
}

func (m *memoryStore) Create(_ context.Context, r *models.EditorDraft, max int, now time.Time) error {
	n := 0
	for _, row := range m.rows {
		if row.ActorID == r.ActorID && !row.Discarded && row.ExpiresAt.After(now) {
			n++
		}
	}
	if n >= max {
		return models.ErrEditorDraftLimitExceeded
	}
	copy := *r
	m.rows[r.PageID] = &copy
	return nil
}
func (m *memoryStore) Find(_ context.Context, id string) (*models.EditorDraft, error) {
	r := m.rows[id]
	if r == nil {
		return nil, models.ErrEditorDraftExpired
	}
	copy := *r
	return &copy, nil
}
func (m *memoryStore) SaveIfLive(_ context.Context, id string, expected, next int64, payload string, expiry, now time.Time) error {
	r := m.rows[id]
	if r == nil || !r.ExpiresAt.After(now) {
		return models.ErrEditorDraftExpired
	}
	if r.Discarded {
		return models.ErrEditorDraftDiscarded
	}
	if r.Version != expected {
		return models.ErrEditorDraftStaleVersion
	}
	r.Version, r.PayloadJSON, r.ExpiresAt = next, payload, expiry
	return nil
}
func (m *memoryStore) DiscardIfToken(_ context.Context, id string, hash []byte) error {
	r := m.rows[id]
	if r == nil {
		return nil
	}
	if string(r.DisposalTokenHash) != string(hash) {
		return models.ErrEditorDraftForbidden
	}
	r.Discarded, r.PayloadJSON = true, ""
	return nil
}
func (m *memoryStore) PurgeExpired(_ context.Context, now time.Time) error {
	for id, r := range m.rows {
		if !r.ExpiresAt.After(now) {
			delete(m.rows, id)
		}
	}
	return nil
}
func serviceFixture(t *testing.T) (*Service, *memoryStore, *time.Time, *bool) {
	t.Helper()
	now := time.Date(2026, 10, 28, 12, 0, 0, 0, time.UTC)
	allowed := true
	store := &memoryStore{rows: map[string]*models.EditorDraft{}}
	s := NewService(store, func(_ context.Context, actor int64, target Target) error {
		if !allowed || actor != 7 || target.RepositoryID != 11 {
			return ErrForbidden
		}
		return nil
	}, func() time.Time { return now })
	return s, store, &now, &allowed
}
func TestEditorDraftOwnerAndPermission(t *testing.T) {
	s, store, _, allowed := serviceFixture(t)
	ctx := context.Background()
	if _, err := s.Open(ctx, 0, "collection", Target{11, "issue_new", 0}); !errors.Is(err, ErrUnauthenticated) {
		t.Fatalf("anonymous open: %v", err)
	}
	g, err := s.Open(ctx, 7, "collection", Target{11, "issue_new", 0})
	if err != nil {
		t.Fatal(err)
	}
	if len(g.PageID) < 32 || g.PageSecret == "" || g.DisposalToken == "" || g.PageSecret == g.DisposalToken {
		t.Fatal("unpredictable independent credentials missing")
	}
	r := store.rows[g.PageID]
	secret := sha256.Sum256([]byte(g.PageSecret))
	if string(r.PageSecretHash) != string(secret[:]) || strings.Contains(r.PayloadJSON, g.PageSecret) {
		t.Fatal("page secret must be hashed only")
	}
	for _, c := range []struct {
		actor              int64
		collection, secret string
	}{{8, "collection", g.PageSecret}, {7, "other", g.PageSecret}, {7, "collection", ""}, {7, "collection", g.DisposalToken}} {
		if _, err := s.Restore(ctx, c.actor, c.collection, g.PageID, c.secret); !errors.Is(err, ErrForbidden) {
			t.Fatalf("credential bypass: %v", err)
		}
	}
	*allowed = false
	if _, err := s.Save(ctx, 7, "collection", g.PageID, g.PageSecret, 0, Payload{Body: "private"}); !errors.Is(err, ErrForbidden) {
		t.Fatalf("permission loss: %v", err)
	}
	if _, err := s.Restore(ctx, 7, "collection", g.PageID, g.PageSecret); !errors.Is(err, ErrForbidden) {
		t.Fatalf("restore permission loss: %v", err)
	}
}
func TestEditorDraftLimitsAndExpiry(t *testing.T) {
	s, store, now, _ := serviceFixture(t)
	ctx := context.Background()
	g, err := s.Open(ctx, 7, "collection", Target{11, "issue_new", 0})
	if err != nil {
		t.Fatal(err)
	}
	valid := Payload{Title: strings.Repeat("界", 255), Body: strings.Repeat("é", 32768), Metadata: map[string]string{"milestoneid": "0"}}
	snapshot, err := s.Save(ctx, 7, "collection", g.PageID, g.PageSecret, 0, valid)
	if err != nil || snapshot.Version != 1 {
		t.Fatalf("Unicode boundary: %+v %v", snapshot, err)
	}
	for _, p := range []Payload{{Title: strings.Repeat("界", 256)}, {Body: strings.Repeat("é", 32769)}, {Metadata: map[string]string{"_csrf": "secret"}}, {Metadata: map[string]string{"attachments": "file"}}} {
		if _, err := s.Save(ctx, 7, "collection", g.PageID, g.PageSecret, 1, p); !errors.Is(err, ErrInvalidPayload) {
			t.Fatalf("invalid payload accepted: %v", err)
		}
	}
	for i := 1; i < 10; i++ {
		if _, err := s.Open(ctx, 7, "collection", Target{11, "issue_new", 0}); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := s.Open(ctx, 7, "collection", Target{11, "issue_new", 0}); !errors.Is(err, ErrLimitExceeded) {
		t.Fatalf("cap: %v", err)
	}
	*now = now.Add(29 * time.Minute)
	if _, err := s.Save(ctx, 7, "collection", g.PageID, g.PageSecret, 1, valid); err != nil {
		t.Fatal(err)
	}
	*now = now.Add(29 * time.Minute)
	if _, err := s.Restore(ctx, 7, "collection", g.PageID, g.PageSecret); err != nil {
		t.Fatal(err)
	}
	*now = now.Add(time.Minute)
	if _, err := s.Restore(ctx, 7, "collection", g.PageID, g.PageSecret); !errors.Is(err, ErrExpired) {
		t.Fatalf("inactivity expiry: %v", err)
	}
	if err := s.PurgeExpired(ctx); err != nil {
		t.Fatal(err)
	}
	if len(store.rows) != 0 {
		t.Fatal("expired payload retained")
	}
}
func TestEditorDraftVersionAndDiscard(t *testing.T) {
	s, store, _, _ := serviceFixture(t)
	ctx := context.Background()
	g, err := s.Open(ctx, 7, "collection", Target{11, "issue_comment", 23})
	if err != nil {
		t.Fatal(err)
	}
	if _, err = s.Save(ctx, 7, "collection", g.PageID, g.PageSecret, 0, Payload{Body: "new"}); err != nil {
		t.Fatal(err)
	}
	if _, err = s.Save(ctx, 7, "collection", g.PageID, g.PageSecret, 0, Payload{Body: "old"}); !errors.Is(err, ErrStaleVersion) {
		t.Fatalf("older save: %v", err)
	}
	snapshot, err := s.Restore(ctx, 7, "collection", g.PageID, g.PageSecret)
	if err != nil || snapshot.Payload.Body != "new" {
		t.Fatalf("restore: %+v %v", snapshot, err)
	}
	if err = s.Discard(ctx, g.PageID, g.PageSecret); !errors.Is(err, ErrForbidden) {
		t.Fatalf("read secret granted disposal: %v", err)
	}
	if err = s.Discard(ctx, g.PageID, g.DisposalToken); err != nil {
		t.Fatal(err)
	}
	if store.rows[g.PageID].PayloadJSON != "" {
		t.Fatal("discard retained payload")
	}
	if _, err = s.Save(ctx, 7, "collection", g.PageID, g.PageSecret, 1, Payload{Body: "resurrection"}); !errors.Is(err, ErrDiscarded) {
		t.Fatalf("resurrection: %v", err)
	}
	if _, err = s.Restore(ctx, 7, "collection", g.PageID, g.PageSecret); !errors.Is(err, ErrDiscarded) {
		t.Fatalf("discarded restore: %v", err)
	}
}
func TestEditorDraftMetadataOnlyReview(t *testing.T) {
	s, _, _, _ := serviceFixture(t)
	ctx := context.Background()
	g, err := s.Open(ctx, 7, "collection", Target{11, "pull_review", 31})
	if err != nil {
		t.Fatal(err)
	}
	if _, err = s.Save(ctx, 7, "collection", g.PageID, g.PageSecret, 0, Payload{Body: "new feature"}); !errors.Is(err, ErrInvalidPayload) {
		t.Fatalf("review body: %v", err)
	}
	if _, err = s.Save(ctx, 7, "collection", g.PageID, g.PageSecret, 0, Payload{Metadata: map[string]string{"comparison_id": "42", "state": "merge"}}); !errors.Is(err, ErrInvalidPayload) {
		t.Fatalf("unsupported review action accepted: %v", err)
	}
	if _, err = s.Save(ctx, 7, "collection", g.PageID, g.PageSecret, 0, Payload{Metadata: map[string]string{"comparison_id": "42", "state": "approved"}}); err != nil {
		t.Fatal(err)
	}
}
