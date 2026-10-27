package repo

import (
	"bytes"
	"errors"
	"net/url"
	"path"
	"sort"
	"strings"

	"github.com/gogits/gogs/modules/base"
	"github.com/gogits/gogs/modules/hg"
	"github.com/gogits/gogs/modules/middleware"
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
		rows = append(rows, hgRefRow{string(r.Kind), r.Name, string(r.Node), middleware.HgPathURL(link, "src", r.Node, ""), link + "?" + q, r.Closed})
	}
	return rows
}
func Home(ctx *middleware.Context) {
	ctx.Data["Title"] = ctx.Repo.Repository.Name
	ctx.Data["IsRepoToolbarSource"] = true
	p := ctx.Repo.TreeName
	entries, file, err := browseTree(ctx, p)
	if err != nil {
		if errors.Is(err, hg.ErrNotFound) {
			ctx.Handle(404, "ListDirectory", err)
		} else {
			ctx.Handle(500, "ListDirectory", err)
		}
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
	if file != nil {
		ctx.Data["IsFile"] = true
		if !renderHgFile(ctx, hg.ManifestEntry{Path: p, Executable: file.Executable, Symlink: file.Symlink}, false) {
			return
		}
		ctx.Repo.FinishRead()
		ctx.HTML(200, HOME)
		return
	}
	if p != "" && len(entries) == 0 {
		ctx.Error(404)
		return
	}
	rows, readme := treeRows(ctx, p, entries)
	ctx.Data["Files"] = rows
	if p != "" {
		parent, _ := splitTree(p)
		ctx.Data["ParentURL"] = middleware.HgPathURL(ctx.Repo.RepoLink, "src", ctx.Repo.Node, parent)
	}
	if readme != nil && !renderHgFile(ctx, *readme, true) {
		return
	}
	ctx.Repo.FinishRead()
	ctx.HTML(200, HOME)
}

func splitTree(p string) (parent, name string) {
	if i := strings.LastIndex(p, "/"); i >= 0 {
		return p[:i], p[i+1:]
	}
	return "", p
}

func browseTree(ctx *middleware.Context, p string) ([]hg.TreeEntry, *hg.TreeEntry, error) {
	run := ctx.Repo.HgRunner
	if p == "" {
		entries, err := run.ListDirectory(ctx.Repo.HgContext, ctx.Repo.HgPath, ctx.Repo.Node, "")
		return entries, nil, err
	}
	parent, name := splitTree(p)
	listed, err := run.ListDirectory(ctx.Repo.HgContext, ctx.Repo.HgPath, ctx.Repo.Node, parent)
	if err != nil {
		return nil, nil, err
	}
	var match *hg.TreeEntry
	for i := range listed {
		if listed[i].Name == name {
			match = &listed[i]
			break
		}
	}
	if match == nil {
		return nil, nil, hg.ErrNotFound
	}
	if !match.Directory {
		cp := *match
		return nil, &cp, nil
	}
	entries, err := run.ListDirectory(ctx.Repo.HgContext, ctx.Repo.HgPath, ctx.Repo.Node, p)
	return entries, nil, err
}

func treeRows(ctx *middleware.Context, dir string, entries []hg.TreeEntry) ([]hgTreeRow, *hg.ManifestEntry) {
	prefix := dir
	if prefix != "" {
		prefix += "/"
	}
	rows := make([]hgTreeRow, 0, len(entries))
	var readme *hg.ManifestEntry
	var readmeName string
	for _, e := range entries {
		rows = append(rows, hgTreeRow{displayPath(e.Name), middleware.HgPathURL(ctx.Repo.RepoLink, "src", ctx.Repo.Node, prefix+e.Name), e.Directory, e.Executable, e.Symlink})
		if e.Directory || !base.IsReadmeFile(e.Name) {
			continue
		}
		if readme != nil && e.Name >= readmeName {
			continue
		}
		chosen := hg.ManifestEntry{Path: prefix + e.Name, Executable: e.Executable, Symlink: e.Symlink}
		readme = &chosen
		readmeName = e.Name
	}
	sort.Slice(rows, func(i, j int) bool {
		if rows[i].Directory != rows[j].Directory {
			return rows[i].Directory
		}
		return rows[i].Name < rows[j].Name
	})
	return rows, readme
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
