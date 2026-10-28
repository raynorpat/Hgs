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
	_, err := e.Insert(&PullEvent{PullID: pull, ActorID: actor, ReviewerID: reviewer, ComparisonID: comparison, Kind: kind, Reason: reason})
	return err
}
