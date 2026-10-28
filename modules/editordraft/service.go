// Package editordraft owns short-lived, document-bound editor recovery.
package editordraft

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/hex"
	"encoding/json"
	"errors"
	"strconv"
	"time"
	"unicode/utf8"

	"github.com/gogits/gogs/models"
)

type Target struct {
	RepositoryID int64
	Kind         string
	ItemID       int64
}
type Payload struct {
	Title, Body string
	Metadata    map[string]string
}
type Grant struct {
	CollectionID, PageID, PageSecret, DisposalToken string
	ExpiresAt                                       time.Time
}
type Snapshot struct {
	Version   int64
	Payload   Payload
	ExpiresAt time.Time
}
type Authorize func(context.Context, int64, Target) error
type Store interface {
	Create(context.Context, *models.EditorDraft, int, time.Time) error
	Find(context.Context, string) (*models.EditorDraft, error)
	SaveIfLive(context.Context, string, int64, int64, string, time.Time, time.Time) error
	DiscardIfToken(context.Context, string, []byte) error
	PurgeExpired(context.Context, time.Time) error
}

var (
	ErrUnauthenticated = errors.New("editor draft: unauthenticated")
	ErrForbidden       = models.ErrEditorDraftForbidden
	ErrExpired         = models.ErrEditorDraftExpired
	ErrDiscarded       = models.ErrEditorDraftDiscarded
	ErrStaleVersion    = models.ErrEditorDraftStaleVersion
	ErrLimitExceeded   = models.ErrEditorDraftLimitExceeded
	ErrInvalidPayload  = models.ErrEditorDraftInvalidPayload
)

type Service struct {
	store     Store
	authorize Authorize
	now       func() time.Time
}

func NewService(store Store, authorize Authorize, now func() time.Time) *Service {
	return &Service{store, authorize, now}
}
func RandomID() (string, error) {
	var b [32]byte
	if _, err := rand.Read(b[:]); err != nil {
		return "", err
	}
	return hex.EncodeToString(b[:]), nil
}
func hash(s string) []byte { h := sha256.Sum256([]byte(s)); return h[:] }
func validTarget(t Target) bool {
	if t.RepositoryID <= 0 {
		return false
	}
	switch t.Kind {
	case "issue_new", "pull_new":
		return t.ItemID == 0
	case "issue_edit", "issue_comment", "pull_edit", "pull_comment", "pull_review":
		return t.ItemID > 0
	}
	return false
}
func (s *Service) Open(ctx context.Context, actor int64, collection string, target Target) (Grant, error) {
	if actor <= 0 {
		return Grant{}, ErrUnauthenticated
	}
	if collection == "" || !validTarget(target) {
		return Grant{}, ErrInvalidPayload
	}
	if err := s.authorize(ctx, actor, target); err != nil {
		return Grant{}, err
	}
	g := Grant{CollectionID: collection, ExpiresAt: s.now().Add(30 * time.Minute)}
	var err error
	if g.PageID, err = RandomID(); err != nil {
		return Grant{}, err
	}
	if g.PageSecret, err = RandomID(); err != nil {
		return Grant{}, err
	}
	if g.DisposalToken, err = RandomID(); err != nil {
		return Grant{}, err
	}
	r := &models.EditorDraft{PageID: g.PageID, CollectionID: collection, ActorID: actor, RepositoryID: target.RepositoryID, Kind: target.Kind, ItemID: target.ItemID, PageSecretHash: hash(g.PageSecret), DisposalTokenHash: hash(g.DisposalToken), PayloadJSON: `{"Title":"","Body":"","Metadata":{}}`, ExpiresAt: g.ExpiresAt}
	if err = s.store.Create(ctx, r, 10, s.now()); err != nil {
		return Grant{}, err
	}
	return g, nil
}
func (s *Service) live(ctx context.Context, actor int64, collection, page, secret string) (*models.EditorDraft, error) {
	if actor <= 0 {
		return nil, ErrUnauthenticated
	}
	r, err := s.store.Find(ctx, page)
	if err != nil {
		return nil, err
	}
	if r.ActorID != actor || r.CollectionID != collection || secret == "" || subtle.ConstantTimeCompare(r.PageSecretHash, hash(secret)) != 1 {
		return nil, ErrForbidden
	}
	if r.Discarded {
		return nil, ErrDiscarded
	}
	if !r.ExpiresAt.After(s.now()) {
		return nil, ErrExpired
	}
	if err = s.authorize(ctx, actor, Target{r.RepositoryID, r.Kind, r.ItemID}); err != nil {
		return nil, err
	}
	return r, nil
}
func (s *Service) Save(ctx context.Context, actor int64, collection, page, secret string, version int64, p Payload) (Snapshot, error) {
	r, err := s.live(ctx, actor, collection, page, secret)
	if err != nil {
		return Snapshot{}, err
	}
	if err = ValidatePayload(r.Kind, p); err != nil {
		return Snapshot{}, err
	}
	if version < 0 || version != r.Version || version == 1<<63-1 {
		return Snapshot{}, ErrStaleVersion
	}
	b, err := json.Marshal(p)
	if err != nil {
		return Snapshot{}, ErrInvalidPayload
	}
	expiry := s.now().Add(30 * time.Minute)
	if err = s.store.SaveIfLive(ctx, page, version, version+1, string(b), expiry, s.now()); err != nil {
		return Snapshot{}, err
	}
	return Snapshot{version + 1, p, expiry}, nil
}
func (s *Service) Restore(ctx context.Context, actor int64, collection, page, secret string) (Snapshot, error) {
	r, err := s.live(ctx, actor, collection, page, secret)
	if err != nil {
		return Snapshot{}, err
	}
	var p Payload
	if json.Unmarshal([]byte(r.PayloadJSON), &p) != nil || ValidatePayload(r.Kind, p) != nil {
		return Snapshot{}, ErrInvalidPayload
	}
	return Snapshot{r.Version, p, r.ExpiresAt}, nil
}
func (s *Service) Discard(ctx context.Context, page, token string) error {
	if token == "" {
		return ErrForbidden
	}
	return s.store.DiscardIfToken(ctx, page, hash(token))
}
func (s *Service) PurgeExpired(ctx context.Context) error { return s.store.PurgeExpired(ctx, s.now()) }
func ValidatePayload(kind string, p Payload) error {
	if !utf8.ValidString(p.Title) || !utf8.ValidString(p.Body) || utf8.RuneCountInString(p.Title) > 255 || len(p.Body) > 64<<10 {
		return ErrInvalidPayload
	}
	allowed := map[string]bool{}
	switch kind {
	case "issue_new":
		allowed = map[string]bool{"assigneeid": true, "milestoneid": true}
	case "pull_new":
		for _, k := range []string{"source_repository", "source_kind", "source_name", "source_head", "target_kind", "target_name", "target_head", "reviewers"} {
			allowed[k] = true
		}
	case "issue_edit", "pull_edit":
	case "issue_comment":
		if p.Title != "" {
			return ErrInvalidPayload
		}
	case "pull_comment":
		if p.Title != "" {
			return ErrInvalidPayload
		}
		allowed = map[string]bool{"comparison_id": true, "path": true, "side": true, "line": true}
	case "pull_review":
		if p.Title != "" || p.Body != "" {
			return ErrInvalidPayload
		}
		allowed = map[string]bool{"comparison_id": true, "state": true}
	default:
		return ErrInvalidPayload
	}
	for k, v := range p.Metadata {
		if !allowed[k] || !utf8.ValidString(v) || len(v) > 4096 {
			return ErrInvalidPayload
		}
	}
	if kind == "pull_review" && len(p.Metadata) > 0 {
		if p.Metadata["state"] != "approved" && p.Metadata["state"] != "changes_requested" {
			return ErrInvalidPayload
		}
		if id, err := strconv.ParseInt(p.Metadata["comparison_id"], 10, 64); err != nil || id <= 0 {
			return ErrInvalidPayload
		}
	}
	if kind == "pull_comment" {
		m := p.Metadata
		if m["comparison_id"] != "" || m["path"] != "" || m["side"] != "" || m["line"] != "" {
			id, err := strconv.ParseInt(m["comparison_id"], 10, 64)
			line, lineErr := strconv.ParseInt(m["line"], 10, 32)
			if err != nil || id <= 0 || lineErr != nil || line <= 0 || m["path"] == "" || (m["side"] != "base" && m["side"] != "source") {
				return ErrInvalidPayload
			}
		}
	}
	return nil
}
