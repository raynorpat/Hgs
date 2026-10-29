// Copyright 2026 The Gogs Authors. All rights reserved.
// Use of this source code is governed by a MIT-style license.

package profile

import (
	"bytes"
	"context"
	"errors"
	"html/template"
	"net/url"
	"path"
	"strings"
	"unicode/utf8"

	"github.com/gogits/gogs/modules/base"
	"github.com/gogits/gogs/modules/hg"
)

type Content struct {
	RepositoryID    int64
	Node            hg.NodeID
	Path, SourceURL string
	HTML            template.HTML
	State           string
}

// ResolveRepository returns no identifying data when the repository is absent
// or unreadable. Its caller owns managed-read admission for the entire Load.
type ResolveRepository func(ctx context.Context, actorID, ownerID int64, name string) (repoID int64, storagePath string, err error)

// ContentURL emits a canonical, pinned content route. The sole unpinned call,
// node="", path="", raw=false, requests the repository root for special links.
type ContentURL func(repoID int64, node hg.NodeID, path string, raw bool) (string, error)

type ReadmeService struct {
	runner     *hg.Runner
	resolve    ResolveRepository
	contentURL ContentURL
}

func NewReadmeService(runner *hg.Runner, resolve ResolveRepository, contentURL ContentURL) *ReadmeService {
	return &ReadmeService{runner: runner, resolve: resolve, contentURL: contentURL}
}

func (s *ReadmeService) Load(ctx context.Context, actorID, ownerID int64, ownerName string, organization bool) (Content, error) {
	absent := Content{State: "absent"}
	if err := ctx.Err(); err != nil {
		return absent, err
	}
	name, file := ownerName, "README.md"
	if organization {
		name, file = ".github", "profile/README.md"
	}
	id, storage, err := s.resolve(ctx, actorID, ownerID, name)
	if err != nil {
		return Content{State: "unavailable"}, err
	}
	if id == 0 || storage == "" {
		return absent, nil
	}
	result := Content{State: "unavailable"}
	node, err := s.runner.Landing(ctx, storage)
	if errors.Is(err, hg.ErrEmptyRepository) {
		return absent, nil
	}
	if err != nil {
		return result, err
	}
	_, entry, err := s.runner.ListPath(ctx, storage, node, file)
	if errors.Is(err, hg.ErrNotFound) {
		return absent, nil
	}
	if err != nil {
		return result, err
	}
	source, err := s.contentURL(id, node, file, false)
	if err != nil {
		return result, err
	}
	result.RepositoryID, result.Node, result.Path, result.SourceURL = id, node, file, source
	if entry == nil || entry.Directory || entry.Symlink {
		return result, nil
	}
	out := &boundedReadme{}
	err = s.runner.ReadFile(ctx, storage, node, file, out)
	if ctx.Err() != nil {
		return absent, ctx.Err()
	}
	if out.Len() > hg.MaxRenderedFileBytes {
		return result, nil
	}
	if err != nil {
		return result, err
	}
	body := out.Bytes()
	if len(bytes.TrimSpace(body)) == 0 {
		return absent, nil
	}
	_, text := base.IsTextFile(body)
	if !utf8.Valid(body) || bytes.IndexByte(body, 0) >= 0 || !text {
		return result, nil
	}
	root, err := s.contentURL(id, "", "", false)
	if err != nil {
		return result, err
	}
	dir := path.Dir(file)
	if dir == "." {
		dir = ""
	}
	rendered := base.RenderMarkdownWithURLs(body, root, func(value string, raw bool) (string, error) {
		if err := ctx.Err(); err != nil {
			return "", err
		}
		return resolveContentURL(value, dir, id, node, raw, s.contentURL)
	})
	if err := ctx.Err(); err != nil {
		return absent, err
	}
	result.HTML = template.HTML(rendered)
	result.State = "ready"
	return result, nil
}

type boundedReadme struct{ buffer bytes.Buffer }

func (w *boundedReadme) Len() int      { return w.buffer.Len() }
func (w *boundedReadme) Bytes() []byte { return w.buffer.Bytes() }

func (w *boundedReadme) Write(p []byte) (int, error) {
	remaining := hg.MaxRenderedFileBytes + 1 - w.Len()
	if len(p) > remaining {
		n, _ := w.buffer.Write(p[:remaining])
		return n, hg.ErrOutputLimit
	}
	return w.buffer.Write(p)
}

var errContentURL = errors.New("invalid README content URL")

func resolveContentURL(value, dir string, id int64, node hg.NodeID, raw bool, emit ContentURL) (string, error) {
	if !utf8.ValidString(value) || strings.ContainsAny(value, "\x00\\") {
		return "", errContentURL
	}
	u, err := url.Parse(value)
	if err != nil {
		return "", errContentURL
	}
	// External and site links retain their meaning. The final sanitizer applies
	// its scheme policy after this pass; no URL is fetched during rendering.
	if u.IsAbs() {
		return value, nil
	}
	if u.Host != "" {
		u.Scheme = "https"
		return u.String(), nil
	}
	if strings.HasPrefix(u.EscapedPath(), "/") || strings.HasPrefix(value, "#") {
		return value, nil
	}
	query, err := url.ParseQuery(u.RawQuery)
	if err != nil {
		return "", errContentURL
	}
	for _, key := range []string{"path", "ref_kind", "ref_name"} {
		if _, ok := query[key]; ok {
			return "", errContentURL
		}
	}
	parts := []string{}
	if dir != "" {
		parts = strings.Split(dir, "/")
	}
	for _, encoded := range strings.Split(u.EscapedPath(), "/") {
		seg, err := url.PathUnescape(encoded)
		if err != nil || !utf8.ValidString(seg) || strings.ContainsAny(seg, "/\\\x00") || strings.EqualFold(seg, ".hg") {
			return "", errContentURL
		}
		switch seg {
		case "", ".":
		case "..":
			if len(parts) == 0 {
				return "", errContentURL
			}
			parts = parts[:len(parts)-1]
		default:
			parts = append(parts, seg)
		}
	}
	target, err := emit(id, node, strings.Join(parts, "/"), raw)
	if err != nil {
		return "", err
	}
	resolved, err := url.Parse(target)
	if err != nil {
		return "", err
	}
	resolved.RawQuery = u.RawQuery
	resolved.Fragment = u.Fragment
	resolved.RawFragment = u.RawFragment
	return resolved.String(), nil
}
