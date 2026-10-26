//go:build ignore

// Copyright 2026 The Gogs Authors. All rights reserved.
// Use of this source code is governed by the MIT-style LICENSE.

// Build-only portable archive command. Run from the source root.
package main

import (
	"archive/zip"
	"debug/buildinfo"
	"flag"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

var files = []string{
	"conf/app.ini", "conf/locale/TRANSLATORS.md", "LICENSE", "README.md", "CONTRIBUTORS.md", "templates/.VERSION",
	"docs/mercurial/installation.md", "docs/mercurial/operations.md", "docs/mercurial/compatibility.md",
	"docs/mercurial/operator-notes.md", "docs/mercurial/transport-evidence.md",
	"docs/mercurial/removed-features.md", "docs/mercurial/build-baseline.md",
}
var trees = []string{"public", "templates", "conf/locale"}

// Check every existing path component: WalkDir alone does not reject a
// symlink in the path leading to its root. Release sources must be quiescent.
func regularPath(name string) error {
	for p := name; ; p = filepath.Dir(p) {
		info, err := os.Lstat(p)
		if err != nil {
			return err
		}
		if info.Mode()&os.ModeSymlink != 0 || (!info.IsDir() && !info.Mode().IsRegular()) {
			return fmt.Errorf("not a regular file/directory: %s", p)
		}
		if filepath.Dir(p) == p {
			return nil
		}
	}
}

func within(name, dir string) bool {
	rel, err := filepath.Rel(dir, name)
	return err == nil && rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator))
}

func packageRelease(root, binary, out string) (err error) {
	if root, err = filepath.Abs(root); err != nil {
		return err
	}
	if binary, err = filepath.Abs(binary); err != nil {
		return err
	}
	if out, err = filepath.Abs(out); err != nil {
		return err
	}
	if err = regularPath(root); err != nil {
		return err
	}
	if err = regularPath(binary); err != nil {
		return err
	}
	info, err := os.Stat(binary)
	if err != nil {
		return err
	}
	if !info.Mode().IsRegular() {
		return fmt.Errorf("binary is not regular")
	}
	build, err := buildinfo.ReadFile(binary)
	if err != nil || build.Path != "github.com/gogits/gogs" {
		return fmt.Errorf("binary must be a built github.com/gogits/gogs executable")
	}
	binaryName := "gogs"
	for _, s := range build.Settings {
		if s.Key == "GOOS" && s.Value == "windows" {
			binaryName = "gogs.exe"
		}
	}
	if strings.EqualFold(binary, out) {
		return fmt.Errorf("output equals binary")
	}
	if _, err := os.Lstat(out); !os.IsNotExist(err) {
		return fmt.Errorf("output already exists or cannot be inspected: %s", out)
	}
	for _, tree := range []string{"public", "templates", "conf", "docs"} {
		dir := filepath.Join(root, tree)
		if within(out, dir) || within(binary, dir) {
			return fmt.Errorf("binary/output inside package input tree: %s", tree)
		}
	}
	// A selected executable inside the checkout must be a deliberate build
	// output, never a file taken from an operator's custom/data/runtime tree.
	if within(binary, root) && filepath.Dir(binary) != root && !within(binary, filepath.Join(root, "dist")) {
		return fmt.Errorf("binary inside non-build source directory")
	}
	sources := map[string]string{binaryName: binary}
	selected := append([]string{}, files...)
	if _, err := os.Lstat(filepath.Join(root, "README_ZH.md")); err == nil {
		selected = append(selected, "README_ZH.md")
	} else if !os.IsNotExist(err) {
		return err
	}
	for _, name := range selected {
		p := filepath.Join(root, filepath.FromSlash(name))
		if err := regularPath(p); err != nil {
			return err
		}
		info, err := os.Stat(p)
		if err != nil {
			return err
		}
		if !info.Mode().IsRegular() {
			return fmt.Errorf("required file is not regular: %s", p)
		}
		sources[name] = p
	}
	for _, tree := range trees {
		p := filepath.Join(root, filepath.FromSlash(tree))
		if err := regularPath(p); err != nil {
			return err
		}
		if err := filepath.WalkDir(p, func(p string, d fs.DirEntry, walkErr error) error {
			if walkErr != nil {
				return walkErr
			}
			rel, err := filepath.Rel(root, p)
			if err != nil {
				return err
			}
			name := filepath.ToSlash(rel)
			if d.Type()&os.ModeSymlink != 0 {
				return fmt.Errorf("symlink in source: %s", p)
			}
			if name == "public/img/avatar" || name == "public/avatar" {
				if !d.IsDir() {
					return fmt.Errorf("avatar cache is not a directory: %s", p)
				}
				return filepath.SkipDir
			}
			base := strings.ToLower(d.Name())
			switch base {
			case ".git", ".hg", ".aws", ".codex", ".agents", "custom", "data", "repos", "uploads", "cache", "__pycache__":
				return fmt.Errorf("private/runtime tree in assets: %s", p)
			}
			if d.IsDir() {
				return nil
			}
			info, err := d.Info()
			if err != nil {
				return err
			}
			if !info.Mode().IsRegular() {
				return fmt.Errorf("special file in source: %s", p)
			}
			switch strings.ToLower(filepath.Ext(base)) {
			case ".pem", ".key", ".crt", ".pfx", ".db", ".sqlite", ".sqlite3", ".log", ".exe":
				return fmt.Errorf("private/runtime file in assets: %s", p)
			}
			ext := strings.ToLower(filepath.Ext(base))
			allowed := false
			switch tree {
			case "public":
				switch ext {
				case ".css", ".eot", ".jpg", ".js", ".less", ".map", ".otf", ".png", ".svg", ".swf", ".ttf", ".woff":
					allowed = true
				}
			case "templates":
				allowed = ext == ".tmpl" || name == "templates/.VERSION"
			case "conf/locale":
				allowed = ext == ".ini" || name == "conf/locale/TRANSLATORS.md"
			}
			if !allowed {
				return fmt.Errorf("unexpected asset type: %s", p)
			}
			sources[name] = p
			return nil
		}); err != nil {
			return err
		}
	}
	if err := os.MkdirAll(filepath.Dir(out), 0755); err != nil {
		return err
	}
	if err := regularPath(filepath.Dir(out)); err != nil {
		return err
	}
	f, err := os.OpenFile(out, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0644)
	if err != nil {
		return err
	}
	good := false
	defer func() {
		f.Close()
		if !good {
			os.Remove(out)
		}
	}()
	z := zip.NewWriter(f)
	// Walk order is fixed by lexical names, independent of map iteration.
	names := make([]string, 0, len(sources))
	for name := range sources {
		names = append(names, name)
	}
	sort.Strings(names)
	for _, name := range names {
		p := sources[name]
		if err := regularPath(p); err != nil {
			z.Close()
			return err
		}
		in, err := os.Open(p)
		if err != nil {
			z.Close()
			return err
		}
		info, err := in.Stat()
		if err != nil {
			in.Close()
			z.Close()
			return err
		}
		h, err := zip.FileInfoHeader(info)
		if err != nil {
			in.Close()
			z.Close()
			return err
		}
		h.Name, h.Method = name, zip.Deflate
		h.SetMode(0644)
		if name == binaryName {
			h.SetMode(0755)
		}
		w, err := z.CreateHeader(h)
		if err == nil {
			_, err = io.Copy(w, in)
		}
		closeErr := in.Close()
		if err != nil {
			z.Close()
			return err
		}
		if closeErr != nil {
			z.Close()
			return closeErr
		}
	}
	if err = z.Close(); err != nil {
		return err
	}
	if err = f.Close(); err != nil {
		return err
	}
	good = true
	return nil
}

func main() {
	binary := flag.String("binary", "", "native Gogs executable built from this source/assets")
	out := flag.String("out", "", "new zip archive (will not overwrite)")
	flag.Parse()
	if *binary == "" || *out == "" || flag.NArg() != 0 {
		fmt.Fprintln(os.Stderr, "usage: go run scripts/package.go -binary <native binary> -out <archive path>")
		os.Exit(1)
	}
	if err := packageRelease(".", *binary, *out); err != nil {
		fmt.Fprintln(os.Stderr, "package:", err)
		os.Exit(1)
	}
	fmt.Println("Created", *out)
}
