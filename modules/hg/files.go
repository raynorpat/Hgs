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

// flatDirectoryScanLimit bounds the manifest text of one tree-manifest
// directory. Flat manifests are scanned as a stream instead (flatScan).
const flatDirectoryScanLimit = 32 << 20

// A streamed flat-manifest scan keeps only the children of one directory, so
// memory follows these bounds rather than the repository size; the browser
// command timeout bounds its time.
const (
	maxFlatChildren = 100000
	maxFlatLine     = 64 << 10
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
	scan, err := r.scanFlat(ctx, repoPath, node, dir)
	if err != nil {
		return nil, err
	}
	return scan.entries(), nil
}

// scanFlat streams "hg manifest -v" through a flatScan for path p. The
// per-file flags template of "hg files" costs a lookup per file that
// dominated large trees; manifest -v reports the flags in one pass. It takes
// no path patterns, so the scan selects p.
func (r *Runner) scanFlat(ctx context.Context, repoPath string, node NodeID, p string) (*flatScan, error) {
	scan := &flatScan{dir: p, prefix: p, byName: map[string]TreeEntry{}}
	if p != "" {
		scan.prefix += "/"
	}
	err := r.run(ctx, repoPath, []string{"manifest", "-r", nodeRevset(node), "-v"}, nil, scan, -1)
	if scan.err != nil {
		return nil, scan.err
	}
	var exit *exec.ExitError
	switch {
	case err == nil:
		return scan, scan.finish()
	case errors.As(err, &exit) && exit.ExitCode() == 1:
		return scan, nil
	default:
		return nil, r.notServed(ctx, repoPath, node, err)
	}
}

// ListPath resolves p at a served changeset to a directory's children or to
// the file entry it names; an unknown path is ErrNotFound. Flat manifests
// answer both from one streamed scan.
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
	if !usesTreeManifest(repoPath) {
		scan, err := r.scanFlat(ctx, repoPath, node, p)
		if err != nil {
			return nil, nil, err
		}
		if entries := scan.entries(); len(entries) > 0 {
			return entries, nil, nil
		}
		if scan.file != nil {
			return nil, scan.file, nil
		}
		return nil, nil, ErrNotFound
	}
	parent, name := "", p
	if i := strings.LastIndex(p, "/"); i >= 0 {
		parent, name = p[:i], p[i+1:]
	}
	listed, err := r.listTreeDirectory(ctx, repoPath, node, parent)
	if err != nil {
		return nil, nil, err
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

// flatScan consumes "hg manifest -v" lines of mode, type and raw path ("644   a",
// "755 * a", "644 @ a") as a stream. It keeps the immediate children of dir and
// the entry for the file dir itself names, if any. Mercurial forbids newlines in
// file names, so lines are lossless in any encoding.
type flatScan struct {
	dir, prefix string
	byName      map[string]TreeEntry
	file        *TreeEntry
	carry       []byte
	err         error
}

func (s *flatScan) Write(p []byte) (int, error) {
	n := len(p)
	for len(p) > 0 && s.err == nil {
		i := bytes.IndexByte(p, '\n')
		if i < 0 {
			if len(s.carry)+len(p) > maxFlatLine {
				s.err = errors.New("hg: file list line too long")
			} else {
				s.carry = append(s.carry, p...)
			}
			break
		}
		line := p[:i]
		if len(s.carry) > 0 {
			if len(s.carry)+len(line) > maxFlatLine {
				s.err = errors.New("hg: file list line too long")
				break
			}
			s.carry = append(s.carry, line...)
			line = s.carry
		}
		s.err = s.line(line)
		s.carry = s.carry[:0]
		p = p[i+1:]
	}
	if s.err != nil {
		return 0, s.err
	}
	return n, nil
}

// finish reports output that stopped mid-line.
func (s *flatScan) finish() error {
	if len(s.carry) > 0 {
		return errors.New("hg: unterminated file list")
	}
	return nil
}

func (s *flatScan) line(b []byte) error {
	if len(b) < 7 {
		return fmt.Errorf("hg: unrecognized file list line %q", b)
	}
	var flag byte
	switch string(b[:6]) {
	case "644   ":
		flag = '.'
	case "755 * ":
		flag = 'x'
	case "644 @ ":
		flag = 'l'
	default:
		return fmt.Errorf("hg: unrecognized file list line %q", b)
	}
	path := b[6:]
	if s.dir != "" && string(path) == s.dir {
		e := TreeEntry{Name: s.dir[strings.LastIndexByte(s.dir, '/')+1:], Executable: flag == 'x', Symlink: flag == 'l'}
		s.file = &e
		return nil
	}
	if len(path) <= len(s.prefix) || string(path[:len(s.prefix)]) != s.prefix {
		return nil
	}
	rel := string(path[len(s.prefix):])
	for _, seg := range strings.Split(rel, "/") {
		if seg == "" {
			return fmt.Errorf("hg: unrecognized file list line %q", b)
		}
	}
	name, _, isDir := strings.Cut(rel, "/")
	e := TreeEntry{Name: name, Directory: isDir}
	if !isDir {
		e.Executable, e.Symlink = flag == 'x', flag == 'l'
	}
	if prev, seen := s.byName[name]; seen {
		if prev.Directory || !isDir {
			return nil
		}
	} else if len(s.byName) >= maxFlatChildren {
		return ErrOutputLimit
	}
	s.byName[name] = e
	return nil
}

func (s *flatScan) entries() []TreeEntry { return sortedTreeEntries(s.byName) }

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
