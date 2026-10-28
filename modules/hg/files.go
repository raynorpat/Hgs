// Copyright 2026 The Gogs Authors. All rights reserved.
// Use of this source code is governed by a MIT-style
// license that can be found in the LICENSE file.

package hg

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	pathpkg "path"
	"path/filepath"
	"sort"
	"strings"
)

// flatDirectoryScanLimit bounds a flat-manifest directory scan. Flat
// manifests have no per-directory revlog, so child names come from a bounded
// scan of paths under that directory.
const flatDirectoryScanLimit = 32 << 20

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

	// The name reaches Mercurial as raw bytes in pattern files, never as an
	// argument Windows would re-encode. path: is literal but also matches a
	// directory's files; rootfilesin: of the parent leaves only the file.
	dir, err := os.MkdirTemp(filepath.Dir(r.cfg.ConfigPath), "cat-")
	if err != nil {
		return err
	}
	defer os.RemoveAll(dir)
	parent := pathpkg.Dir(file)
	if parent == "." {
		parent = ""
	}
	include, pattern := filepath.Join(dir, "include"), filepath.Join(dir, "pattern")
	if err = os.WriteFile(include, []byte("rootfilesin:"+parent+"\x00"), 0600); err != nil {
		return err
	}
	if err = os.WriteFile(pattern, []byte("path:"+file+"\x00"), 0600); err != nil {
		return err
	}

	err = r.run(ctx, path, []string{"cat", "-r", nodeRevset(node), "-I", "listfile0:" + include, "--", "listfile0:" + pattern}, nil, out, -1)
	var exit *exec.ExitError
	switch {
	case err == nil:
		return nil
	case errors.As(err, &exit) && exit.ExitCode() == 1:
		// hg cat exits 1 when no file matched.
		return ErrNotFound
	}
	return r.notServed(ctx, path, node, err)
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

// notServed turns a failure caused by node not being served (unknown or
// secret, which aborts the command) into ErrNotFound.
func (r *Runner) notServed(ctx context.Context, path string, node NodeID, err error) error {
	if errors.Is(r.served(ctx, path, node), ErrNotFound) {
		return ErrNotFound
	}
	return err
}

// ListDirectory returns the immediate children of dir at a served changeset.
// dir is empty at the repository root. An unknown or secret node is
// ErrNotFound. A Mercurial failure while the node is served is returned as-is.
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
	if usesTreeManifest(repoPath) {
		return r.listTreeDirectory(ctx, repoPath, node, dir)
	}
	return r.listFlatDirectory(ctx, repoPath, node, dir)
}

func usesTreeManifest(repoPath string) bool {
	for _, name := range []string{
		filepath.Join(repoPath, ".hg", "requires"),
		filepath.Join(repoPath, ".hg", "store", "requires"),
	} {
		b, err := os.ReadFile(name)
		if err != nil {
			continue
		}
		for _, line := range strings.Split(string(b), "\n") {
			if strings.TrimSpace(line) == "treemanifest" {
				return true
			}
		}
	}
	return false
}

func (r *Runner) listFlatDirectory(ctx context.Context, repoPath string, node NodeID, dir string) ([]TreeEntry, error) {
	b, err := r.flatManifest(ctx, repoPath, node)
	if err != nil {
		return nil, err
	}
	return parseFlatDirectory(b, dir)
}

// flatManifest reads the whole flat manifest with its flags. manifest -v does it
// in one pass; the files template's flags keyword costs a per-file lookup that
// dominated large trees. It takes no path patterns, so callers select paths.
func (r *Runner) flatManifest(ctx context.Context, repoPath string, node NodeID) ([]byte, error) {
	var out bytes.Buffer
	err := r.run(ctx, repoPath, []string{"manifest", "-r", nodeRevset(node), "-v"}, nil, &out, flatDirectoryScanLimit)
	var exit *exec.ExitError
	switch {
	case err == nil:
		return out.Bytes(), nil
	case errors.Is(err, ErrOutputLimit):
		return nil, err
	case errors.As(err, &exit) && exit.ExitCode() == 1:
		return nil, nil
	default:
		return nil, r.notServed(ctx, repoPath, node, err)
	}
}

// ListPath resolves p at a served changeset to a directory's children or to
// the file entry it names; an unknown path is ErrNotFound. Flat manifests
// answer both from one listing.
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
	parent, name := "", p
	if i := strings.LastIndex(p, "/"); i >= 0 {
		parent, name = p[:i], p[i+1:]
	}
	var listed []TreeEntry
	if usesTreeManifest(repoPath) {
		if listed, err = r.listTreeDirectory(ctx, repoPath, node, parent); err != nil {
			return nil, nil, err
		}
	} else {
		b, err := r.flatManifest(ctx, repoPath, node)
		if err != nil {
			return nil, nil, err
		}
		if entries, err := parseFlatDirectory(b, p); err != nil || len(entries) > 0 {
			return entries, nil, err
		}
		if listed, err = parseFlatDirectory(b, parent); err != nil {
			return nil, nil, err
		}
	}
	for i := range listed {
		if listed[i].Name != name {
			continue
		}
		if !listed[i].Directory {
			return nil, &listed[i], nil
		}
		entries, err := r.listTreeDirectory(ctx, repoPath, node, p)
		return entries, nil, err
	}
	return nil, nil, ErrNotFound
}

func parseFlatDirectory(b []byte, dir string) ([]TreeEntry, error) {
	if len(b) == 0 {
		return []TreeEntry{}, nil
	}
	text, ok := strings.CutSuffix(string(b), "\n")
	if !ok {
		return nil, errors.New("hg: unterminated file list")
	}
	prefix := dir
	if prefix != "" {
		prefix += "/"
	}
	byName := map[string]TreeEntry{}
	for _, line := range strings.Split(text, "\n") {
		// "hg manifest -v" lines are mode, type and raw path: "644   a", "755 * a", "644 @ a".
		if len(line) < 7 {
			return nil, fmt.Errorf("hg: unrecognized file list line %q", line)
		}
		var flag byte
		switch line[:6] {
		case "644   ":
			flag = '.'
		case "755 * ":
			flag = 'x'
		case "644 @ ":
			flag = 'l'
		default:
			return nil, fmt.Errorf("hg: unrecognized file list line %q", line)
		}
		rel, ok := strings.CutPrefix(line[6:], prefix)
		if !ok {
			continue
		}
		for _, seg := range strings.Split(rel, "/") {
			if seg == "" {
				return nil, fmt.Errorf("hg: unrecognized file list line %q", line)
			}
		}
		name, _, isDir := strings.Cut(rel, "/")
		e := TreeEntry{Name: name, Directory: isDir}
		if !isDir {
			switch flag {
			case 'x':
				e.Executable = true
			case 'l':
				e.Symlink = true
			}
		}
		if prev, seen := byName[name]; seen {
			if prev.Directory || !isDir {
				continue
			}
		}
		byName[name] = e
	}
	return sortedTreeEntries(byName), nil
}

type treeTextEntry struct {
	TreeEntry
	node string
}

func (r *Runner) listTreeDirectory(ctx context.Context, repoPath string, node NodeID, dir string) ([]TreeEntry, error) {
	var out bytes.Buffer
	err := r.Run(ctx, repoPath, []string{"log", "-r", nodeRevset(node), "-T", "{manifest % '{node}'}"}, nil, &out)
	if err != nil {
		return nil, r.notServed(ctx, repoPath, node, err)
	}
	manifestNode, err := ParseNodeID(strings.TrimSpace(out.String()))
	if err != nil {
		return nil, fmt.Errorf("hg: manifest node %q", strings.TrimSpace(out.String()))
	}
	text, err := r.debugManifest(ctx, repoPath, manifestNode, "")
	if err != nil {
		return nil, err
	}
	cur := ""
	if dir != "" {
		for _, part := range strings.Split(dir, "/") {
			entries, err := parseTreeManifest(text)
			if err != nil {
				return nil, err
			}
			var child string
			for _, e := range entries {
				if e.Name == part && e.Directory {
					child = e.node
					break
				}
			}
			if child == "" {
				return nil, ErrNotFound
			}
			if cur != "" {
				cur += "/"
			}
			cur += part
			text, err = r.debugManifest(ctx, repoPath, NodeID(child), cur)
			if err != nil {
				return nil, err
			}
		}
	}
	entries, err := parseTreeManifest(text)
	if err != nil {
		return nil, err
	}
	byName := make(map[string]TreeEntry, len(entries))
	for _, e := range entries {
		byName[e.Name] = e.TreeEntry
	}
	return sortedTreeEntries(byName), nil
}

func (r *Runner) debugManifest(ctx context.Context, repoPath string, node NodeID, dir string) ([]byte, error) {
	args := []string{"debugdata", "-m", string(node)}
	if dir != "" {
		args = []string{"debugdata", "--dir", dir, string(node)}
	}
	var out bytes.Buffer
	if err := r.run(ctx, repoPath, args, nil, &out, flatDirectoryScanLimit); err != nil {
		return nil, err
	}
	return out.Bytes(), nil
}

func parseTreeManifest(b []byte) ([]treeTextEntry, error) {
	if len(b) == 0 {
		return []treeTextEntry{}, nil
	}
	text, ok := strings.CutSuffix(string(b), "\n")
	if !ok {
		return nil, errors.New("hg: unterminated manifest text")
	}
	var entries []treeTextEntry
	for _, line := range strings.Split(text, "\n") {
		name, rest, ok := strings.Cut(line, "\x00")
		if !ok || name == "" || strings.Contains(name, "/") || len(rest) < 40 {
			return nil, fmt.Errorf("hg: unrecognized manifest text %q", line)
		}
		node, flag := strings.ToLower(rest[:40]), rest[40:]
		if strings.Trim(node, "0123456789abcdef") != "" {
			return nil, fmt.Errorf("hg: unrecognized manifest text %q", line)
		}
		e := treeTextEntry{node: node, TreeEntry: TreeEntry{Name: name}}
		switch flag {
		case "":
		case "x":
			e.Executable = true
		case "l":
			e.Symlink = true
		case "t":
			e.Directory = true
		default:
			return nil, fmt.Errorf("hg: unrecognized manifest text %q", line)
		}
		entries = append(entries, e)
	}
	return entries, nil
}

func sortedTreeEntries(byName map[string]TreeEntry) []TreeEntry {
	entries := make([]TreeEntry, 0, len(byName))
	for _, e := range byName {
		entries = append(entries, e)
	}
	sort.Slice(entries, func(i, j int) bool { return entries[i].Name < entries[j].Name })
	return entries
}
