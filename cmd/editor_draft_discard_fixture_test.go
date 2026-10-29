package cmd

import (
	"context"
	"testing"
	"time"

	"github.com/go-xorm/xorm"
	"github.com/gogits/gogs/models"
)

type draftFixtureDiscardObserver struct {
	ctx    context.Context
	cancel context.CancelFunc
	pages  chan string
	done   chan struct{}
}

func startDraftFixtureDiscardObserver(t *testing.T, owner context.Context, db *xorm.Engine) *draftFixtureDiscardObserver {
	ctx, cancel := context.WithCancel(owner)
	o := &draftFixtureDiscardObserver{ctx: ctx, cancel: cancel, pages: make(chan string, 1), done: make(chan struct{})}
	t.Cleanup(o.stop)
	go func() {
		defer close(o.done)
		var page string
		var row models.EditorDraft
		for {
			select {
			case <-ctx.Done():
				return
			case page = <-o.pages:
			}
			if ctx.Err() != nil {
				return
			}
			row = models.EditorDraft{}
			found, err := db.Id(page).Cols("version", "discarded", "expires_at").Where("payload_json <> ?", "").Get(&row)
			if err != nil {
				t.Error("DRAFT_DROP_DISCARD: retained-row lookup failed")
				return
			}
			now := time.Now()
			if !found || row.Version < 1 || row.Discarded || !row.ExpiresAt.After(now) {
				t.Log("DRAFT_DROP_DISCARD: phase=ignored saved_live_row=false")
				continue
			}
			if row.ExpiresAt.After(now.Add(30 * time.Minute)) {
				t.Error("DRAFT_DROP_DISCARD: retained expiry exceeds production TTL")
				return
			}
			break
		}
		t.Log("DRAFT_DROP_DISCARD: phase=retained connection_closed=true native_discard_invoked=false saved=true live=true expiry_within_production_ttl=true")
		if ctx.Err() != nil {
			return
		}
		// Verify the same UTC comparison used by the SQLite draft store before
		// committing eligibility, so the purger cannot race this receipt.
		tx := db.NewSession()
		defer tx.Close()
		if err := tx.Begin(); err != nil {
			t.Error("DRAFT_DROP_DISCARD: owned aging transaction failed")
			return
		}
		defer tx.Rollback()
		now := time.Now().UTC()
		changed, err := tx.Where("page_id = ? AND version = ? AND discarded = ? AND expires_at > ?", page, row.Version, false, now).Cols("expires_at").Update(&models.EditorDraft{ExpiresAt: now.Add(-time.Second)})
		if err != nil || changed != 1 {
			t.Error("DRAFT_DROP_DISCARD: selected saved row was not made eligible")
			return
		}
		eligible, err := tx.Where("page_id = ? AND expires_at <= ?", page, now).Count(new(models.EditorDraft))
		if err != nil || eligible != 1 {
			t.Error("DRAFT_DROP_DISCARD: aged row did not match the owned purge predicate")
			return
		}
		if err := tx.Commit(); err != nil {
			t.Error("DRAFT_DROP_DISCARD: owned aging commit failed")
			return
		}
		t.Log("DRAFT_DROP_DISCARD: phase=eligible controlled_row_aging=true only_expires_at_changed=true purge_predicate=true")
		ticker := time.NewTicker(100 * time.Millisecond)
		defer ticker.Stop()
		deadline := time.NewTimer(2 * time.Minute)
		defer deadline.Stop()
		for {
			if ctx.Err() != nil {
				return
			}
			var current models.EditorDraft
			found, err := db.Id(page).Cols("version", "discarded", "expires_at").Get(&current)
			if err != nil {
				t.Error("DRAFT_DROP_DISCARD: owned purge observation failed")
				return
			}
			if !found {
				t.Log("DRAFT_DROP_DISCARD: phase=purged retained_row_absent=true")
				return
			}
			if current.ExpiresAt.After(time.Now()) || current.Version != row.Version || current.Discarded {
				t.Error("DRAFT_DROP_DISCARD: aged row changed before owned purge")
				return
			}
			select {
			case <-ctx.Done():
				return
			case <-deadline.C:
				t.Error("DRAFT_DROP_DISCARD: owned purger did not remove eligible row within bound")
				return
			case <-ticker.C:
			}
		}
	}()
	return o
}

func (o *draftFixtureDiscardObserver) dropped(page string) {
	select {
	case <-o.ctx.Done():
	case <-o.done:
	case o.pages <- page:
	default:
	}
}

func (o *draftFixtureDiscardObserver) stop() {
	o.cancel()
	<-o.done
}
