package models

import "time"

// PullEvent is the durable, in-app audit history. It grants no authority.
type PullEvent struct {
	ID           int64     `xorm:"pk autoincr 'id'"`
	PullID       int64     `xorm:"index 'pull_id'"`
	ActorID      int64     `xorm:"'actor_id'"`
	ReviewerID   int64     `xorm:"'reviewer_id'"`
	ComparisonID int64     `xorm:"'comparison_id'"`
	Kind         string    `xorm:"VARCHAR(32)"`
	Reason       string    `xorm:"MEDIUMTEXT"`
	Created      time.Time `xorm:"created"`
}

func pullEvent(e Engine, pull, actor, reviewer, comparison int64, kind, reason string) error {
	row := &PullEvent{PullID: pull, ActorID: actor, ReviewerID: reviewer, ComparisonID: comparison, Kind: kind, Reason: reason}
	if _, err := e.Insert(row); err != nil {
		return err
	}
	if kind == "review-submitted" && actor == reviewer && (reason == string(ReviewApproved) || reason == string(ReviewChangesRequested)) {
		// Legacy xorm defers copying generated timestamps into the bean until
		// commit. Read the canonical value inside this transaction instead.
		if _, err := e.Id(row.ID).Get(row); err != nil {
			return err
		}
		return firstContributionReview(e, reviewer, pull, row.Created)
	}
	return nil
}
