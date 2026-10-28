package models

import (
	"context"
	"net"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/gogits/gogs/modules/hg"
)

// This fixture is opt-in and refuses any existing schema. Run it alone against
// a newly provisioned loopback gogs_test_ database; it never reads app config.
func TestPullReviewMySQLRepeatableRead(t *testing.T) {
	if os.Getenv("GOGS_TEST_DB_BACKEND") != "mysql" {
		t.Skip("MySQL repeatable-read runtime unverified: no explicit disposable MySQL fixture configured")
	}
	oldX, oldDB, oldHas := x, DbCfg, HasEngine
	t.Cleanup(func() {
		if x != oldX {
			x.Close()
		}
		x, DbCfg, HasEngine = oldX, oldDB, oldHas
	})
	DbCfg.Type = "mysql"
	DbCfg.Host = os.Getenv("GOGS_TEST_DB_HOST")
	DbCfg.Name = os.Getenv("GOGS_TEST_DB_NAME")
	DbCfg.User = os.Getenv("GOGS_TEST_DB_USER")
	DbCfg.Passwd = os.Getenv("GOGS_TEST_DB_PASSWORD")
	host, _, err := net.SplitHostPort(DbCfg.Host)
	ip := net.ParseIP(host)
	if err != nil || ip == nil || !ip.IsLoopback() || !strings.HasPrefix(DbCfg.Name, "gogs_test_") || DbCfg.User == "" {
		t.Fatal("requires explicit loopback host:port, gogs_test_ database, and test user")
	}
	e, err := getEngine()
	if err != nil {
		t.Fatal("cannot open disposable MySQL fixture")
	}
	metas, err := e.DBMetas()
	if err != nil {
		e.Close()
		t.Fatal("cannot inspect disposable MySQL fixture")
	}
	if len(metas) != 0 {
		e.Close()
		t.Fatal("refusing nonempty MySQL database")
	}
	if err := e.StoreEngine("InnoDB").Sync2(tables...); err != nil {
		e.Close()
		t.Fatal("cannot initialize disposable MySQL fixture")
	}
	x, HasEngine = e, true

	owner := newAccessTestUser(t, "mysql-owner", true)
	author := newAccessTestUser(t, "mysql-author", true)
	reviewer := newAccessTestUser(t, "mysql-reviewer", true)
	target := newAccessTestRepo(t, owner, "target", false)
	source := newAccessTestRepo(t, author, "source", false)
	if _, err := x.Insert(&ForkRelation{RepositoryID: source.Id, SourceID: target.Id, FamilyID: target.Id}); err != nil {
		t.Fatal(err)
	}
	if err := AddAccess(&Access{UserId: reviewer.Id, RepoId: target.Id, Mode: WRITABLE}); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	p, err := CreatePullRecord(ctx, author.Id, PullRequest{SourceRepoID: source.Id, TargetRepoID: target.Id, Title: "Repeatable read", SourceSelection: `{"Ref":{"Kind":"node","Name":"1111111111111111111111111111111111111111"}}`, TargetSelection: `{"Ref":{"Kind":"node","Name":"2222222222222222222222222222222222222222"}}`})
	if err != nil {
		t.Fatal(err)
	}
	c, err := ReservePullComparison(ctx, author.Id, p.ID)
	if err != nil {
		t.Fatal(err)
	}
	c.SourceNode, c.TargetNode, c.BaseNode = hg.NodeID(strings.Repeat("1", 40)), hg.NodeID(strings.Repeat("2", 40)), hg.NodeID(strings.Repeat("3", 40))
	c.State, c.SnapshotComplete = "ready", true
	if err := CompletePullComparison(ctx, author.Id, c); err != nil {
		t.Fatal(err)
	}
	if err := AssignPullReviewers(ctx, author.Id, p.ID, []int64{reviewer.Id}); err != nil {
		t.Fatal(err)
	}

	// Fix the ordering without sleeps: the loss transaction establishes its
	// snapshot while the review is inactive, then a real submission commits on
	// another connection. This is the stale snapshot left after the reported
	// UPDATE wait, without relying on scheduler timing or lock-observer privileges.
	loss := x.NewSession()
	defer loss.Close()
	defer loss.Rollback()
	if err := loss.Begin(); err != nil {
		t.Fatal(err)
	}
	var isolation struct{ Level string }
	if ok, err := loss.Sql("SELECT @@transaction_isolation AS level").Get(&isolation); err != nil || !ok || isolation.Level != "REPEATABLE-READ" {
		t.Fatal("fixture requires MySQL repeatable-read isolation")
	}
	var before PullReview
	if ok, err := loss.Where("pull_id = ? AND reviewer_id = ?", p.ID, reviewer.Id).Get(&before); err != nil || !ok || before.State != ReviewInactive {
		t.Fatalf("initial snapshot: %+v %v", before, err)
	}
	start, submitted := make(chan struct{}), make(chan error, 1)
	go func() {
		<-start
		submitted <- SubmitPullReview(ctx, reviewer.Id, p.ID, c.ID, ReviewChangesRequested)
	}()
	close(start)
	select {
	case err := <-submitted:
		if err != nil {
			t.Fatal(err)
		}
	case <-ctx.Done():
		t.Fatal("submission did not complete")
	}
	var snapshot PullReview
	if _, err := loss.Where("pull_id = ? AND reviewer_id = ?", p.ID, reviewer.Id).Get(&snapshot); err != nil || snapshot.State != ReviewInactive {
		t.Fatalf("fixture did not retain old snapshot: %+v %v", snapshot, err)
	}
	if err := addAccessWithAuthorize(loss, &Access{UserId: reviewer.Id, RepoId: target.Id}, READABLE); err != nil {
		t.Fatal(err)
	}
	if err := commitRetiringReviews(loss); err != nil {
		t.Fatal(err)
	}
	// No list/read retirement between revoke and regrant.
	if err := AddAccess(&Access{UserId: reviewer.Id, RepoId: target.Id, Mode: WRITABLE}); err != nil {
		t.Fatal(err)
	}
	if got := reviewState(t, p.ID, reviewer.Id); got != ReviewInactive {
		t.Fatalf("old snapshot let a decision survive revoke/regrant: %s", got)
	}
	var events []PullEvent
	if err := x.Where("pull_id = ? AND kind = ?", p.ID, "review-authority-lost").Find(&events); err != nil || len(events) != 1 {
		t.Fatalf("retirement audit: %+v %v", events, err)
	}
	if err := SubmitPullReview(ctx, reviewer.Id, p.ID, c.ID, ReviewChangesRequested); err != nil {
		t.Fatalf("fresh review after regrant: %v", err)
	}
}
