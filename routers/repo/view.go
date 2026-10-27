package repo

import (
	"bytes"
	"errors"
	"net/url"
	"path"
	"sort"
	"strings"

	"github.com/Unknwon/macaron"
	"github.com/gogits/gogs/modules/base"
	"github.com/gogits/gogs/modules/hg"
	"github.com/gogits/gogs/modules/middleware"
	"github.com/gogits/gogs/modules/ui"
)

const HOME base.TplName = "repo/home"

type hgTreeRow struct {
	Name, URL                      string
	Directory, Executable, Symlink bool
}
type hgLink struct{ Name, URL string }
type hgRefRow struct {
	Kind, Name, Node, URL, SelectionURL string
	Closed                              bool
}

func urlValues(kind hg.RefKind, name string) string {
	return url.Values{"ref_kind": {string(kind)}, "ref_name": {name}}.Encode()
}
func referenceRows(link string, refs []hg.Reference) []hgRefRow {
	rows := make([]hgRefRow, 0, len(refs))
	for _, r := range refs {
		q := urlValues(r.Kind, r.Name)
		sourceURL := middleware.HgPathURL(link, "src", r.Node, "")
		selectionURL := link + "?" + q
		// A branch may have several heads. Choosing the displayed row must choose
		// that exact head; resolving its name again would silently pick another.
		if r.Kind == hg.RefBranch {
			selectionURL = sourceURL
		}
		rows = append(rows, hgRefRow{string(r.Kind), r.Name, string(r.Node), sourceURL, selectionURL, r.Closed})
	}
	return rows
}

// PrepareRepositoryUI runs before RepoRef so empty repositories use the same shell.
// It only composes prepared authority; it never opens Mercurial history.
func PrepareRepositoryUI(active string) macaron.Handler {
	return func(ctx *middleware.Context) { ui.BuildShell(ctx, ui.Repository, active) }
}

func Home(ctx *middleware.Context) {
	ui.BuildShell(ctx, ui.Repository, "source")
	ctx.Data["Title"] = ctx.Repo.Repository.Name
	ctx.Data["IsRepoToolbarSource"] = true
	p := ctx.Repo.TreeName
	manifest, err := ctx.Repo.HgRunner.Manifest(ctx.Repo.HgContext, ctx.Repo.HgPath, ctx.Repo.Node)
	if err != nil {
		ctx.Handle(404, "Manifest", err)
		return
	}
	refs, err := ctx.Repo.HgRunner.References(ctx.Repo.HgContext, ctx.Repo.HgPath)
	if err != nil {
		ctx.Handle(500, "References", err)
		return
	}
	ctx.Data["References"] = referenceRows(ctx.Repo.RepoLink, refs)
	ctx.Data["SourceURL"] = middleware.HgPathURL(ctx.Repo.RepoLink, "src", ctx.Repo.Node, "")
	crumbs := []hgLink{}
	parts := strings.Split(p, "/")
	if p != "" {
		for i, seg := range parts {
			crumbs = append(crumbs, hgLink{displayPath(seg), middleware.HgPathURL(ctx.Repo.RepoLink, "src", ctx.Repo.Node, strings.Join(parts[:i+1], "/"))})
		}
	}
	ctx.Data["Breadcrumbs"] = crumbs
	for _, e := range manifest {
		if e.Path == p {
			ctx.Data["IsFile"] = true
			if !renderHgFile(ctx, e, false) {
				return
			}
			ctx.Repo.FinishRead()
			ctx.HTML(200, HOME)
			return
		}
	}
	prefix := p
	if prefix != "" {
		prefix += "/"
	}
	rows := []hgTreeRow{}
	seen := map[string]bool{}
	var readme *hg.ManifestEntry
	for i, e := range manifest {
		rel, ok := strings.CutPrefix(e.Path, prefix)
		if !ok {
			continue
		}
		name, _, dir := strings.Cut(rel, "/")
		if seen[name] {
			continue
		}
		seen[name] = true
		rows = append(rows, hgTreeRow{displayPath(name), middleware.HgPathURL(ctx.Repo.RepoLink, "src", ctx.Repo.Node, prefix+name), dir, e.Executable, e.Symlink})
		if !dir && readme == nil && base.IsReadmeFile(name) {
			readme = &manifest[i]
		}
	}
	if p != "" && len(rows) == 0 {
		ctx.Error(404)
		return
	}
	sort.Slice(rows, func(i, j int) bool {
		if rows[i].Directory != rows[j].Directory {
			return rows[i].Directory
		}
		return rows[i].Name < rows[j].Name
	})
	ctx.Data["Files"] = rows
	if p != "" {
		parent := ""
		if i := strings.LastIndex(p, "/"); i >= 0 {
			parent = p[:i]
		}
		ctx.Data["ParentURL"] = middleware.HgPathURL(ctx.Repo.RepoLink, "src", ctx.Repo.Node, parent)
	}
	if readme != nil && !renderHgFile(ctx, *readme, true) {
		return
	}
	ctx.Repo.FinishRead()
	ctx.HTML(200, HOME)
}

var errRenderedLimit = errors.New("rendered file limit")

type renderedWriter struct{ bytes.Buffer }

func (w *renderedWriter) Write(p []byte) (int, error) {
	n := len(p)
	left := hg.MaxRenderedFileBytes + 1 - w.Len()
	if len(p) > left {
		p = p[:left]
	}
	w.Buffer.Write(p)
	if len(p) != n {
		return len(p), errRenderedLimit
	}
	return n, nil
}
func renderHgFile(ctx *middleware.Context, e hg.ManifestEntry, inList bool) bool {
	w := new(renderedWriter)
	err := ctx.Repo.HgRunner.ReadFile(ctx.Repo.HgContext, ctx.Repo.HgPath, ctx.Repo.Node, e.Path, w)
	if err != nil && !errors.Is(err, errRenderedLimit) {
		ctx.Error(400, err.Error())
		return false
	}
	b := w.Bytes()
	oversized := len(b) > hg.MaxRenderedFileBytes
	ctx.Data["FileName"] = displayPath(path.Base(e.Path))
	ctx.Data["FileSize"] = int64(len(b))
	ctx.Data["FileExt"] = strings.TrimPrefix(path.Ext(e.Path), ".")
	ctx.Data["FileLink"] = middleware.HgPathURL(ctx.Repo.RepoLink, "raw", ctx.Repo.Node, e.Path)
	ctx.Data["FileHistoryURL"] = historyURL(ctx.Repo.RepoLink, hg.HistoryQuery{Snapshot: ctx.Repo.Node, SnapshotRevision: ctx.Repo.Changeset.Revision, BeforeRevision: -1, Path: e.Path})
	ctx.Data["ReadmeInList"] = inList
	ctx.Data["ReadmeExist"] = inList
	ctx.Data["FileOversized"] = oversized
	ctx.Data["FileSymlink"] = e.Symlink
	_, text := base.IsTextFile(b)
	text = text && !bytes.ContainsRune(b, 0)
	ctx.Data["IsFileText"] = text
	if text && !oversized {
		markdown := base.IsMarkdownFile(e.Path) && !e.Symlink
		ctx.Data["IsMarkdown"] = markdown
		if markdown {
			ctx.Data["FileContent"] = string(base.RenderMarkdown(b, middleware.HgPathURL(ctx.Repo.RepoLink, "src", ctx.Repo.Node, "")))
		} else {
			ctx.Data["FileContent"] = string(b)
		}
	}
	return true
}
