// Copyright 2026 The Gogs Authors. All rights reserved.
// Use of this source code is governed by a MIT-style
// license that can be found in the LICENSE file.

//go:build integration

package integration

import (
	"bytes"
	"context"
	"encoding/binary"
	"io/fs"
	"net/url"
	"os"
	"path/filepath"
	"reflect"
	"sort"
	"strconv"
	"strings"
	"testing"
)

// streamFile is one file of a stream2 payload: src is 's' for the store
// vfs or 'c' for the cache vfs.
type streamFile struct {
	src           byte
	name, content string
}

// streamBundle builds a bundle2 with one mandatory stream2 part carrying
// files and the given requirements (Mercurial 7.2.4 bundle2.py part framing,
// streamclone._v2_parse_files payload).
func streamBundle(requirements string, files []streamFile) []byte {
	var payload bytes.Buffer
	size := 0
	for _, f := range files {
		payload.WriteByte(f.src)
		payload.Write(binary.AppendUvarint(nil, uint64(len(f.name))))
		payload.Write(binary.AppendUvarint(nil, uint64(len(f.content))))
		payload.WriteString(f.name + f.content)
		size += len(f.content)
	}
	params := [][2]string{
		{"requirements", url.QueryEscape(requirements)},
		{"filecount", strconv.Itoa(len(files))},
		{"bytecount", strconv.Itoa(size)},
	}
	var header bytes.Buffer
	header.WriteByte(byte(len("STREAM2")))
	header.WriteString("STREAM2")
	binary.Write(&header, binary.BigEndian, uint32(0))
	header.Write([]byte{byte(len(params)), 0})
	for _, p := range params {
		header.Write([]byte{byte(len(p[0])), byte(len(p[1]))})
	}
	for _, p := range params {
		header.WriteString(p[0] + p[1])
	}
	var b bytes.Buffer
	b.WriteString("HG20")
	binary.Write(&b, binary.BigEndian, int32(0))
	binary.Write(&b, binary.BigEndian, int32(header.Len()))
	b.Write(header.Bytes())
	binary.Write(&b, binary.BigEndian, int32(payload.Len()))
	b.Write(payload.Bytes())
	binary.Write(&b, binary.BigEndian, int32(0)) // end of part payload
	binary.Write(&b, binary.BigEndian, int32(0)) // end of bundle
	return b.Bytes()
}

// hgFiles lists every file under repo/.hg except locks.
func hgFiles(t *testing.T, repo string) map[string]string {
	files := map[string]string{}
	root := filepath.Join(repo, ".hg")
	err := filepath.WalkDir(root, func(p string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() || strings.HasSuffix(p, "lock") {
			return err
		}
		b, err := os.ReadFile(p)
		rel, _ := filepath.Rel(root, p)
		files[filepath.ToSlash(rel)] = string(b)
		return err
	})
	if err != nil {
		t.Fatal(err)
	}
	return files
}

// TestStreamBundleFileNames records what an authorized writer's hand-built
// stream bundle can write into an empty repository besides revlogs (see
// "Stream bundles: which files they can write" in
// docs/mercurial/transport-evidence.md). It asserts the properties that
// matter: nothing outside .hg/store and .hg/cache, no repository hgrc, no
// hooks or other configuration picked up from the written files.
func TestStreamBundleFileNames(t *testing.T) {
	s := newStack(t, matrix(t)[0], stackOptions{})
	const reqs = "dotencode,fncache,generaldelta,revlogv1,share-safe,sparserevlog,store"
	const hook = "[hooks]\npretxnopen.stream = echo injected\n[ui]\nusername = injected\n"
	for _, tc := range []struct {
		name, reqs string
		files      []streamFile
		// lands lists the streamed files expected under .hg afterwards.
		lands []string
		// opens is false when the repository can no longer be opened.
		opens bool
	}{
		{name: "odd names", reqs: reqs, opens: true, files: []streamFile{
			{'s', "hgrc", hook},
			{'s', "requires", "shared\n"},
			{'s', "data/odd.i", "not a revlog"},
			{'s', "Upper/x", "upper"},
			{'c', "hgrc", hook},
			{'c', "branch2-served", "garbage"},
		}, lands: []string{"store/hgrc", "store/data/odd.i", "store/_upper/x", "cache/hgrc", "cache/branch2-served"}},
		// The store's dot-encoding turns ".." into "~2e~2e".
		{name: "store traversal", reqs: reqs, opens: true, files: []streamFile{{'s', "../hgrc", hook}}, lands: []string{"store/~2e~2e/hgrc"}},
		{name: "cache traversal", reqs: reqs, opens: true, files: []streamFile{{'c', "../hgrc", hook}}},
		{name: "nested repository", reqs: reqs, opens: true, files: []streamFile{{'c', "x/.hg/hgrc", hook}}},
		{name: "unknown vfs", reqs: reqs, opens: true, files: []streamFile{{'w', "hgrc", hook}}},
		// "shared" without .hg/sharedpath, which no stream can write, leaves
		// a repository Mercurial refuses to open.
		{name: "shared requirement", reqs: reqs + ",shared", opens: false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			name := "stream-" + strings.ReplaceAll(tc.name, " ", "-")
			c := &caseEnv{t: t, s: s, user: "writer", name: name, transport: transportSSH}
			c.server = filepath.Join(s.root, "repos", name)
			s.server.must(t, "", "init", c.server)
			s.addRepo(name, c.server)
			before := hgFiles(t, c.server)

			_, out := c.wireUnbundle(streamBundle(tc.reqs, tc.files))
			after := hgFiles(t, c.server)
			var changed, streamed []string
			for f, v := range after {
				if before[f] == v {
					continue
				}
				changed = append(changed, f)
				// Mercurial's own transaction bookkeeping and requirements.
				switch {
				case f == "branch", f == "requires", f == "store/requires", f == "store/fncache",
					strings.HasPrefix(f, "journal."), strings.HasPrefix(f, "undo."), strings.HasPrefix(f, "store/undo."):
				case strings.HasPrefix(f, "store/"), strings.HasPrefix(f, "cache/"):
					streamed = append(streamed, f)
				default:
					t.Errorf("stream bundle wrote .hg/%s", f)
				}
			}
			sort.Strings(changed)
			sort.Strings(streamed)
			want := append([]string(nil), tc.lands...)
			sort.Strings(want)
			t.Logf("written or changed under .hg: %q; requires %q; store/fncache %q\n%s", changed, after["requires"], after["store/fncache"], out)
			if !reflect.DeepEqual(streamed, want) && !(len(streamed) == 0 && len(want) == 0) {
				t.Errorf("streamed files %q, want %q", streamed, want)
			}
			if _, err := os.Stat(filepath.Join(c.server, ".hg", "hgrc")); err == nil {
				t.Error("stream bundle created the repository hgrc")
			}
			cfg, err := s.server.run(context.Background(), c.server, "config", "--source")
			if (err == nil) != tc.opens {
				t.Errorf("repository opens: %v, want %v\n%s", err, tc.opens, cfg)
			}
			if strings.Contains(cfg, "injected") {
				t.Errorf("configuration picked up a streamed file:\n%s", cfg)
			}
		})
	}
}
