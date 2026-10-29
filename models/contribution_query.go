// Copyright 2026 The Gogs Authors. All rights reserved.
// Use of this source code is governed by a MIT-style
// license that can be found in the LICENSE file.

package models

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"database/sql/driver"
	"errors"
	"fmt"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/go-xorm/core"
	"github.com/go-xorm/xorm"
)

// Query rows contain only authorized presentation data, never author addresses.
type ContributionQuery struct {
	Year, Limit                      int
	Day, Kind, AfterMonth, AfterKind string
	AfterRepository                  int64
	Cutoff                           time.Time
}
type ContributionDay struct {
	Date, Kind string
	N          int
}
type ContributionLink struct {
	RepositoryID int64
	Label, URL   string
}
type ContributionGroup struct {
	Month, Kind, Title, Status string
	RepositoryID               int64 `xorm:"'repository_id'"`
	N                          int
	Links                      []ContributionLink
}
type ContributionResult struct {
	Days            []ContributionDay
	Groups          []ContributionGroup
	FirstYear       int
	Revision, State string
	IndexedAt       time.Time
}

var ErrContributionChanged = errors.New("contributions: snapshot changed")

type contributionReadConnector struct {
	backend driver.Driver
	dsn     string
}

func (c contributionReadConnector) Driver() driver.Driver { return c.backend }
func (c contributionReadConnector) Connect(ctx context.Context) (driver.Conn, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	return c.backend.Open(c.dsn)
}

// The connection is pinned before configuring isolation. In particular, MySQL
// SET TRANSACTION is neither issued after BEGIN nor on another pooled connection.
func contributionReadSession(ctx context.Context) (*xorm.Session, func(), error) {
	e := contributionEngine()
	db := e.DB().DB
	var owned *sql.DB
	if DbCfg.Type == "sqlite3" {
		owned = sql.OpenDB(contributionReadConnector{e.DB().DB.Driver(), "file:" + DbCfg.Path + "?cache=private&mode=ro&_pragma=busy_timeout(2000)"})
		db = owned
	}
	closeDB := func() {
		if owned != nil {
			owned.Close()
		}
	}
	conn, err := db.Conn(ctx)
	if err != nil {
		closeDB()
		return nil, nil, contributionError(err)
	}
	if DbCfg.Type == "mysql" {
		if _, err = conn.ExecContext(ctx, "SET TRANSACTION ISOLATION LEVEL REPEATABLE READ"); err != nil {
			conn.Close()
			closeDB()
			return nil, nil, contributionError(err)
		}
	}
	tx, err := conn.BeginTx(ctx, nil)
	if err != nil {
		conn.Close()
		closeDB()
		return nil, nil, contributionError(err)
	}
	if DbCfg.Type == "postgres" {
		if _, err = tx.ExecContext(ctx, "SET TRANSACTION ISOLATION LEVEL REPEATABLE READ READ ONLY"); err != nil {
			tx.Rollback()
			conn.Close()
			closeDB()
			return nil, nil, contributionError(err)
		}
	}
	s := e.NewSession()
	s.Db = e.DB()
	s.Tx = &core.Tx{Tx: tx, Mapper: e.DB().Mapper}
	s.IsAutoCommit = false
	return s, func() { s.Rollback(); s.Close(); conn.Close(); closeDB() }, nil
}

func contributionIDs(ids []int64) string {
	if len(ids) == 0 {
		return "0"
	}
	var b strings.Builder
	for i, id := range ids {
		if i > 0 {
			b.WriteByte(',')
		}
		b.WriteString(strconv.FormatInt(id, 10))
	}
	return b.String()
}

// Repository count, not event count, bounds authorization work. Reuse the
// central policy on this same snapshot; the callback is an additional denial.
func contributionPreflight(ctx context.Context, viewer int64, gate func(context.Context, int64, int64) (bool, error)) (map[int64]bool, error) {
	decisions := map[int64]bool{}
	if gate == nil {
		return decisions, nil
	}
	for after := int64(0); ; {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		var batch []struct {
			ID int64 `xorm:"'id'"`
		}
		if err := contributionEngine().Sql("SELECT id FROM repository WHERE id > ? AND state = ? ORDER BY id LIMIT 100", after, RepositoryReady).Find(&batch); err != nil {
			return nil, err
		}
		for _, r := range batch {
			after = r.ID
			allow, err := gate(ctx, viewer, r.ID)
			if err != nil {
				return nil, err
			}
			decisions[r.ID] = allow
		}
		if len(batch) < 100 {
			return decisions, nil
		}
	}
}

func contributionReadable(ctx context.Context, s *xorm.Session, viewer, owner int64, org bool, decisions map[int64]bool) ([]int64, map[int64]Repository, error) {
	ids := []int64{}
	repos := map[int64]Repository{}
	unknown := []Repository{}
	for after := int64(0); ; {
		if err := ctx.Err(); err != nil {
			return nil, nil, err
		}
		var batch []Repository
		if err := s.Where("id > ? AND state = ?", after, RepositoryReady).Asc("id").Limit(100).Find(&batch); err != nil {
			return nil, nil, err
		}
		for _, r := range batch {
			after = r.Id
			a, err := repositoryAccessFor(s, viewer, r.Id, false)
			if err == ErrForkLineage || err == ErrRepoNotExist || err == ErrUserNotExist {
				continue
			}
			if err != nil {
				return nil, nil, err
			}
			if !a.Read {
				continue
			}
			allowed, known := decisions[r.Id]
			if !known {
				unknown = append(unknown, r)
				continue
			}
			if !allowed {
				continue
			}
			var owner User
			ok, err := s.Id(r.OwnerId).Get(&owner)
			if err != nil {
				return nil, nil, err
			}
			if !ok {
				continue
			}
			r.Owner = &owner
			repos[r.Id] = r
			ids = append(ids, r.Id)
		}
		if len(batch) < 100 {
			break
		}
	}
	for _, r := range unknown {
		if r.OwnerId == owner {
			return nil, nil, ErrContributionChanged
		}
		var relevant struct{ N int }
		condition := "p.target_repo_id IN (" + contributionIDs(ids) + ")"
		if org {
			condition += " AND target.owner_id = " + strconv.FormatInt(owner, 10)
		} else {
			condition += " AND (p.author_id = " + strconv.FormatInt(owner, 10) + " OR EXISTS (SELECT 1 FROM contribution_first_review f WHERE f.pull_id = p.id AND f.reviewer_id = " + strconv.FormatInt(owner, 10) + "))"
		}
		query := "SELECT COUNT(*) AS n FROM pull_request p INNER JOIN repository target ON target.id = p.target_repo_id WHERE p.source_deleted = false AND p.source_repo_id = ? AND " + condition
		if _, err := s.Sql(query, r.Id).Get(&relevant); err != nil {
			return nil, nil, err
		}
		if relevant.N > 0 {
			return nil, nil, ErrContributionChanged
		}
		if !org {
			// Relevance itself must enforce canonical endpoint visibility; a raw
			// stored actor match here would turn private PRs into cursor churn.
			candidates := append(append([]int64{}, ids...), r.Id)
			eligible, eligibleArgs := contributionRelation(owner, false, candidates, ContributionQuery{}, true)
			query = "SELECT COUNT(*) AS n FROM (" + eligible + ") d WHERE d.repository_id = ?"
			if _, err := s.Sql(query, append(eligibleArgs, r.Id)...).Get(&relevant); err != nil {
				return nil, nil, err
			}
			if relevant.N > 0 {
				return nil, nil, ErrContributionChanged
			}
			if _, err := s.Sql("SELECT COUNT(*) AS n FROM issue WHERE repo_id = ? AND poster_id = ? AND is_pull = false", r.Id, owner).Get(&relevant); err != nil {
				return nil, nil, err
			}
			if relevant.N > 0 {
				return nil, nil, ErrContributionChanged
			}
		}
	}
	return ids, repos, nil
}

// Current uniqueness includes primary and additional addresses, including
// unconfirmed competing ownership. Activation flags are not ownership proof.
func contributionProofSQL(alias string, owner int64) string {
	id := strconv.FormatInt(owner, 10)
	return "EXISTS (SELECT 1 FROM email_verification v WHERE v.user_id = " + id + " AND v.normalized_email = " + alias + ".author_email AND EXISTS (SELECT 1 FROM `user` u WHERE u.id = v.user_id AND u.type = 0 AND (lower(trim(u.email)) = v.normalized_email OR EXISTS (SELECT 1 FROM email_address a WHERE a.uid = u.id AND lower(trim(a.email)) = v.normalized_email))) AND NOT EXISTS (SELECT 1 FROM `user` u WHERE u.type = 0 AND u.id <> v.user_id AND lower(trim(u.email)) = v.normalized_email) AND NOT EXISTS (SELECT 1 FROM email_address a INNER JOIN `user` u ON u.id = a.uid WHERE u.type = 0 AND u.id <> v.user_id AND lower(trim(a.email)) = v.normalized_email))"
}

func contributionRelation(owner int64, org bool, ids []int64, q ContributionQuery, unprojected bool) (string, []interface{}) {
	readable := contributionIDs(ids)
	from := " FROM contribution_event e INNER JOIN contribution_repository m ON m.event_id = e.id INNER JOIN repository r ON r.id = m.repository_id LEFT JOIN contribution_index_state st ON st.repository_id = r.id LEFT JOIN issue i ON e.kind = 'issue' AND i.id = e.object_id LEFT JOIN pull_request p ON e.kind IN ('pull','review') AND p.id = e.object_id LEFT JOIN contribution_first_review fr ON e.kind = 'review' AND fr.pull_id = e.object_id AND fr.reviewer_id = e.actor_id"
	where := " WHERE r.id IN (" + readable + ") AND r.state = 'ready'"
	args := []interface{}{}
	if !unprojected {
		where += " AND e.utc_date <> '' AND e.occurred_unix >= ? AND e.occurred_unix <= ?"
		args = []interface{}{time.Time{}.Unix() + 1, q.Cutoff.Unix()}
	}
	where += " AND (" +
		"(e.kind = 'changeset' AND st.generation > 0 AND m.generation = st.generation AND length(e.node) = 40) OR " +
		"(e.kind = 'repository' AND e.object_id = r.id AND r.creator_id > 0) OR " +
		"(e.kind = 'issue' AND i.repo_id = r.id AND i.is_pull = false) OR " +
		"(e.kind IN ('pull','review') AND p.target_repo_id = r.id AND (e.kind = 'pull' OR fr.id IS NOT NULL) AND ((p.source_deleted = false AND p.source_repo_id IN (" + readable + ")) OR (p.source_deleted = true AND p.state = 'merged' AND NOT EXISTS (SELECT 1 FROM repository source WHERE source.id = p.source_repo_id)))))"
	if org {
		where += " AND r.owner_id = " + strconv.FormatInt(owner, 10)
	} else {
		id := strconv.FormatInt(owner, 10)
		where += " AND EXISTS (SELECT 1 FROM `user` subject WHERE subject.id = " + id + " AND subject.type = 0) AND ((e.kind = 'changeset' AND " + contributionProofSQL("e", owner) + ") OR (e.kind = 'issue' AND i.poster_id = " + id + ") OR (e.kind = 'pull' AND p.author_id = " + id + ") OR (e.kind = 'review' AND fr.reviewer_id = " + id + ") OR (e.kind = 'repository' AND r.creator_id = " + id + "))"
	}
	// Deduplication occurs only after every access, proof and scope predicate.
	status := "CASE WHEN e.kind = 'issue' THEN CASE WHEN i.is_closed = true THEN 'closed' ELSE 'open' END WHEN e.kind IN ('pull','review') THEN p.state ELSE '' END"
	return "SELECT e.id,e.kind,e.occurred_unix,e.utc_date,e.utc_month,e.object_id,e.node," + status + " AS status,MIN(r.id) AS repository_id" + from + where + " GROUP BY e.id,e.kind,e.occurred_unix,e.utc_date,e.utc_month,e.object_id,e.node,i.is_closed,p.state", args
}

func contributionFiltered(relation string, args []interface{}, q ContributionQuery) (string, []interface{}) {
	day := "d.utc_date"
	where := " WHERE " + day + " >= ? AND " + day + " <= ?"
	args = append(append([]interface{}{}, args...), fmt.Sprintf("%04d-01-01", q.Year), fmt.Sprintf("%04d-12-31", q.Year))
	if q.Day != "" {
		where += " AND " + day + " = ?"
		args = append(args, q.Day)
	}
	if q.Kind != "" {
		where += " AND d.kind = ?"
		args = append(args, q.Kind)
	}
	return " FROM (" + relation + ") d" + where, args
}

// ReadContributionQuery performs all policy, aggregation and keyset reads in
// one database snapshot. No authorization grant is retained between calls.
func ReadContributionQuery(ctx context.Context, viewer, owner int64, org bool, q ContributionQuery, gate func(context.Context, int64, int64) (bool, error)) (result ContributionResult, err error) {
	result.State = "ready"
	decisions, err := contributionPreflight(ctx, viewer, gate)
	if err != nil {
		return result, contributionError(err)
	}
	s, close, err := contributionReadSession(ctx)
	if err != nil {
		return result, err
	}
	defer close()
	var subject User
	found, err := s.Id(owner).Get(&subject)
	if err != nil {
		return result, contributionError(err)
	}
	if !found {
		return result, nil
	}
	if subject.IsOrganization() != org {
		return result, ErrContributionInvalid
	}
	ids, repos, err := contributionReadable(ctx, s, viewer, owner, org, decisions)
	if err != nil {
		return result, contributionError(err)
	}
	relation, args := contributionRelation(owner, org, ids, q, false)
	var earliest struct{ Date string }
	if _, err = s.Sql("SELECT MIN(d.utc_date) AS date FROM ("+relation+") d WHERE d.kind <> 'repository'", args...).Get(&earliest); err != nil {
		return result, contributionError(err)
	}
	if len(earliest.Date) >= 4 {
		result.FirstYear, _ = strconv.Atoi(earliest.Date[:4])
	}
	// Apply the selected year's indexed timestamp bounds before deduplication,
	// while the separate earliest-year aggregate still sees authorized history.
	yearArgs := append([]interface{}{}, args...)
	yearArgs[0] = time.Date(q.Year, 1, 1, 0, 0, 0, 0, time.UTC).Unix()
	yearEnd := time.Date(q.Year+1, 1, 1, 0, 0, 0, 0, time.UTC).Add(-time.Nanosecond)
	if yearEnd.Before(q.Cutoff) {
		yearArgs[1] = yearEnd.Unix()
	}
	filtered, filterArgs := contributionFiltered(relation, yearArgs, q)
	day := "d.utc_date"
	if err = s.Sql("SELECT "+day+" AS date,d.kind,COUNT(*) AS n"+filtered+" GROUP BY "+day+",d.kind ORDER BY date,d.kind", filterArgs...).Find(&result.Days); err != nil {
		return result, contributionError(err)
	}
	month := "d.utc_month"
	groupQuery := "SELECT " + month + " AS month,d.kind,d.repository_id,COUNT(*) AS n,CASE WHEN MIN(d.status) = MAX(d.status) THEN MIN(d.status) ELSE 'mixed' END AS status" + filtered
	groupArgs := append([]interface{}{}, filterArgs...)
	if q.AfterMonth != "" {
		groupQuery += " AND (" + month + " < ? OR (" + month + " = ? AND d.kind > ?) OR (" + month + " = ? AND d.kind = ? AND d.repository_id > ?))"
		groupArgs = append(groupArgs, q.AfterMonth, q.AfterMonth, q.AfterKind, q.AfterMonth, q.AfterKind, q.AfterRepository)
	}
	groupQuery += " GROUP BY " + month + ",d.kind,d.repository_id ORDER BY month DESC,d.kind,d.repository_id LIMIT " + strconv.Itoa(q.Limit+1)
	if err = s.Sql(groupQuery, groupArgs...).Find(&result.Groups); err != nil {
		return result, contributionError(err)
	}
	for n := range result.Groups {
		g := &result.Groups[n]
		r := repos[g.RepositoryID]
		g.Title = r.Owner.Name + "/" + r.Name
		link, linkErr := r.RepoLink()
		if linkErr != nil {
			return result, contributionError(linkErr)
		}
		var events []ContributionEvent
		query := "SELECT d.id,d.kind,d.object_id,d.node" + filtered + " AND " + month + " = ? AND d.kind = ? AND d.repository_id = ? ORDER BY d.occurred_unix DESC,d.id DESC LIMIT 25"
		linkArgs := append(append([]interface{}{}, filterArgs...), g.Month, g.Kind, g.RepositoryID)
		if err = s.Sql(query, linkArgs...).Find(&events); err != nil {
			return result, contributionError(err)
		}
		for _, event := range events {
			label, url := g.Title, link
			switch event.Kind {
			case "changeset":
				label = event.Node
				url += "/commit/" + event.Node
			case "issue":
				var issue Issue
				if _, err = s.Id(event.ObjectID).Get(&issue); err != nil {
					return result, contributionError(err)
				}
				label = issue.Name
				url += "/issues/" + strconv.FormatInt(issue.Index, 10)
			case "pull", "review":
				var pull PullRequest
				if _, err = s.Id(event.ObjectID).Get(&pull); err != nil {
					return result, contributionError(err)
				}
				label = pull.Title
				url += "/pulls/" + strconv.FormatInt(pull.Number, 10)
			}
			g.Links = append(g.Links, ContributionLink{RepositoryID: g.RepositoryID, Label: label, URL: url})
		}
	}
	result.Revision, err = contributionQueryRevision(s, owner, org, ids, repos, relation, args, &result)
	if err == nil && result.State == "ready" {
		legacy, legacyArgs := contributionRelation(owner, org, ids, q, true)
		var missing struct{ N int }
		_, err = s.Sql("SELECT COUNT(*) AS n FROM ("+legacy+") d WHERE d.utc_date IS NULL OR d.utc_date = ''", legacyArgs...).Get(&missing)
		if missing.N > 0 {
			result.State = "updating"
		}
	}
	var verified struct{ N int }
	if err == nil && !org {
		_, err = s.Sql("SELECT COUNT(*) AS n FROM (SELECT normalized_email AS author_email FROM email_verification WHERE user_id = ?) proof_address WHERE "+contributionProofSQL("proof_address", owner), owner).Get(&verified)
	}
	if err == nil && !org && verified.N > 0 {
		// Before readable native history is indexed, its authors are unknown.
		// Keep this completeness signal separate from the scoped cursor digest.
		var completeness struct {
			Cold, Missing, Failed int
			Oldest                time.Time
		}
		query := "SELECT MAX(CASE WHEN st.repository_id IS NULL OR st.generation = 0 OR st.updating = true OR j.requested > j.completed THEN 1 ELSE 0 END) AS cold,MAX(CASE WHEN st.repository_id IS NULL OR st.generation = 0 THEN 1 ELSE 0 END) AS missing,MAX(CASE WHEN st.unavailable = true THEN 1 ELSE 0 END) AS failed,MIN(st.last_success) AS oldest FROM repository r LEFT JOIN contribution_index_state st ON st.repository_id = r.id LEFT JOIN contribution_job j ON j.kind = 'repository' AND j.object_id = r.id WHERE r.id IN (" + contributionIDs(ids) + ")"
		_, err = s.Sql(query).Get(&completeness)
		if completeness.Failed > 0 {
			result.State = "unavailable"
		} else if completeness.Cold > 0 && result.State != "unavailable" {
			result.State = "updating"
		}
		if completeness.Missing > 0 {
			result.IndexedAt = time.Time{}
		} else if !completeness.Oldest.IsZero() && (result.IndexedAt.IsZero() || completeness.Oldest.Before(result.IndexedAt)) {
			result.IndexedAt = completeness.Oldest
		}
	}
	return result, contributionError(err)
}

func contributionQueryRevision(s *xorm.Session, owner int64, org bool, ids []int64, repos map[int64]Repository, relation string, args []interface{}, result *ContributionResult) (string, error) {
	h := sha256.New()
	readable := contributionIDs(ids)
	// Native state is scoped to owned repositories and currently eligible rows.
	var relevant []struct {
		RepositoryID int64 `xorm:"'repository_id'"`
	}
	query := "SELECT DISTINCT r.id AS repository_id FROM repository r WHERE r.id IN (" + readable + ") AND (r.owner_id = ? OR r.id IN (SELECT d.repository_id FROM (" + relation + ") d)) ORDER BY r.id"
	if org {
		query = "SELECT r.id AS repository_id FROM repository r WHERE r.id IN (" + readable + ") AND r.owner_id = ? ORDER BY r.id"
	}
	queryArgs := append([]interface{}{owner}, args...)
	if org {
		queryArgs = []interface{}{owner}
	}
	if err := s.Sql(query, queryArgs...).Find(&relevant); err != nil {
		return "", err
	}
	for _, row := range relevant {
		r := repos[row.RepositoryID]
		var st ContributionIndexState
		if _, err := s.Id(r.Id).Get(&st); err != nil {
			return "", err
		}
		fmt.Fprintf(h, "repo:%d:%d:%s:%s:%d:%d:%t:%t;", r.Id, r.OwnerId, r.Owner.Name, r.Name, st.Generation, st.NextGeneration, st.Updating, st.Unavailable)
		if st.Unavailable {
			result.State = "unavailable"
		} else if result.State != "unavailable" && (st.Generation == 0 || st.Updating) {
			result.State = "updating"
		}
		if !st.LastSuccess.IsZero() && (result.IndexedAt.IsZero() || st.LastSuccess.Before(result.IndexedAt)) {
			result.IndexedAt = st.LastSuccess
		}
		var job ContributionJob
		if _, err := s.Where("kind = ? AND object_id = ?", "repository", r.Id).Get(&job); err != nil {
			return "", err
		}
		if job.Requested > job.Completed && result.State == "ready" {
			result.State = "updating"
		}
	}
	classWhere := "c.repository_id IN (" + readable + ") AND (c.source_id = 0 OR c.source_id IN (" + readable + ")) AND c.revision > 0"
	if org {
		classWhere += " AND EXISTS (SELECT 1 FROM repository r WHERE r.id = c.repository_id AND r.owner_id = " + strconv.FormatInt(owner, 10) + ")"
	} else {
		classWhere += " AND EXISTS (SELECT 1 FROM `user` u WHERE u.id = " + strconv.FormatInt(owner, 10) + " AND u.type = 0) AND ((c.kind = 'changeset' AND " + contributionProofSQL("c", owner) + ") OR (c.kind <> 'changeset' AND c.actor_id = " + strconv.FormatInt(owner, 10) + "))"
	}
	// Classes are durable epochs, including tombstones. A mutation advances its
	// endpoint partition beyond every prior epoch. Aggregate in SQL so an org
	// with one distinct author per changeset does not materialize that history.
	for offset := 0; ; offset += 100 {
		var classes []ContributionClass
		if err := s.Sql("SELECT c.repository_id,c.source_id,c.kind,MAX(c.revision) AS revision FROM contribution_class c WHERE " + classWhere + " GROUP BY c.repository_id,c.source_id,c.kind ORDER BY c.repository_id,c.source_id,c.kind LIMIT 100 OFFSET " + strconv.Itoa(offset)).Find(&classes); err != nil {
			return "", err
		}
		for _, c := range classes {
			fmt.Fprintf(h, "class:%d:%d:%s:%d;", c.RepositoryID, c.SourceID, c.Kind, c.Revision)
		}
		if len(classes) < 100 {
			break
		}
	}
	// Include canonical objects not yet in the index. Checking indexed rows
	// alone falsely claims completeness while the very first job is pending.
	var pending struct{ N int }
	jobSQL := "SELECT COUNT(*) AS n FROM contribution_job j LEFT JOIN issue i ON j.kind = 'issue' AND i.id = j.object_id LEFT JOIN contribution_first_review f ON j.kind = 'review' AND f.id = j.object_id LEFT JOIN pull_request p ON (j.kind = 'pull' AND p.id = j.object_id) OR (j.kind = 'review' AND p.id = f.pull_id) INNER JOIN repository r ON (j.kind = 'issue' AND r.id = i.repo_id) OR (j.kind IN ('pull','review') AND r.id = p.target_repo_id) WHERE j.requested > j.completed AND r.id IN (" + readable + ") AND ((j.kind = 'issue' AND i.is_pull = false) OR (j.kind IN ('pull','review') AND ((p.source_deleted = false AND p.source_repo_id IN (" + readable + ")) OR (p.source_deleted = true AND p.state = 'merged' AND NOT EXISTS (SELECT 1 FROM repository source WHERE source.id = p.source_repo_id)))))"
	if org {
		jobSQL += " AND r.owner_id = " + strconv.FormatInt(owner, 10)
	} else {
		jobSQL += " AND ((j.kind = 'issue' AND i.poster_id = ?) OR (j.kind = 'pull' AND p.author_id = ?) OR (j.kind = 'review' AND f.reviewer_id = ?))"
	}
	jobArgs := []interface{}{}
	if !org {
		jobArgs = []interface{}{owner, owner, owner}
	}
	if _, err := s.Sql(jobSQL, jobArgs...).Get(&pending); err != nil {
		return "", err
	}
	if pending.N > 0 && result.State == "ready" {
		result.State = "updating"
	}
	// Live proof metadata is scoped to addresses on authorized class rows.
	if !org {
		var proofs []EmailVerification
		if err := s.Sql("SELECT v.* FROM email_verification v WHERE v.user_id = ? AND EXISTS (SELECT 1 FROM contribution_class c WHERE "+classWhere+" AND c.kind = 'changeset' AND c.author_email = v.normalized_email)", owner).Find(&proofs); err != nil {
			return "", err
		}
		sort.Slice(proofs, func(i, j int) bool { return proofs[i].NormalizedEmail < proofs[j].NormalizedEmail })
		for _, p := range proofs {
			fmt.Fprintf(h, "proof:%s:%d:%d;", p.NormalizedEmail, p.VerifiedAt.UnixNano(), p.Revision)
		}
	}
	return fmt.Sprintf("%x", h.Sum(nil)), nil
}
