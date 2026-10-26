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
	"strings"
)

// Manifest lists the files of a served changeset from the store; nothing is
// checked out.
func (r *Runner) Manifest(ctx context.Context, path string, node NodeID) ([]ManifestEntry, error) {
	node, err := ParseNodeID(string(node))
	if err != nil {
		return nil, err
	}
	ctx, cancel := context.WithTimeout(ctx, BrowserCommandTimeout)
	defer cancel()
	var out bytes.Buffer
	if err = r.Run(ctx, path, []string{"manifest", "-r", nodeRevset(node), "-v"}, nil, &out); err != nil {
		return nil, r.notServed(ctx, path, node, err)
	}
	return parseManifest(out.Bytes())
}

// parseManifest reads "hg manifest -v" lines of mode, type and raw path, as
// in "644   a", "755 * a" and "644 @ a". Mercurial forbids newlines in file
// names, so the output is lossless in any encoding.
func parseManifest(b []byte) ([]ManifestEntry, error) {
	entries := []ManifestEntry{}
	s, ok := strings.CutSuffix(string(b), "\n")
	if !ok {
		if s == "" {
			return entries, nil
		}
		return nil, errors.New("hg: unterminated manifest output")
	}
	for _, line := range strings.Split(s, "\n") {
		if len(line) < 7 {
			return nil, fmt.Errorf("hg: unrecognized manifest line %q", line)
		}
		e := ManifestEntry{Path: line[6:]}
		switch line[:6] {
		case "644   ":
		case "755 * ":
			e.Executable = true
		case "644 @ ":
			e.Symlink = true
		default:
			return nil, fmt.Errorf("hg: unrecognized manifest line %q", line)
		}
		entries = append(entries, e)
	}
	return entries, nil
}

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
