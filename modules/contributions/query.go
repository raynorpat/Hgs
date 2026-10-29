// Copyright 2026 The Gogs Authors. All rights reserved.
// Use of this source code is governed by a MIT-style
// license that can be found in the LICENSE file.

package contributions

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/gogits/gogs/models"
)

type Scope struct {
	OwnerID      int64
	Organization bool
}
type Query struct {
	Year              int
	Day, Kind, Cursor string
	Limit             int
}
type Counts struct{ Changesets, Issues, Pulls, Reviews int }
type DayCount struct {
	Date   string
	Counts Counts
}
type EventLink struct {
	RepositoryID int64
	Label, URL   string
}
type ActivityGroup struct {
	Key, Date, Kind, Title, Status string
	Count                          int
	Links                          []EventLink
}
type Page struct {
	Days              []DayCount
	Totals            Counts
	Groups            []ActivityGroup
	Years             []int
	NextCursor, State string
	IndexedAt         time.Time
}

var ErrInvalidQuery = errors.New("contributions: invalid query")
var ErrStaleCursor = errors.New("contributions: activity changed; restart pagination")

func (s *Service) Read(ctx context.Context, viewerID int64, scope Scope, q Query) (Page, error) {
	now := s.now().UTC().Truncate(time.Second)
	if q.Year == 0 {
		q.Year = now.Year()
	}
	if q.Limit == 0 {
		q.Limit = 25
	}
	if scope.OwnerID <= 0 || viewerID < 0 || q.Year < 1 || q.Year > now.Year() || q.Limit < 1 || q.Limit > 50 {
		return Page{}, ErrInvalidQuery
	}
	if q.Kind != "" && q.Kind != "changeset" && q.Kind != "issue" && q.Kind != "pull" && q.Kind != "review" && q.Kind != "repository" {
		return Page{}, ErrInvalidQuery
	}
	if q.Day != "" {
		day, err := time.Parse("2006-01-02", q.Day)
		if err != nil || day.Year() != q.Year || day.After(now) {
			return Page{}, ErrInvalidQuery
		}
	}
	c := cursor{Viewer: viewerID, Owner: scope.OwnerID, Organization: scope.Organization, Year: q.Year, Day: q.Day, Kind: q.Kind, Cutoff: now.Unix()}
	if q.Cursor != "" {
		old, err := decodeCursor(q.Cursor)
		if err != nil || old.Viewer != c.Viewer || old.Owner != c.Owner || old.Organization != c.Organization || old.Year != c.Year || old.Day != c.Day || old.Kind != c.Kind || old.Cutoff > c.Cutoff || old.Cutoff <= 0 {
			return Page{}, ErrStaleCursor
		}
		c = old
	}
	data, err := models.ReadContributionQuery(ctx, viewerID, scope.OwnerID, scope.Organization, models.ContributionQuery{Year: q.Year, Limit: q.Limit, Day: q.Day, Kind: q.Kind, Cutoff: time.Unix(c.Cutoff, 0).UTC(), AfterMonth: c.Month, AfterKind: c.GroupKind, AfterRepository: c.Repository}, s.queryRead)
	if err != nil {
		if errors.Is(err, models.ErrContributionChanged) {
			return Page{}, ErrStaleCursor
		}
		if errors.Is(err, models.ErrContributionInvalid) {
			return Page{}, ErrInvalidQuery
		}
		return Page{State: "unavailable"}, err
	}
	if q.Cursor != "" && c.Revision != data.Revision {
		return Page{}, ErrStaleCursor
	}
	c.Revision = data.Revision
	p := Page{State: data.State, IndexedAt: data.IndexedAt, Days: []DayCount{}, Groups: []ActivityGroup{}, Years: []int{}}
	end := time.Date(q.Year+1, 1, 1, 0, 0, 0, 0, time.UTC)
	if q.Year == now.Year() {
		end = time.Date(now.Year(), now.Month(), now.Day()+1, 0, 0, 0, 0, time.UTC)
	}
	for day := time.Date(q.Year, 1, 1, 0, 0, 0, 0, time.UTC); day.Before(end); day = day.AddDate(0, 0, 1) {
		p.Days = append(p.Days, DayCount{Date: day.Format("2006-01-02")})
	}
	byDay := map[string]Counts{}
	for _, row := range data.Days {
		counts := byDay[row.Date]
		addCount(&counts, row.Kind, row.N)
		byDay[row.Date] = counts
		addCount(&p.Totals, row.Kind, row.N)
	}
	for i := range p.Days {
		p.Days[i].Counts = byDay[p.Days[i].Date]
	}
	first := data.FirstYear
	if first == 0 {
		first = now.Year()
	}
	for year := now.Year(); year >= first; year-- {
		p.Years = append(p.Years, year)
	}
	more := len(data.Groups) > q.Limit
	if more {
		data.Groups = data.Groups[:q.Limit]
	}
	for _, row := range data.Groups {
		g := ActivityGroup{Key: fmt.Sprintf("%s/%s/%d", row.Month, row.Kind, row.RepositoryID), Date: row.Month, Kind: row.Kind, Title: row.Title, Status: row.Status, Count: row.N, Links: []EventLink{}}
		for _, link := range row.Links {
			g.Links = append(g.Links, EventLink{RepositoryID: link.RepositoryID, Label: link.Label, URL: link.URL})
		}
		p.Groups = append(p.Groups, g)
	}
	if more {
		last := data.Groups[len(data.Groups)-1]
		c.Month, c.GroupKind, c.Repository = last.Month, last.Kind, last.RepositoryID
		p.NextCursor, err = encodeCursor(c)
		if err != nil {
			return Page{State: "unavailable"}, err
		}
	}
	return p, nil
}

func addCount(c *Counts, kind string, n int) {
	switch kind {
	case "changeset":
		c.Changesets += n
	case "issue":
		c.Issues += n
	case "pull":
		c.Pulls += n
	case "review":
		c.Reviews += n
	}
}
