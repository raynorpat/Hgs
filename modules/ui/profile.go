package ui

import (
	"errors"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/gogits/gogs/models"
	"github.com/gogits/gogs/modules/contributions"
	"github.com/gogits/gogs/modules/middleware"
	"github.com/gogits/gogs/modules/setting"
)

type ProfileMonth struct {
	Key    string
	Column int
}

type ProfileDay struct {
	Date, URL    string
	Count, Level int
	Selected     bool
}
type ProfileGroup struct {
	contributions.ActivityGroup
	Month, RepositoryURL string
	Capped               bool
}
type ProfileRepository struct {
	*models.Repository
	URL                string
	Watching, Starring bool
}
type ProfileView struct {
	Owner                                                       *models.User
	URL, Tab, SettingsURL, NextURL, RestartURL, ClearURL, Error string
	Query                                                       contributions.Query
	Page                                                        contributions.Page
	Days                                                        []ProfileDay
	StartDay                                                    int
	CalendarMonths                                              []ProfileMonth
	SelectedYearMissing                                         bool
	Groups                                                      []ProfileGroup
	Filters                                                     []NavItem
	Repositories                                                []ProfileRepository
	Total                                                       int
}

// PrepareProfile composes request-local view data only. The provider owns indexing.
// Invalid and stale selections keep a normal GET recovery link and an honest status.
func PrepareProfile(ctx *middleware.Context, owner *models.User, provider contributions.Provider, repos []*models.Repository) int {
	v := ProfileView{Owner: owner, URL: owner.HomeLink(), Tab: ctx.Query("tab")}
	if owner.IsOrganization() {
		v.URL = setting.AppSubUrl + "/org/" + owner.Name
	}
	if v.Tab == "" {
		v.Tab = "overview"
	}
	status := 200
	now := time.Now().UTC()
	v.Query = contributions.Query{Year: now.Year(), Day: ctx.Query("day"), Kind: ctx.Query("kind"), Cursor: ctx.Query("cursor")}
	invalid := false
	values := ctx.Req.URL.Query()
	for _, key := range []string{"tab", "year", "day", "kind", "cursor"} {
		if len(values[key]) > 1 {
			invalid = true
		}
	}
	if raw, ok := values["year"]; ok {
		year, err := strconv.Atoi(raw[0])
		if err != nil || year < 1 || year > now.Year() {
			invalid = true
		} else {
			v.Query.Year = year
		}
	}
	if v.Tab != "overview" && v.Tab != "repositories" && v.Tab != "activity" && !(v.Tab == "people" && owner.IsOrganization()) {
		invalid = true
		v.Tab = "overview"
	}
	switch v.Query.Kind {
	case "", "changeset", "issue", "pull", "review", "repository":
	default:
		invalid = true
	}
	if v.Query.Day != "" {
		day, err := time.Parse("2006-01-02", v.Query.Day)
		if err != nil || day.Year() != v.Query.Year || day.After(now) {
			invalid = true
		}
	}
	if len(v.Query.Cursor) > 4096 {
		invalid = true
	}
	actor := int64(0)
	if ctx.IsSigned {
		actor = ctx.User.Id
	}
	if actor == owner.Id {
		v.SettingsURL = setting.AppSubUrl + "/user/settings"
	}
	if owner.IsOrganization() && ctx.Org.IsOwner {
		v.SettingsURL = v.URL + "/settings"
	}
	v.Page = contributions.Page{State: "unavailable", Years: []int{v.Query.Year}}
	var reader contributions.Reader
	if provider != nil {
		reader = provider.Contributions()
	}
	if invalid {
		status = 400
		v.Error = "invalid"
	} else if reader != nil {
		page, err := reader.Read(ctx.Req.Context(), actor, contributions.Scope{OwnerID: owner.Id, Organization: owner.IsOrganization()}, v.Query)
		switch {
		case errors.Is(err, contributions.ErrInvalidQuery):
			status = 400
			v.Error = "invalid"
		case errors.Is(err, contributions.ErrStaleCursor):
			status = 409
			v.Error = "stale"
		case err != nil:
			v.Page.State = "unavailable"
		default:
			v.Page = page
		}
	}
	v.RestartURL = profileURL(v.URL, v.Tab, v.Query, "")
	if v.Error == "invalid" {
		v.RestartURL = v.URL + "?tab=" + v.Tab
	}
	clear := v.Query
	clear.Day = ""
	v.ClearURL = profileURL(v.URL, v.Tab, clear, "")
	if v.Page.NextCursor != "" {
		v.NextURL = profileURL(v.URL, v.Tab, v.Query, v.Page.NextCursor)
	}
	v.Total = countTotal(v.Page.Totals)
	v.SelectedYearMissing = true
	for _, year := range v.Page.Years {
		if year == v.Query.Year {
			v.SelectedYearMissing = false
		}
	}
	v.StartDay = int(time.Date(v.Query.Year, 1, 1, 0, 0, 0, 0, time.UTC).Weekday()) + 1
	for i, d := range v.Page.Days {
		if strings.HasSuffix(d.Date, "-01") {
			month, _ := strconv.Atoi(d.Date[5:7])
			v.CalendarMonths = append(v.CalendarMonths, ProfileMonth{Key: "profile.month_" + strconv.Itoa(month), Column: (i+v.StartDay-1)/7 + 1})
		}
		n := countTotal(d.Counts)
		level := 0
		switch {
		case n >= 10:
			level = 4
		case n >= 5:
			level = 3
		case n >= 2:
			level = 2
		case n > 0:
			level = 1
		}
		q := v.Query
		q.Day = d.Date
		v.Days = append(v.Days, ProfileDay{d.Date, profileURL(v.URL, v.Tab, q, ""), n, level, d.Date == v.Query.Day})
	}
	lastMonth := ""
	for _, g := range v.Page.Groups {
		row := ProfileGroup{ActivityGroup: g, Capped: len(g.Links) < g.Count}
		if lastMonth != g.Date {
			row.Month = g.Date
			lastMonth = g.Date
		}
		// Task22 supplies the current canonical owner/repository title, never stored names.
		parts := strings.SplitN(g.Title, "/", 2)
		if len(parts) == 2 {
			row.RepositoryURL = setting.AppSubUrl + "/" + url.PathEscape(parts[0]) + "/" + url.PathEscape(parts[1])
			switch g.Kind {
			case "changeset":
				row.RepositoryURL += "/commits"
			case "issue":
				row.RepositoryURL += "/issues"
			case "pull", "review":
				row.RepositoryURL += "/pulls"
			}
		}
		v.Groups = append(v.Groups, row)
	}
	for _, kind := range []string{"", "changeset", "pull", "issue", "review"} {
		q := v.Query
		q.Kind = kind
		key := kind
		if key == "" {
			key = "all"
		}
		v.Filters = append(v.Filters, NavItem{Key: key, URL: profileURL(v.URL, v.Tab, q, ""), Active: kind == v.Query.Kind})
	}
	for _, r := range repos {
		link, err := r.RepoLink()
		if err != nil {
			continue
		}
		v.Repositories = append(v.Repositories, ProfileRepository{Repository: r, URL: link, Watching: actor > 0 && models.IsWatching(actor, r.Id), Starring: actor > 0 && models.IsStaring(actor, r.Id)})
	}
	ctx.Data["Profile"] = v
	ctx.Data["TabName"] = v.Tab
	return status
}

func profileURL(path, tab string, q contributions.Query, cursor string) string {
	values := url.Values{"tab": {tab}, "year": {strconv.Itoa(q.Year)}}
	if q.Day != "" {
		values.Set("day", q.Day)
	}
	if q.Kind != "" {
		values.Set("kind", q.Kind)
	}
	if cursor != "" {
		values.Set("cursor", cursor)
	}
	return path + "?" + values.Encode()
}
func countTotal(c contributions.Counts) int { return c.Changesets + c.Issues + c.Pulls + c.Reviews }
