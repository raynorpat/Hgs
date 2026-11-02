// Copyright 2026 The Gogs Authors. All rights reserved.
// Use of this source code is governed by a MIT-style
// license that can be found in the LICENSE file.

package hg

import (
	"context"
	"encoding/base64"
	"fmt"
	"io"
	"net/url"
	"strings"
	"unicode/utf8"
)

// ReadFile streams the stored content of file at a served changeset to out,
// without an output limit; a symlink yields its link text. Rendering callers
// bound what they read.
func (r *Runner) ReadFile(ctx context.Context, path string, node NodeID, file string, out io.Writer) error {
	node, err := ParseNodeID(string(node))
	if err != nil {
		return err
	}
	if err = checkFile(file); err != nil {
		return err
	}
	if r.reads == nil {
		return fmt.Errorf("%w: no read client", ErrReadService)
	}
	rel, err := r.reads.relative(path)
	if err != nil {
		return err
	}
	_, err = r.reads.Stream(ctx, "/raw", url.Values{"repo": {rel}, "changeset": {string(node)}, "path": {file}}, out)
	return err
}

// checkFile accepts only plain repository-relative file names: Mercurial
// stores no empty, "." or ".." segments, and nothing under .hg is content.
func checkFile(file string) error {
	if strings.Contains(file, `\`) {
		// Mercurial on Windows reads a backslash as a separator.
		return ErrUnsupportedEncoding
	}
	if strings.ContainsRune(file, 0) {
		return ErrNotFound
	}
	for _, seg := range strings.Split(file, "/") {
		if seg == "" || seg == "." || seg == ".." || strings.EqualFold(seg, ".hg") {
			return ErrNotFound
		}
	}
	return nil
}

// treeEntries reads the tree of a served changeset at dir, which is empty at
// the root. The service takes the path as a string, so a name that is not
// UTF-8 is unsupported. Names in the listing are exact.
func (r *Runner) treeEntries(ctx context.Context, repoPath string, node NodeID, dir string) (directory bool, entries []TreeEntry, err error) {
	vars := map[string]interface{}{"changeset": string(node)}
	if dir != "" {
		if !utf8.ValidString(dir) {
			return false, nil, ErrUnsupportedEncoding
		}
		vars["path"] = dir
	}
	var out struct {
		Repo struct {
			Tree struct {
				Directory bool
				Entries   []struct {
					NameBase64                     string
					Directory, Executable, Symlink bool
				}
			}
		}
	}
	err = r.readQuery(ctx, repoPath, "Tree", `query Tree($repo: String!, $changeset: NodeId!, $path: GQLPath) { repo(path: $repo) { tree(changeset: $changeset, path: $path) { directory entries { nameBase64 directory executable symlink } } } }`, vars, &out)
	if err != nil {
		return false, nil, err
	}
	entries = []TreeEntry{}
	for _, e := range out.Repo.Tree.Entries {
		name, err := base64.StdEncoding.DecodeString(e.NameBase64)
		if err != nil || len(name) == 0 {
			return false, nil, fmt.Errorf("%w: invalid tree entry name", ErrReadService)
		}
		entries = append(entries, TreeEntry{Name: string(name), Directory: e.Directory, Executable: e.Executable, Symlink: e.Symlink})
	}
	return out.Repo.Tree.Directory, entries, nil
}

// ListDirectory returns the immediate children of dir at a served changeset.
// dir is empty at the repository root. An unknown or secret node, or a dir that
// is not a directory, is ErrNotFound.
func (r *Runner) ListDirectory(ctx context.Context, repoPath string, node NodeID, dir string) ([]TreeEntry, error) {
	node, err := ParseNodeID(string(node))
	if err != nil {
		return nil, err
	}
	if dir != "" {
		if err = checkFile(dir); err != nil {
			return nil, err
		}
	}
	ctx, cancel := context.WithTimeout(ctx, BrowserCommandTimeout)
	defer cancel()
	isDir, entries, err := r.treeEntries(ctx, repoPath, node, dir)
	if err != nil {
		return nil, err
	}
	if !isDir {
		return nil, ErrNotFound
	}
	return entries, nil
}

// ListPath resolves p at a served changeset to a directory's children or to
// the file entry it names; an unknown path is ErrNotFound.
func (r *Runner) ListPath(ctx context.Context, repoPath string, node NodeID, p string) ([]TreeEntry, *TreeEntry, error) {
	if p == "" {
		entries, err := r.ListDirectory(ctx, repoPath, node, "")
		return entries, nil, err
	}
	node, err := ParseNodeID(string(node))
	if err != nil {
		return nil, nil, err
	}
	if err = checkFile(p); err != nil {
		return nil, nil, err
	}
	ctx, cancel := context.WithTimeout(ctx, BrowserCommandTimeout)
	defer cancel()
	isDir, entries, err := r.treeEntries(ctx, repoPath, node, p)
	if err != nil {
		return nil, nil, err
	}
	if isDir {
		return entries, nil, nil
	}
	if len(entries) != 1 {
		return nil, nil, fmt.Errorf("%w: file tree has %d entries", ErrReadService, len(entries))
	}
	return nil, &entries[0], nil
}
