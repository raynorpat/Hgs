package models

import (
	"context"
	"crypto/subtle"
	"encoding/json"
	"errors"
	"io"
	"strconv"
	"strings"
	"time"

	"github.com/go-xorm/xorm"
)

// EditorDraft is temporary data, never an authentication or mutation grant.
type EditorDraft struct {
	PageID                            string `xorm:"pk VARCHAR(64) 'page_id'"`
	CollectionID                      string `xorm:"VARCHAR(64) 'collection_id'"`
	ActorID                           int64  `xorm:"index 'actor_id'"`
	RepositoryID                      int64  `xorm:"index 'repository_id'"`
	SourceRepositoryID                int64  `xorm:"index 'source_repository_id'"`
	Kind                              string `xorm:"VARCHAR(24)"`
	ItemID                            int64  `xorm:"index 'item_id'"`
	PageSecretHash, DisposalTokenHash []byte
	Version                           int64
	PayloadJSON                       string    `xorm:"MEDIUMTEXT 'payload_json'"`
	ExpiresAt                         time.Time `xorm:"index"`
	Discarded                         bool
}

var (
	ErrEditorDraftForbidden      = errors.New("editor draft: forbidden")
	ErrEditorDraftExpired        = errors.New("editor draft: expired")
	ErrEditorDraftDiscarded      = errors.New("editor draft: discarded")
	ErrEditorDraftStaleVersion   = errors.New("editor draft: stale version")
	ErrEditorDraftLimitExceeded  = errors.New("editor draft: limit exceeded")
	ErrEditorDraftInvalidPayload = errors.New("editor draft: invalid payload")
	ErrEditorDraftStorage        = errors.New("editor draft: storage unavailable")
)

type EditorDraftStore struct{}

func lockDraftAccountDeletion(s *xorm.Session, actor int64) error {
	if DbCfg.Type == "sqlite3" {
		_, err := s.Exec("UPDATE `user` SET id = id WHERE id = ?", actor)
		return err
	}
	_, err := policyGet(s, true, "user", "id = ?", new(User), actor)
	return err
}

func NewEditorDraftStore() *EditorDraftStore { return &EditorDraftStore{} }

func draftQueryTime(now time.Time) time.Time {
	// Xorm writes SQLite datetime fields in UTC; raw query arguments otherwise
	// retain their supplied zone. Match the stored representation of this instant.
	if DbCfg.Type == "sqlite3" {
		return now.UTC()
	}
	return now
}

// The pinned Engine contains a pointer mutex and shared table/cache/DB objects.
// This request-local view changes only logging flags and its logger, never the
// shared dialect logger via SetLogger. Tables have already been synchronized;
// draft operations perform no schema discovery or dialect logging operations.
func draftEngine() *xorm.Engine {
	e := *x
	e.ShowSQL, e.ShowDebug, e.ShowErr, e.ShowInfo, e.ShowWarn = false, false, false, false, false
	e.Logger = xorm.NewSimpleLogger(io.Discard)
	return &e
}
func draftStoreError(err error) error {
	if err == nil {
		return nil
	}
	for _, known := range []error{ErrEditorDraftForbidden, ErrEditorDraftExpired, ErrEditorDraftDiscarded, ErrEditorDraftStaleVersion, ErrEditorDraftLimitExceeded, ErrEditorDraftInvalidPayload, context.Canceled, context.DeadlineExceeded} {
		if errors.Is(err, known) {
			return known
		}
	}
	// Database errors can contain failing-row values, even with SQL logging off.
	return ErrEditorDraftStorage
}

// This private serialization view keeps models independent of editordraft.
type editorDraftPayload struct {
	Title, Body string
	Metadata    map[string]string
}

func draftPayload(raw string) (editorDraftPayload, error) {
	var p editorDraftPayload
	err := json.Unmarshal([]byte(raw), &p)
	if err != nil {
		return p, ErrEditorDraftInvalidPayload
	}
	return p, nil
}
func draftDenied(err error) error {
	if errors.Is(err, ErrReviewReserved) || errors.Is(err, ErrInvalidCredential) || errors.Is(err, ErrRepoNotExist) || errors.Is(err, ErrUserNotExist) || errors.Is(err, ErrPullNotExist) || errors.Is(err, ErrForkLineage) || errors.Is(err, ErrPullState) || errors.Is(err, ErrPullInvalid) {
		return ErrEditorDraftForbidden
	}
	return err
}
func draftSource(e Engine, r *EditorDraft, p editorDraftPayload) (int64, error) {
	if strings.HasPrefix(r.Kind, "pull_") && r.Kind != "pull_new" {
		var pull PullRequest
		ok, err := e.Id(r.ItemID).Get(&pull)
		if err != nil {
			return 0, err
		}
		if !ok || pull.TargetRepoID != r.RepositoryID || pull.SourceDeleted {
			return 0, ErrEditorDraftForbidden
		}
		return pull.SourceRepoID, nil
	}
	if r.Kind != "pull_new" || p.Metadata["source_repository"] == "" {
		return 0, nil
	}
	parts := strings.Split(p.Metadata["source_repository"], "/")
	if len(parts) != 2 {
		return 0, nil
	}
	var owner User
	ok, err := e.Where("lower_name = ?", strings.ToLower(parts[0])).Get(&owner)
	if err != nil || !ok {
		return 0, err
	}
	var repo Repository
	ok, err = e.Where("owner_id = ? AND lower_name = ?", owner.Id, strings.ToLower(parts[1])).Get(&repo)
	if err != nil || !ok {
		return 0, err
	}
	return repo.Id, nil
}
func draftAuthority(e Engine, r *EditorDraft, p editorDraftPayload, locked bool) error {
	if _, err := enabledPolicyAccount(e, r.ActorID, locked); err != nil {
		return draftDenied(err)
	}
	a, err := repositoryAccessFor(e, r.ActorID, r.RepositoryID, locked)
	if err != nil {
		return draftDenied(err)
	}
	if !a.Read {
		return ErrEditorDraftForbidden
	}
	switch r.Kind {
	case "issue_new":
		if r.ItemID != 0 {
			return ErrEditorDraftForbidden
		}
	case "issue_edit", "issue_comment":
		var issue Issue
		ok, err := policyGet(e, locked, "issue", "id = ? AND repo_id = ?", &issue, r.ItemID, r.RepositoryID)
		if err != nil {
			return err
		}
		if !ok || issue.IsPull {
			return ErrEditorDraftForbidden
		}
		if r.Kind == "issue_edit" && issue.PosterId != r.ActorID && !a.Write {
			return ErrEditorDraftForbidden
		}
	case "pull_new":
		if r.ItemID != 0 {
			return ErrEditorDraftForbidden
		}
		if r.SourceRepositoryID > 0 {
			if err := pullCreationAllowed(e, r.ActorID, PullRequest{SourceRepoID: r.SourceRepositoryID, TargetRepoID: r.RepositoryID}, locked); err != nil {
				return draftDenied(err)
			}
		}
	case "pull_edit", "pull_comment", "pull_review":
		var pull PullRequest
		ok, err := policyGet(e, locked, "pull_request", "id = ? AND target_repo_id = ?", &pull, r.ItemID, r.RepositoryID)
		if err != nil {
			return err
		}
		if !ok || pull.SourceDeleted || pull.SourceRepoID != r.SourceRepositoryID {
			return ErrEditorDraftForbidden
		}
		if _, _, err = pullReads(e, r.ActorID, pull, locked); err != nil {
			return draftDenied(err)
		}
		if err = pullComparisonPrivacy(e, pull, locked); err != nil {
			return draftDenied(err)
		}
		if r.Kind == "pull_edit" && pull.AuthorID != r.ActorID {
			return ErrEditorDraftForbidden
		}
		if r.Kind == "pull_review" {
			eligible, err := pullReviewEligibleWithLock(e, pull, r.ActorID, locked)
			if err != nil {
				return err
			}
			var review PullReview
			assigned, err := policyGet(e, locked, "pull_review", "pull_id = ? AND reviewer_id = ?", &review, pull.ID, r.ActorID)
			if err != nil {
				return err
			}
			if !eligible || !assigned || !review.Assigned || pull.State != PullOpen {
				return ErrEditorDraftForbidden
			}
			var current PullComparison
			ready, err := policyGet(e, locked, "pull_comparison", "id = ? AND pull_id = ?", &current, pull.CurrentComparisonID, pull.ID)
			if err != nil {
				return err
			}
			if !ready || current.State != "ready" || !current.SnapshotComplete {
				return ErrEditorDraftForbidden
			}
		}
		if id := p.Metadata["comparison_id"]; id != "" && id != "0" {
			n, err := strconv.ParseInt(id, 10, 64)
			if err != nil || n <= 0 {
				return ErrEditorDraftInvalidPayload
			}
			var c PullComparison
			ok, err := policyGet(e, locked, "pull_comparison", "id = ? AND pull_id = ?", &c, n, pull.ID)
			if err != nil {
				return err
			}
			if !ok || c.State != "ready" || !c.SnapshotComplete {
				return ErrEditorDraftForbidden
			}
			if r.Kind == "pull_review" && pull.CurrentComparisonID != n {
				return ErrEditorDraftForbidden
			}
		}
	default:
		return ErrEditorDraftForbidden
	}
	if r.Kind == "issue_new" {
		for _, key := range []string{"milestoneid", "assigneeid"} {
			v := p.Metadata[key]
			if v == "" || v == "0" {
				continue
			}
			id, err := strconv.ParseInt(v, 10, 64)
			if err != nil || id <= 0 {
				return ErrEditorDraftInvalidPayload
			}
			if !a.Write {
				return ErrEditorDraftForbidden
			}
			if key == "milestoneid" {
				var m Milestone
				ok, err := policyGet(e, locked, "milestone", "id = ? AND repo_id = ?", &m, id, r.RepositoryID)
				if err != nil {
					return err
				}
				if !ok {
					return ErrEditorDraftInvalidPayload
				}
			} else {
				var member Access
				ok, err := policyGet(e, locked, "access", "user_id = ? AND repo_id = ?", &member, id, r.RepositoryID)
				if err != nil {
					return err
				}
				if !ok {
					return ErrEditorDraftInvalidPayload
				}
			}
		}
	}
	return nil
}

// AuthorizeEditorDraft checks the live canonical target, never client URL identity.
func AuthorizeEditorDraft(ctx context.Context, actor, repo int64, kind string, item int64) (err error) {
	defer func() { err = draftStoreError(err) }()
	if err := ctx.Err(); err != nil {
		return err
	}
	r := &EditorDraft{ActorID: actor, RepositoryID: repo, Kind: kind, ItemID: item}
	p := editorDraftPayload{}
	e := draftEngine()
	source, err := draftSource(e, r, p)
	if err != nil {
		return err
	}
	r.SourceRepositoryID = source
	return draftAuthority(e, r, p, false)
}

// CheckEditorDraft is a status-only check, including an optional editable new
// PR candidate. A candidate is not a trusted previously-authorized dependency.
func CheckEditorDraft(ctx context.Context, actor, repo int64, kind string, item int64, metadata map[string]string) (err error) {
	defer func() { err = draftStoreError(err) }()
	if err = AuthorizeEditorDraft(ctx, actor, repo, kind, item); err != nil {
		return err
	}
	for key, value := range metadata {
		if kind != "pull_new" || key != "source_repository" || len(value) > 512 {
			return ErrEditorDraftInvalidPayload
		}
	}
	if kind != "pull_new" || metadata["source_repository"] == "" {
		return nil
	}
	e := draftEngine()
	r := &EditorDraft{ActorID: actor, RepositoryID: repo, Kind: kind}
	p := editorDraftPayload{Metadata: metadata}
	source, err := draftSource(e, r, p)
	if err != nil {
		return err
	}
	if source == 0 {
		return ErrEditorDraftInvalidPayload
	}
	r.SourceRepositoryID = source
	if err = draftAuthority(e, r, p, false); errors.Is(err, ErrEditorDraftForbidden) {
		if targetErr := AuthorizeEditorDraft(ctx, actor, repo, kind, item); targetErr != nil {
			return targetErr
		}
		return ErrEditorDraftInvalidPayload
	}
	return err
}
func draftTransaction(ctx context.Context, r *EditorDraft, p editorDraftPayload, apply func(*xorm.Session) error) error {
	e := draftEngine()
	previousSource := r.SourceRepositoryID
	source, err := draftSource(e, r, p)
	if err != nil {
		return err
	}
	ids := []int64{r.RepositoryID}
	if source > 0 {
		ids = append(ids, source)
	}
	if r.SourceRepositoryID > 0 {
		ids = append(ids, r.SourceRepositoryID)
	}
	err = pullTransactionEngine(e, ctx, ids, func(s *xorm.Session) error {
		if r.Kind == "pull_new" {
			// Input edits never bypass established target/source authority.
			prior := *r
			prior.SourceRepositoryID = previousSource
			if err := draftAuthority(s, &prior, editorDraftPayload{}, true); err != nil {
				return err
			}
		}
		if previousSource > 0 {
			a, err := repositoryAccessFor(s, r.ActorID, previousSource, true)
			if err != nil {
				return draftDenied(err)
			}
			if !a.Read {
				return ErrEditorDraftForbidden
			}
		}
		current, err := draftSource(s, r, p)
		if err != nil {
			return err
		}
		if current != source {
			return ErrEditorDraftForbidden
		}
		if r.Kind == "pull_new" && p.Metadata["source_repository"] != "" && source == 0 {
			return ErrEditorDraftInvalidPayload
		}
		r.SourceRepositoryID = source
		if err := draftAuthority(s, r, p, true); err != nil {
			return err
		}
		return apply(s)
	})
	if r.Kind == "pull_new" && source != previousSource && errors.Is(draftDenied(err), ErrEditorDraftForbidden) {
		// A different candidate can fail its ready-row lock before the write
		// callback. Recheck only the established authority under its own sorted
		// locks to classify that denial. Never call apply or retry a mutation.
		prior := *r
		prior.SourceRepositoryID = previousSource
		priorIDs := []int64{r.RepositoryID}
		if previousSource > 0 {
			priorIDs = append(priorIDs, previousSource)
		}
		return draftDenied(pullTransactionEngine(e, ctx, priorIDs, func(s *xorm.Session) error {
			if err := draftAuthority(s, &prior, editorDraftPayload{}, true); err != nil {
				return err
			}
			return ErrEditorDraftInvalidPayload
		}))
	}
	return draftDenied(err)
}
func (s *EditorDraftStore) Create(ctx context.Context, r *EditorDraft, maxLive int, now time.Time) (err error) {
	defer func() { err = draftStoreError(err) }()
	now = draftQueryTime(now)
	p, err := draftPayload(r.PayloadJSON)
	if err != nil {
		return err
	}
	return draftTransaction(ctx, r, p, func(tx *xorm.Session) error {
		var n int64
		var err error
		if DbCfg.Type == "sqlite3" {
			n, err = tx.Where("actor_id = ? AND discarded = ? AND expires_at > ?", r.ActorID, false, now).Count(new(EditorDraft))
		} else {
			// MySQL may have established a snapshot during source resolution.
			// Count current locked rows after serializing on the account, rather
			// than using that earlier snapshot for the cross-repository cap.
			var live []EditorDraft
			err = tx.Sql("SELECT page_id FROM editor_draft WHERE actor_id = ? AND discarded = ? AND expires_at > ? FOR UPDATE", r.ActorID, false, now).Find(&live)
			n = int64(len(live))
		}
		if err != nil {
			return err
		}
		if n >= int64(maxLive) {
			return ErrEditorDraftLimitExceeded
		}
		_, err = tx.Insert(r)
		return err
	})
}
func (s *EditorDraftStore) Find(ctx context.Context, page string) (result *EditorDraft, err error) {
	defer func() { err = draftStoreError(err) }()
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	var r EditorDraft
	ok, err := draftEngine().Id(page).Get(&r)
	if err != nil {
		return nil, err
	}
	if !ok {
		return nil, ErrEditorDraftExpired
	}
	if r.Discarded {
		return &r, nil
	}
	p, err := draftPayload(r.PayloadJSON)
	if err != nil {
		return nil, err
	}
	err = draftTransaction(ctx, &r, p, func(tx *xorm.Session) error {
		var current EditorDraft
		ok, err := policyGet(tx, true, "editor_draft", "page_id = ?", &current, page)
		if err != nil {
			return err
		}
		if !ok {
			return ErrEditorDraftExpired
		}
		if current.Version != r.Version || current.Discarded {
			return ErrEditorDraftStaleVersion
		}
		r = current
		return nil
	})
	if err != nil {
		return nil, err
	}
	return &r, nil
}
func (s *EditorDraftStore) SaveIfLive(ctx context.Context, page string, expected, next int64, payload string, expiry, now time.Time) (err error) {
	defer func() { err = draftStoreError(err) }()
	now = draftQueryTime(now)
	var r EditorDraft
	ok, err := draftEngine().Id(page).Get(&r)
	if err != nil {
		return err
	}
	if !ok {
		return ErrEditorDraftExpired
	}
	p, err := draftPayload(payload)
	if err != nil {
		return err
	}
	return draftTransaction(ctx, &r, p, func(tx *xorm.Session) error {
		var current EditorDraft
		ok, err := policyGet(tx, true, "editor_draft", "page_id = ?", &current, page)
		if err != nil {
			return err
		}
		if !ok {
			return ErrEditorDraftExpired
		}
		if current.Discarded {
			return ErrEditorDraftDiscarded
		}
		if !current.ExpiresAt.After(now) {
			return ErrEditorDraftExpired
		}
		if current.Version != expected || next != expected+1 {
			return ErrEditorDraftStaleVersion
		}
		n, err := tx.Where("page_id = ? AND version = ? AND discarded = ? AND expires_at > ?", page, expected, false, now).Cols("version", "payload_json", "expires_at", "source_repository_id").Update(&EditorDraft{Version: next, PayloadJSON: payload, ExpiresAt: expiry, SourceRepositoryID: r.SourceRepositoryID})
		if err != nil {
			return err
		}
		if n != 1 {
			return ErrEditorDraftStaleVersion
		}
		return nil
	})
}
func (s *EditorDraftStore) DiscardIfToken(ctx context.Context, page string, hash []byte) (err error) {
	defer func() { err = draftStoreError(err) }()
	if err := ctx.Err(); err != nil {
		return err
	}
	var r EditorDraft
	ok, err := draftEngine().Id(page).Get(&r)
	if err != nil || !ok {
		return err
	}
	if len(hash) != 32 || subtle.ConstantTimeCompare(r.DisposalTokenHash, hash) != 1 {
		return ErrEditorDraftForbidden
	}
	_, err = draftEngine().Where("page_id = ?", page).Cols("payload_json", "discarded").Update(&EditorDraft{Discarded: true})
	return err
}
func (s *EditorDraftStore) PurgeExpired(ctx context.Context, now time.Time) (err error) {
	defer func() { err = draftStoreError(err) }()
	if err := ctx.Err(); err != nil {
		return err
	}
	_, err = draftEngine().Where("expires_at <= ?", draftQueryTime(now)).Delete(new(EditorDraft))
	return err
}
