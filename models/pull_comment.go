package models

import (
	"context"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/go-xorm/xorm"
)

// PullComment is user Markdown stored as data. A comment with ComparisonID set
// is an inline thread root anchored to that immutable comparison's base or
// source side; it is outdated once the pull's current comparison moves on.
type PullComment struct {
	ID           int64  `xorm:"pk autoincr 'id'"`
	PullID       int64  `xorm:"index 'pull_id'"`
	AuthorID     int64  `xorm:"'author_id'"`
	ComparisonID int64  `xorm:"'comparison_id'"`
	Path         string `xorm:"TEXT"`
	Side         string `xorm:"VARCHAR(8)"`
	Line         int
	Body         string `xorm:"MEDIUMTEXT"`
	Resolved     bool
	Created      time.Time `xorm:"created"`
	Outdated     bool      `xorm:"-"`
}

type PullCommentAnchor struct {
	ComparisonID int64
	Path, Side   string
	Line         int
}

func validPullComment(body string) bool {
	// Markdown code examples are data; the final Markdown sanitizer is the
	// active-content boundary, just as it is for the pull description.
	return utf8.ValidString(body) && strings.TrimSpace(body) != "" && len(body) <= 64*1024
}

// AddPullComment lets any reader of both endpoints comment. The anchor must
// already be validated against the comparison's content by the caller.
func AddPullComment(ctx context.Context, actor, pull int64, body string, a *PullCommentAnchor) (int64, error) {
	if !validPullComment(body) {
		return 0, ErrPullInvalid
	}
	c := PullComment{PullID: pull, AuthorID: actor, Body: body}
	if a != nil {
		if a.ComparisonID <= 0 || a.Line < 1 || (a.Side != "base" && a.Side != "source") || a.Path == "" {
			return 0, ErrPullInvalid
		}
		c.ComparisonID, c.Path, c.Side, c.Line = a.ComparisonID, a.Path, a.Side, a.Line
	}
	err := mutatePull(ctx, actor, pull, func(s *xorm.Session, p PullRequest, _, _ RepositoryAccess) error {
		if a != nil {
			if ok, err := s.Where("id = ? AND pull_id = ? AND state = ? AND snapshot_complete = ?", a.ComparisonID, p.ID, "ready", true).Get(new(PullComparison)); err != nil {
				return err
			} else if !ok {
				return ErrPullInvalid
			}
		}
		if _, err := s.Insert(&c); err != nil {
			return err
		}
		return pullEvent(s, p.ID, actor, 0, c.ComparisonID, "commented", "")
	})
	return c.ID, err
}

func ListPullComments(ctx context.Context, actor, pull int64) ([]PullComment, error) {
	p, err := GetPullRecordByID(ctx, actor, pull)
	if err != nil {
		return nil, err
	}
	var rows []PullComment
	if err = x.Where("pull_id = ?", pull).Asc("id").Find(&rows); err != nil {
		return nil, err
	}
	for i := range rows {
		rows[i].Outdated = rows[i].ComparisonID != 0 && rows[i].ComparisonID != p.CurrentComparisonID
	}
	return rows, nil
}

// ResolvePullThread lets the thread author and target writers resolve or reopen
// an inline thread. It never touches formal reviews.
func ResolvePullThread(ctx context.Context, actor, pull, thread int64, resolved bool) error {
	return mutatePull(ctx, actor, pull, func(s *xorm.Session, p PullRequest, _, target RepositoryAccess) error {
		var c PullComment
		if ok, err := s.Where("id = ? AND pull_id = ? AND comparison_id > 0", thread, p.ID).Get(&c); err != nil {
			return err
		} else if !ok {
			return ErrPullNotExist
		}
		if c.AuthorID != actor && !target.Write {
			return ErrInvalidCredential
		}
		c.Resolved = resolved
		if _, err := s.Id(c.ID).Cols("resolved").Update(&c); err != nil {
			return err
		}
		kind := "thread-reopened"
		if resolved {
			kind = "thread-resolved"
		}
		return pullEvent(s, p.ID, actor, 0, c.ComparisonID, kind, "")
	})
}
