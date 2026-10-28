package repo

import (
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"html/template"
	"net/http"
	"net/url"
	"strconv"
	"strings"

	"github.com/gogits/gogs/models"
	"github.com/gogits/gogs/modules/base"
	"github.com/gogits/gogs/modules/collaboration"
	"github.com/gogits/gogs/modules/hg"
	"github.com/gogits/gogs/modules/hosting"
	"github.com/gogits/gogs/modules/middleware"
	"github.com/gogits/gogs/modules/setting"
	"github.com/gogits/gogs/modules/ui"
)

// PullForm contains only submitted data. Actor and destination always come
// from the session and repository route, never from this form.
type PullForm struct {
	Title            string `form:"title"`
	Content          string `form:"content"`
	SourceRepoID     int64  `form:"source_repo_id"`
	SourceRepository string `form:"source_repository"`
	SourceKind       string `form:"source_kind"`
	SourceName       string `form:"source_name"`
	SourceHead       string `form:"source_head"`
	TargetKind       string `form:"target_kind"`
	TargetName       string `form:"target_name"`
	TargetHead       string `form:"target_head"`
	ExpectedSource   string `form:"expected_source"`
	ExpectedTarget   string `form:"expected_target"`
	ComparisonID     int64  `form:"comparison_id"`
	Reviewers        string `form:"reviewers"`
	State            string `form:"state"`
	ReviewerID       int64  `form:"reviewer_id"`
	Reason           string `form:"reason"`
	Path             string `form:"path"`
	Side             string `form:"side"`
	Line             int    `form:"line"`
	Resolved         bool   `form:"resolved"`
	SourceNode       string `form:"source_node"`
	TargetNode       string `form:"target_node"`
	MergeKey         string `form:"merge_key"`
	Confirm          bool   `form:"confirm"`
}

// Bound parsing happens before binding, and raw validation messages never echo
// request bodies, internal paths or database errors.
func PullRequestLimits(ctx *middleware.Context) {
	ctx.Resp.Header().Set("Cache-Control", "private, no-store")
	if ctx.Req.Method == "POST" {
		ctx.Req.Request.Body = http.MaxBytesReader(ctx.Resp, ctx.Req.Request.Body, 256<<10)
		if err := ctx.Req.ParseForm(); err != nil {
			ctx.Error(413)
		}
	}
}

type pullRow struct {
	ID, Number                                        int64
	Title, URL, Source, Target, Author, Avatar, State string
}
type pullTab struct {
	Label, URL string
	Count      int
	Active     bool
}
type pullPerson struct{ Name, Avatar string }
type pullComment struct {
	models.PullComment
	Author, Avatar, URL, FileURL string
	HTML                         template.HTML
	CanResolve                   bool
}
type pullReviewer struct {
	models.PullReview
	Name, Avatar string
}
type pullDetail struct {
	Pull                                                                                                         models.PullRequest
	Row                                                                                                          pullRow
	Form                                                                                                         PullForm
	Source, Target                                                                                               hg.ReviewSelection
	Comparison                                                                                                   models.PullComparison
	Freshness, Status, OperationURL, MergeKey, Strategy                                                          string
	Body                                                                                                         template.HTML
	Comments                                                                                                     []pullComment
	Reviewers                                                                                                    []pullReviewer
	Changesets                                                                                                   []hg.Changeset
	Diff                                                                                                         string
	Binary, Truncated, CanEdit, CanSelectSource, CanSelect, CanState, CanAssign, CanReview, CanDismiss, CanMerge bool
	Operation                                                                                                    collaboration.MergeResult
	SourceClone, TargetClone                                                                                     string
}

func pullActor(ctx *middleware.Context) int64 {
	if ctx.IsSigned && ctx.User != nil {
		return ctx.User.Id
	}
	return 0
}
func pullID(s string) int64                   { n, _ := strconv.ParseInt(s, 10, 64); return n }
func pullRoot(ctx *middleware.Context) string { return ctx.Repo.RepoLink + "/pulls" }
func pullURL(ctx *middleware.Context, p models.PullRequest) string {
	return fmt.Sprintf("%s/%d", pullRoot(ctx), p.Number)
}
func pullPersonFor(id int64) pullPerson {
	u, e := models.GetUserById(id)
	if e != nil {
		return pullPerson{Name: "?"}
	}
	return pullPerson{u.Name, u.AvatarLink()}
}
func pullRepoName(id int64) (string, error) {
	r, e := models.GetRepositoryById(id)
	if e != nil {
		return "", e
	}
	if e = r.GetOwner(); e != nil {
		return "", e
	}
	return r.Owner.Name + "/" + r.Name, nil
}
func pullDisplay(p models.PullRequest) (pullRow, error) {
	source, e := pullRepoName(p.SourceRepoID)
	if e != nil {
		return pullRow{}, e
	}
	target, e := pullRepoName(p.TargetRepoID)
	if e != nil {
		return pullRow{}, e
	}
	person := pullPersonFor(p.AuthorID)
	return pullRow{p.ID, p.Number, p.Title, fmt.Sprintf("%s/%s/pulls/%d", setting.AppSubUrl, target, p.Number), source, target, person.Name, person.Avatar, string(p.State)}, nil
}
func pullSelections(f PullForm) (hg.ReviewSelection, hg.ReviewSelection) {
	return hg.ReviewSelection{Ref: hg.RefSelection{Kind: hg.RefKind(f.SourceKind), Name: f.SourceName}, Head: hg.NodeID(f.SourceHead)}, hg.ReviewSelection{Ref: hg.RefSelection{Kind: hg.RefKind(f.TargetKind), Name: f.TargetName}, Head: hg.NodeID(f.TargetHead)}
}
func pullReviewerIDs(raw string) ([]int64, error) {
	if len(raw) > 4096 {
		return nil, models.ErrPullInvalid
	}
	names := strings.FieldsFunc(raw, func(r rune) bool { return r == ',' || r == ' ' || r == '\n' })
	if len(names) > 50 {
		return nil, models.ErrPullInvalid
	}
	ids := []int64{}
	for _, name := range names {
		u, e := models.GetUserByName(name)
		if e != nil {
			return nil, models.ErrPullInvalid
		}
		ids = append(ids, u.Id)
	}
	return ids, nil
}
func pullFormSafe(ctx *middleware.Context) bool {
	for _, key := range []string{"actor", "actor_id", "author_id", "target_repo_id", "pull_id"} {
		if ctx.Req.FormValue(key) != "" {
			pullError(ctx, models.ErrPullInvalid)
			return false
		}
	}
	return true
}
func pullErrorStatus(err error) int {
	switch {
	case errors.Is(err, models.ErrPullNotExist), errors.Is(err, models.ErrRepoNotExist), errors.Is(err, hg.ErrNotFound):
		return 404
	case errors.Is(err, models.ErrInvalidCredential):
		return 403
	case errors.Is(err, models.ErrPullConflict), errors.Is(err, models.ErrPullState), errors.Is(err, models.ErrMergeActive), errors.Is(err, models.ErrMergeBlocked), errors.Is(err, models.ErrMergePartial), errors.Is(err, models.ErrMergeIdempotency), errors.Is(err, collaboration.ErrMergeStale), errors.Is(err, hg.ErrAmbiguousHead), errors.Is(err, hosting.ErrMutationBusy):
		return 409
	case errors.Is(err, models.ErrPullInvalid), errors.Is(err, models.ErrPullFilter), errors.Is(err, hg.ErrNoChanges), errors.Is(err, hg.ErrUnrelatedHistory):
		return 422
	default:
		return 503
	}
}
func pullError(ctx *middleware.Context, err error) {
	ctx.Resp.Header().Set("Content-Type", "text/plain; charset=utf-8")
	ctx.Resp.WriteHeader(pullErrorStatus(err))
	ctx.Resp.Write([]byte(ctx.Tr("pulls.error")))
}

func pullFailureMessage(ctx *middleware.Context, err error) string {
	if pullErrorStatus(err) >= 500 {
		return ctx.Tr("pulls.unknown")
	}
	return ctx.Tr("pulls.unsaved")
}
func loadPull(ctx *middleware.Context, s *collaboration.Service) (models.PullRequest, bool) {
	p, e := s.GetPull(ctx.Req.Request.Context(), pullActor(ctx), ctx.Repo.Repository.Id, pullID(ctx.Params(":number")))
	if e != nil {
		pullError(ctx, e)
		return p, false
	}
	return p, true
}

// Pulls handles both workspace and destination-scoped lists. Each tab count
// comes from the same currently authorized two-endpoint query as its rows.
func Pulls(ctx *middleware.Context, s *collaboration.Service) {
	kind := ui.Workspace
	repoID := int64(0)
	root := setting.AppSubUrl + "/pulls"
	if ctx.Repo.Repository != nil {
		kind = ui.Repository
		repoID = ctx.Repo.Repository.Id
		root = pullRoot(ctx)
	}
	ctx.Data["Title"] = ctx.Tr("ui.pull_requests")
	ui.BuildShell(ctx, kind, "pull_requests")
	state, filter := ctx.Query("state"), ctx.Query("filter")
	if state == "" {
		state = "open"
	}
	if filter == "" {
		filter = "all"
	}
	page := int(pullID(ctx.Query("page")))
	if page < 1 {
		page = 1
	}
	if page > 1000000 {
		page = 1000000
	}
	q := models.PullQuery{RepoID: repoID, State: state, Filter: filter, Page: page, PageSize: 30}
	result, e := s.ListPulls(ctx.Req.Request.Context(), pullActor(ctx), q)
	if e != nil {
		pullError(ctx, e)
		return
	}
	rows := []pullRow{}
	for _, p := range result.Requests {
		row, e := pullDisplay(p)
		if e != nil {
			pullError(ctx, e)
			return
		}
		rows = append(rows, row)
	}
	tabs := []pullTab{}
	for _, st := range []string{"open", "merged", "declined", "all"} {
		countQuery := q
		countQuery.State = st
		countQuery.Page = 1
		countQuery.PageSize = 1
		c, e := s.ListPulls(ctx.Req.Request.Context(), pullActor(ctx), countQuery)
		if e != nil {
			pullError(ctx, e)
			return
		}
		tabs = append(tabs, pullTab{ctx.Tr("pulls." + st), root + "?state=" + st + "&filter=" + url.QueryEscape(filter), c.Total, state == st})
	}
	pages := []ui.PageLink{}
	for _, v := range []struct {
		n     int
		label string
	}{{page - 1, ctx.Tr("ui.previous")}, {page, fmt.Sprint(page)}, {page + 1, ctx.Tr("ui.next")}} {
		pages = append(pages, ui.PageLink{Label: v.label, URL: fmt.Sprintf("%s?state=%s&filter=%s&page=%d", root, state, filter, v.n), Current: v.n == page, Disabled: v.n < 1 || (v.n > page && page*30 >= result.Total)})
	}
	// Hydration can race revocation; do not emit even labels after lost access.
	for _, p := range result.Requests {
		if _, e := s.GetPull(ctx.Req.Request.Context(), pullActor(ctx), p.TargetRepoID, p.Number); e != nil {
			pullError(ctx, e)
			return
		}
	}
	ctx.Data["PullRows"], ctx.Data["PullTabs"], ctx.Data["PullRoot"], ctx.Data["PullFilter"], ctx.Data["PullState"] = rows, tabs, root, filter, state
	ctx.Data["PullPagination"] = struct {
		Label string
		Pages []ui.PageLink
	}{ctx.Tr("ui.pull_requests"), pages}
	ctx.HTML(200, "repo/pull/list")
}

func PullNew(ctx *middleware.Context, s *collaboration.Service) {
	renderPullNew(ctx, s, PullForm{SourceRepoID: ctx.Repo.Repository.Id, SourceRepository: ctx.Repo.Owner.Name + "/" + ctx.Repo.Repository.Name, SourceKind: "branch", TargetKind: "branch", TargetName: "default"}, 200, "")
}
func renderPullNew(ctx *middleware.Context, s *collaboration.Service, f PullForm, status int, message string) {
	ctx.Data["Title"] = ctx.Tr("pulls.create")
	ui.BuildShell(ctx, ui.Repository, "pull_requests")
	ctx.Data["PullForm"], ctx.Data["PullError"], ctx.Data["PullEditor"] = f, message, issueEditor(ctx, "pull-body", f.Content)
	ctx.HTML(status, "repo/pull/new")
}
func PullNewPost(ctx *middleware.Context, s *collaboration.Service, f PullForm) {
	if !pullFormSafe(ctx) {
		return
	}
	ids, e := pullReviewerIDs(f.Reviewers)
	if e == nil && f.SourceRepository != "" {
		f.SourceRepoID, e = models.ResolvePullSource(ctx.Req.Request.Context(), pullActor(ctx), ctx.Repo.Repository.Id, f.SourceRepository)
	}
	src, dst := pullSelections(f)
	var p models.PullRequest
	if e == nil {
		p, e = s.CreatePull(ctx.Req.Request.Context(), pullActor(ctx), collaboration.NewPull{SourceRepoID: f.SourceRepoID, TargetRepoID: ctx.Repo.Repository.Id, Source: src, Target: dst, Title: f.Title, Body: f.Content, ReviewerIDs: ids})
	}
	if e != nil {
		renderPullNew(ctx, s, f, pullErrorStatus(e), pullFailureMessage(ctx, e))
		return
	}
	ctx.Redirect(pullURL(ctx, p), 303)
}

func PullView(ctx *middleware.Context, s *collaboration.Service) {
	p, ok := loadPull(ctx, s)
	if ok {
		renderPull(ctx, s, p, nil, 200, "")
	}
}
func renderPull(ctx *middleware.Context, s *collaboration.Service, p models.PullRequest, submitted *PullForm, status int, message string) {
	actor := pullActor(ctx)
	request := ctx.Req.Request.Context()
	row, e := pullDisplay(p)
	if e != nil {
		pullError(ctx, e)
		return
	}
	source, target, e := models.PullSelections(p)
	if e != nil {
		pullError(ctx, e)
		return
	}
	d := pullDetail{Pull: p, Row: row, Source: source, Target: target, Body: base.Str2html(string(base.RenderMarkdown([]byte(p.Body), ctx.Repo.RepoLink)))}
	d.Form = PullForm{Title: p.Title, Content: p.Body, SourceRepoID: p.SourceRepoID, SourceKind: string(source.Ref.Kind), SourceName: source.Ref.Name, SourceHead: string(source.Head), TargetKind: string(target.Ref.Kind), TargetName: target.Ref.Name, TargetHead: string(target.Head), ExpectedSource: p.SourceSelection, ExpectedTarget: p.TargetSelection, ComparisonID: p.CurrentComparisonID}
	if submitted != nil {
		d.Form = *submitted
	}
	sa, _ := models.RepositoryAccessFor(actor, p.SourceRepoID)
	ta, _ := models.RepositoryAccessFor(actor, p.TargetRepoID)
	enabled := actor > 0 && models.ValidateRepositoryCredential(actor, 0, 0) == nil
	d.CanEdit = enabled && actor == p.AuthorID
	d.CanSelectSource = d.CanEdit && sa.Write && p.State == models.PullOpen
	d.CanSelect = enabled && p.State == models.PullOpen && (d.CanSelectSource || ta.Write)
	d.CanState = enabled && p.State != models.PullMerged && (ta.Write || (actor == p.AuthorID && (p.State == models.PullOpen || sa.Write)))
	d.CanAssign = enabled && (actor == p.AuthorID || ta.Write)
	d.CanReview = false
	d.CanDismiss = enabled && ta.Admin
	d.Comparison, d.Freshness, e = s.InspectPullComparison(request, actor, p.ID)
	if e != nil {
		if pullErrorStatus(e) == 403 || pullErrorStatus(e) == 404 {
			pullError(ctx, e)
			return
		}
		d.Freshness = "unavailable"
	}
	d.Status = d.Freshness
	if d.Comparison.ID > 0 && d.Comparison.SnapshotComplete {
		_, preview, err := s.Comparison(request, actor, p.ID, d.Comparison.ID)
		if err == nil {
			d.Changesets = preview.Changesets
			d.Diff = string(preview.Diff.Text)
			d.Binary = preview.Diff.Binary
			d.Truncated = preview.Diff.Truncated
		} else if !errors.Is(err, hg.ErrComparisonTooLarge) {
			d.Status = "unavailable"
		}
	}
	reviews, e := models.ListPullReviews(request, actor, p.ID)
	if e != nil {
		pullError(ctx, e)
		return
	}
	blocked := false
	for _, r := range reviews {
		if enabled && p.State == models.PullOpen && r.ReviewerID == actor && r.Assigned && r.Eligible {
			d.CanReview = true
		}
		person := pullPersonFor(r.ReviewerID)
		d.Reviewers = append(d.Reviewers, pullReviewer{r, person.Name, person.Avatar})
		if r.Assigned && submitted == nil {
			if d.Form.Reviewers != "" {
				d.Form.Reviewers += ", "
			}
			d.Form.Reviewers += person.Name
		}
		if r.State == models.ReviewChangesRequested && r.Eligible {
			blocked = true
		}
	}
	comments, e := models.ListPullComments(request, actor, p.ID)
	if e != nil {
		pullError(ctx, e)
		return
	}
	for _, c := range comments {
		person := pullPersonFor(c.AuthorID)
		item := pullComment{PullComment: c, Author: person.Name, Avatar: person.Avatar, URL: row.URL + fmt.Sprintf("#comment-%d", c.ID), HTML: base.Str2html(string(base.RenderMarkdown([]byte(c.Body), ctx.Repo.RepoLink))), CanResolve: enabled && (actor == c.AuthorID || ta.Write)}
		if c.ComparisonID > 0 {
			item.FileURL = pullFileURL(row.URL, c.ComparisonID, c.Side, c.Path)
		}
		d.Comments = append(d.Comments, item)
	}
	d.CanMerge = enabled && ta.Write && p.State == models.PullOpen && d.Status == "fresh" && !blocked
	opID := ctx.Params(":operation")
	if opID == "" {
		op, e := models.LatestPullMerge(request, actor, p.ID)
		if e != nil {
			pullError(ctx, e)
			return
		}
		opID = op.ID
	}
	if opID != "" {
		op, err := models.GetMergeOperation(request, actor, p.ID, opID)
		if err != nil {
			pullError(ctx, err)
			return
		}
		d.Operation, e = s.MergeStatus(request, actor, p.ID, opID)
		if e != nil {
			pullError(ctx, e)
			return
		}
		d.OperationURL = row.URL + "/operations/" + url.PathEscape(opID)
		if d.Operation.State != "failed" || (op.SourceNode == d.Comparison.SourceNode && op.TargetNode == d.Comparison.TargetNode) {
			var disabled bool
			d.Status, disabled = pullOperationPresentation(d.Operation)
			if disabled {
				d.CanMerge = false
			}
		}
	}
	if p.State == models.PullMerged {
		d.Status = "merged"
		d.CanMerge = false
	}
	if d.Freshness != "fresh" {
		d.CanReview = false
	}
	d.Strategy = ctx.Tr("pulls.strategy")
	if d.CanMerge {
		strategy, err := s.ComparisonMergeStrategy(request, actor, p.ID, d.Comparison.ID)
		if err != nil {
			d.CanMerge = false
			d.Status = "unavailable"
		} else {
			d.Strategy = ctx.Tr("pulls.strategy_" + string(strategy))
		}
	}
	if submitted != nil && submitted.MergeKey != "" {
		d.MergeKey = submitted.MergeKey
	} else if d.CanMerge {
		var raw [24]byte
		if _, e = rand.Read(raw[:]); e != nil {
			pullError(ctx, e)
			return
		}
		d.MergeKey = hex.EncodeToString(raw[:])
	}
	d.SourceClone = strings.TrimSuffix(setting.AppUrl, "/") + "/hg/" + row.Source
	d.TargetClone = strings.TrimSuffix(setting.AppUrl, "/") + "/hg/" + row.Target
	current, e := s.GetPull(request, actor, p.TargetRepoID, p.Number)
	if e != nil {
		pullError(ctx, e)
		return
	}
	if !models.SamePullSelection(p, current) {
		d.Status = "stale"
		d.CanMerge = false
		d.CanReview = false
		d.CanSelect = false
	}
	ctx.Data["Title"] = p.Title
	ui.BuildShell(ctx, ui.Repository, "pull_requests")
	ctx.Data["Pull"], ctx.Data["PullError"], ctx.Data["PullEditor"], ctx.Data["PullCommentEditor"] = d, message, issueEditor(ctx, "pull-edit", d.Form.Content), issueEditor(ctx, "pull-comment", "")
	if submitted != nil && ctx.Data["PullCommentSubmitted"] == true {
		ctx.Data["PullCommentEditor"] = issueEditor(ctx, "pull-comment", submitted.Content)
	}
	ctx.HTML(status, "repo/pull/view")
}

func PullEdit(ctx *middleware.Context, s *collaboration.Service, f PullForm) {
	if !pullFormSafe(ctx) {
		return
	}
	p, ok := loadPull(ctx, s)
	if !ok {
		return
	}
	if e := s.EditPull(ctx.Req.Request.Context(), pullActor(ctx), p.ID, f.Title, f.Content); e != nil {
		renderPull(ctx, s, p, &f, pullErrorStatus(e), pullFailureMessage(ctx, e))
		return
	}
	ctx.Redirect(pullURL(ctx, p), 303)
}
func PullState(ctx *middleware.Context, s *collaboration.Service, f PullForm) {
	pullMutation(ctx, s, f, func(p models.PullRequest) error {
		return s.SetPullState(ctx.Req.Request.Context(), pullActor(ctx), p.ID, models.PullState(f.State))
	})
}
func pullMutation(ctx *middleware.Context, s *collaboration.Service, f PullForm, apply func(models.PullRequest) error) {
	if !pullFormSafe(ctx) {
		return
	}
	p, ok := loadPull(ctx, s)
	if !ok {
		return
	}
	if e := apply(p); e != nil {
		renderPull(ctx, s, p, &f, pullErrorStatus(e), pullFailureMessage(ctx, e))
		return
	}
	ctx.Redirect(pullURL(ctx, p), 303)
}
func PullRefresh(ctx *middleware.Context, s *collaboration.Service, f PullForm) {
	if !pullFormSafe(ctx) {
		return
	}
	p, ok := loadPull(ctx, s)
	if !ok {
		return
	}
	if f.SourceKind != "" {
		expected := p
		expected.SourceSelection = f.ExpectedSource
		expected.TargetSelection = f.ExpectedTarget
		expected.CurrentComparisonID = f.ComparisonID
		source, target := pullSelections(f)
		if e := s.ReselectPull(ctx.Req.Request.Context(), pullActor(ctx), expected, source, target); e != nil {
			renderPull(ctx, s, p, &f, pullErrorStatus(e), pullFailureMessage(ctx, e))
			return
		}
	}
	_, e := s.RefreshComparison(ctx.Req.Request.Context(), pullActor(ctx), p.ID)
	if e != nil {
		current, err := s.GetPull(ctx.Req.Request.Context(), pullActor(ctx), p.TargetRepoID, p.Number)
		if err != nil {
			pullError(ctx, err)
			return
		}
		renderPull(ctx, s, current, nil, pullErrorStatus(e), ctx.Tr("pulls.saved_refresh"))
		return
	}
	ctx.Redirect(pullURL(ctx, p), 303)
}

func pullFileURL(root string, comparison int64, side, path string) string {
	return fmt.Sprintf("%s/comparisons/%d/files/%s?side=%s", root, comparison, escapePullPath(path), url.QueryEscape(side))
}
func escapePullPath(path string) string {
	parts := strings.Split(path, "/")
	for i := range parts {
		parts[i] = url.PathEscape(parts[i])
	}
	return strings.Join(parts, "/")
}
