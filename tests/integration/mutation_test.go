// Copyright 2026 The Gogs Authors. All rights reserved.
// Use of this source code is governed by a MIT-style
// license that can be found in the LICENSE file.

//go:build integration

package integration

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"regexp"
	"strings"
	"testing"
)

// template is the server repository every case starts from:
//
//	0 seed     public, bookmark "doomed"
//	1 tag v1   public
//	2 main     public, bookmark "main"
//	3 draft    draft (a local commit on the server)
type template struct {
	path   string
	nodes  []string
	stream string // stream clone bundle of the repository
}

func newTemplate(s *stack) *template {
	h := s.server
	tp := &template{path: filepath.Join(s.root, "template")}
	h.must(s.t, "", "init", tp.path)
	commit := func(name string) {
		writeFile(s.t, filepath.Join(tp.path, name), name)
		h.must(s.t, tp.path, "commit", "-A", "-m", name, "-u", "seed")
	}
	commit("seed")
	h.must(s.t, tp.path, "tag", "-r", "0", "-u", "seed", "-m", "tag v1", "v1")
	commit("main")
	h.must(s.t, tp.path, "bookmark", "-r", "2", "main")
	h.must(s.t, tp.path, "bookmark", "-r", "0", "doomed")
	h.must(s.t, tp.path, "phase", "--public", "-r", "2")
	commit("draft")
	tp.nodes = strings.Fields(h.stdout(s.t, tp.path, "log", "-r", "all()", "-T", "{node}\n"))
	tp.stream = filepath.Join(s.root, "template.hg")
	h.must(s.t, tp.path, "bundle", "-a", "-t", "none-v2;stream=v2", tp.stream)
	return tp
}

func writeFile(t *testing.T, path, content string) {
	t.Helper()
	if err := os.WriteFile(path, []byte(content+"\n"), 0600); err != nil {
		t.Fatal(err)
	}
}

func copyDir(t *testing.T, src, dst string) {
	t.Helper()
	err := filepath.WalkDir(src, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel, _ := filepath.Rel(src, p)
		target := filepath.Join(dst, rel)
		if d.IsDir() {
			return os.MkdirAll(target, 0700)
		}
		b, err := os.ReadFile(p)
		if err != nil {
			return err
		}
		return os.WriteFile(target, b, 0600)
	})
	if err != nil {
		t.Fatal(err)
	}
}

// caseEnv is one case's server repository and client clone.
type caseEnv struct {
	t                    *testing.T
	s                    *stack
	tp                   *template
	transport, user      string
	name, server, client string
	url                  string
	opts                 []string
}

func (c *caseEnv) commit(name string, extra ...string) {
	writeFile(c.t, filepath.Join(c.client, name), name)
	c.s.client.must(c.t, c.client, append([]string{"commit", "-A", "-m", name, "-u", "tester"}, extra...)...)
}

// push runs the native client; args go between "push" and the URL.
func (c *caseEnv) push(args ...string) (bool, string) {
	argv := append(append(append([]string{}, c.opts...), "push"), args...)
	out, err := c.s.client.run(context.Background(), c.client, append(argv, c.url)...)
	return pushSucceeded(err, out), out
}

// failureText is how Mercurial clients report a refused or partly refused
// push even when they exit 0 or 1.
var failureText = regexp.MustCompile(`(?i)abort|failed|rejected|not authorized|denied|error`)

// pushSucceeded: exit 0, or exit 1 for "no changes found" when only
// bookmarks or phases moved, with no reported failure.
func pushSucceeded(err error, out string) bool {
	var exit *exec.ExitError
	noChanges := errors.As(err, &exit) && exit.ExitCode() == 1 && strings.Contains(out, "no changes found")
	return (err == nil || noChanges) && !failureText.MatchString(out)
}

// wirePushkey sends a raw pushkey command over the case's transport.
func (c *caseEnv) wirePushkey(namespace, key, old, new string) (bool, string) {
	if c.transport == transportHTTP {
		status, body := c.s.httpWire(c.t, c.user, c.name, url.Values{
			"cmd": {"pushkey"}, "namespace": {namespace}, "key": {key}, "old": {old}, "new": {new},
		}, nil)
		return status == 200 && strings.HasPrefix(body, "1"), fmt.Sprintf("HTTP %d: %s", status, body)
	}
	out, errOut := c.s.sshWire(c.t, c.user, c.name, sshCommandBytes("pushkey",
		[2]string{"namespace", namespace}, [2]string{"key", key}, [2]string{"old", old}, [2]string{"new", new}))
	resp, ok := sshResponse(out)
	return ok && strings.HasPrefix(resp, "1"), out + errOut
}

// wireBatch sends cmds, already escaped, as one batch command.
func (c *caseEnv) wireBatch(cmds string) (string, string) {
	if c.transport == transportHTTP {
		status, body := c.s.httpWire(c.t, c.user, c.name, url.Values{"cmd": {"batch"}, "cmds": {cmds}}, nil)
		if status != 200 {
			return "", fmt.Sprintf("HTTP %d: %s", status, body)
		}
		return body, body
	}
	out, errOut := c.s.sshWire(c.t, c.user, c.name, append(sshCommandBytes("batch", [2]string{"cmds", cmds}), "* 0\n"...))
	resp, _ := sshResponse(out)
	return resp, out + errOut
}

// wireUnbundle forces bundle into the repository with one unbundle command.
func (c *caseEnv) wireUnbundle(bundle []byte) (bool, string) {
	const force = "666f726365" // hex("force"), the heads argument of a forced push
	if c.transport == transportHTTP {
		status, body := c.s.httpWire(c.t, c.user, c.name, url.Values{"cmd": {"unbundle"}, "heads": {force}}, bundle)
		return status == 200 && !strings.Contains(strings.ToLower(body), "error:") && !strings.Contains(body, "abort"),
			fmt.Sprintf("HTTP %d: %q", status, body)
	}
	req := append(sshCommandBytes("unbundle", [2]string{"heads", force}), sshPayload(bundle)...)
	out, errOut := c.s.sshWire(c.t, c.user, c.name, req)
	// Mandatory bundle2 part names are upper-cased: "ERROR:ABORT".
	return strings.HasPrefix(out, "0\n") && !strings.Contains(strings.ToLower(out), "error:") && !strings.Contains(errOut, "abort"),
		fmt.Sprintf("%q %s", out, errOut)
}

// mutation is one attempted write. run performs it and reports whether the
// server accepted it; check, for writers, verifies the change it made.
type mutation struct {
	name string
	// current marks cases that need a current client: bundle2, phases or
	// obsolescence. driver cases use test wire drivers instead of a client
	// and run once per server version, in the current-client rows.
	current, driver bool
	// empty starts the server repository empty instead of from the template.
	empty bool
	// unsupported writes must fail and change nothing, for writers too.
	unsupported bool
	// readerOnly cases have no writer control.
	readerOnly bool
	run        func(c *caseEnv) (bool, string)
	check      func(c *caseEnv, before, after repoState) error
}

func node(s repoState, n string) bool { return strings.Contains(s.Log, `"node": "`+n+`"`) }

// bookmark reports whether bookmark name exists and, unless n is "", points
// at node n.
func bookmark(s repoState, name, n string) bool {
	var marks []struct{ Bookmark, Node string }
	if err := json.Unmarshal([]byte(s.Bookmarks), &marks); err != nil {
		return false
	}
	for _, m := range marks {
		if m.Bookmark == name {
			return n == "" || m.Node == n
		}
	}
	return false
}

func (c *caseEnv) tip() string {
	return strings.TrimSpace(c.s.client.stdout(c.t, c.client, "log", "-r", "tip", "--template", "{node}"))
}

func phase(c *caseEnv, n string) string {
	return strings.TrimSpace(c.s.server.stdout(c.t, c.server, "log", "--hidden", "-r", n, "-T", "{phase}"))
}

func newNode(c *caseEnv, after repoState, wantPhase string) error {
	tip := c.tip()
	if !node(after, tip) {
		return fmt.Errorf("pushed changeset %s missing", tip)
	}
	if p := phase(c, tip); p != wantPhase {
		return fmt.Errorf("pushed changeset %s is %s, want %s", tip, p, wantPhase)
	}
	return nil
}

var bundle1 = []string{"--config", "devel.legacy.exchange=bundle1"}

var mutations = []mutation{
	{
		name: "push changeset",
		run:  func(c *caseEnv) (bool, string) { c.commit("change"); return c.push() },
		check: func(c *caseEnv, _, after repoState) error {
			if err := newNode(c, after, "public"); err != nil {
				return err
			}
			if p := phase(c, c.tp.nodes[3]); p != "public" {
				return fmt.Errorf("pushed descendant left its ancestor %s", p)
			}
			return nil
		},
	},
	{
		name: "push new head",
		run: func(c *caseEnv) (bool, string) {
			c.s.client.must(c.t, c.client, "update", "-r", "0")
			c.commit("new head")
			return c.push("-f")
		},
		check: func(c *caseEnv, before, after repoState) error {
			if len(strings.Fields(after.Heads)) != len(strings.Fields(before.Heads))+1 {
				return fmt.Errorf("heads %q, want one more than %q", after.Heads, before.Heads)
			}
			return newNode(c, after, "public")
		},
	},
	{
		name: "push changeset, bundle1",
		run:  func(c *caseEnv) (bool, string) { c.commit("bundle1 change"); return c.push(bundle1...) },
		check: func(c *caseEnv, _, after repoState) error {
			return newNode(c, after, "public")
		},
	},
	{
		name: "push tag",
		run: func(c *caseEnv) (bool, string) {
			c.s.client.must(c.t, c.client, "tag", "-r", "0", "-u", "tester", "-m", "tag v2", "v2")
			return c.push()
		},
		check: func(c *caseEnv, _, after repoState) error {
			if !strings.Contains(after.Tags, `"tag": "v2"`) {
				return errors.New("tag v2 missing")
			}
			return nil
		},
	},
}

// bookmarkMutations are the bookmark create/move/delete pushes, through
// bundle2 parts and through legacy pushkey (bundle1 exchange).
func bookmarkMutations() []mutation {
	var ms []mutation
	for _, legacy := range []bool{false, true} {
		suffix, extra := "", []string(nil)
		if legacy {
			suffix, extra = ", legacy pushkey", bundle1
		}
		ms = append(ms,
			mutation{
				name:    "bookmark create" + suffix,
				current: !legacy,
				run: func(c *caseEnv) (bool, string) {
					c.s.client.must(c.t, c.client, "bookmark", "-r", c.tp.nodes[2], "created")
					return c.push(append([]string{"-B", "created"}, extra...)...)
				},
				check: func(c *caseEnv, _, after repoState) error {
					if !bookmark(after, "created", c.tp.nodes[2]) {
						return fmt.Errorf("bookmark created missing: %s", after.Bookmarks)
					}
					return nil
				},
			},
			mutation{
				name:    "bookmark move" + suffix,
				current: !legacy,
				run: func(c *caseEnv) (bool, string) {
					c.s.client.must(c.t, c.client, "bookmark", "-f", "-r", c.tp.nodes[3], "main")
					return c.push(append([]string{"-B", "main"}, extra...)...)
				},
				check: func(c *caseEnv, before, after repoState) error {
					if after.Bookmarks == before.Bookmarks || !bookmark(after, "main", c.tp.nodes[3]) {
						return fmt.Errorf("bookmark main not moved: %s", after.Bookmarks)
					}
					return nil
				},
			},
			mutation{
				name:    "bookmark delete" + suffix,
				current: !legacy,
				run: func(c *caseEnv) (bool, string) {
					c.s.client.must(c.t, c.client, "bookmark", "-d", "doomed")
					return c.push(append([]string{"-B", "doomed"}, extra...)...)
				},
				check: func(c *caseEnv, _, after repoState) error {
					if bookmark(after, "doomed", "") {
						return fmt.Errorf("bookmark doomed not deleted: %s", after.Bookmarks)
					}
					return nil
				},
			},
		)
	}
	return ms
}

var publishedDraft = func(c *caseEnv, _, _ repoState) error {
	if p := phase(c, c.tp.nodes[3]); p != "public" {
		return fmt.Errorf("server draft changeset is %s after publishing", p)
	}
	return nil
}

var evolution = []string{"--config", "experimental.evolution=all"}

var phaseAndObsoleteMutations = []mutation{
	{
		// The clone came from a publishing server, so the client already
		// sees the server's draft changeset as public and pushes only that.
		name:    "phase publish",
		current: true,
		run:     func(c *caseEnv) (bool, string) { return c.push() },
		check:   publishedDraft,
	},
	{
		name:    "phase publish, legacy pushkey",
		current: true,
		run:     func(c *caseEnv) (bool, string) { return c.push(bundle1...) },
		check:   publishedDraft,
	},
	{
		// Evolution is enabled on the client only; the server advertises no
		// obsmarkers capability and must store none.
		name:    "push with obsolescence marker",
		current: true,
		run: func(c *caseEnv) (bool, string) {
			c.commit("to amend", evolution...)
			c.s.client.must(c.t, c.client, append(evolution, "commit", "--amend", "-m", "amended", "-u", "tester")...)
			if c.s.client.stdout(c.t, c.client, append(evolution, "debugobsolete")...) == "" {
				c.t.Fatal("amend created no obsolescence marker")
			}
			return c.push(evolution...)
		},
		check: func(c *caseEnv, _, after repoState) error {
			if after.Obsolete != "" {
				return fmt.Errorf("server stored obsolescence markers: %s", after.Obsolete)
			}
			return newNode(c, after, "public")
		},
	},
}

var wireMutations = []mutation{
	{
		name:   "wire pushkey bookmark",
		driver: true,
		run: func(c *caseEnv) (bool, string) {
			return c.wirePushkey("bookmarks", "wired", "", c.tp.nodes[2])
		},
		check: func(c *caseEnv, _, after repoState) error {
			if !bookmark(after, "wired", c.tp.nodes[2]) {
				return fmt.Errorf("bookmark wired missing: %s", after.Bookmarks)
			}
			return nil
		},
	},
	{
		name:   "wire pushkey phase",
		driver: true,
		run: func(c *caseEnv) (bool, string) {
			return c.wirePushkey("phases", c.tp.nodes[3], "1", "0")
		},
		check: publishedDraft,
	},
	{
		// The obsolete namespace's pushkey handler does not consult the
		// evolution settings, so this reaches the server's obsstore.
		name:        "wire pushkey obsolescence markers",
		driver:      true,
		unsupported: true,
		run: func(c *caseEnv) (bool, string) {
			key, value := obsoleteMarkerDump(c)
			return c.wirePushkey("obsolete", key, "", value)
		},
	},
	{
		name:   "wire batch with pushkey",
		driver: true,
		run: func(c *caseEnv) (bool, string) {
			resp, out := c.wireBatch("heads ;pushkey namespace=bookmarks,key=batched,old=,new=" + c.tp.nodes[2])
			results := strings.Split(resp, ";")
			return len(results) == 2 && strings.HasPrefix(results[1], "1"), out
		},
		check: func(c *caseEnv, _, after repoState) error {
			if !bookmark(after, "batched", c.tp.nodes[2]) {
				return fmt.Errorf("bookmark batched missing: %s", after.Bookmarks)
			}
			return nil
		},
	},
	{
		// A stream clone bundle is applied only to an empty repository and
		// writes store files directly, outside the transaction journal. No
		// client pushes one, so there is no writer control. Writers are
		// checked at pretxnopen like any other push.
		name:       "wire unbundle stream bundle into empty repository",
		driver:     true,
		empty:      true,
		readerOnly: true,
		run: func(c *caseEnv) (bool, string) {
			b, err := os.ReadFile(c.tp.stream)
			if err != nil {
				c.t.Fatal(err)
			}
			return c.wireUnbundle(b)
		},
	},
}

// obsoleteMarkerDump makes one marker in a scratch repository with evolution
// enabled and returns its pushkey encoding.
func obsoleteMarkerDump(c *caseEnv) (string, string) {
	scratch := filepath.Join(c.s.root, "markers-"+c.transport+"-"+c.user)
	if _, err := os.Stat(scratch); err != nil {
		c.s.client.must(c.t, "", "init", scratch)
		c.s.client.must(c.t, scratch, append(evolution, "debugobsolete", c.tp.nodes[3], strings.Repeat("1", 40))...)
	}
	out := c.s.client.stdout(c.t, "", append(evolution, "debugpushkey", scratch, "obsolete")...)
	key, value, ok := strings.Cut(strings.TrimSpace(out), "\t")
	if !ok {
		c.t.Fatalf("no marker dump in %q", out)
	}
	return key, value
}

func allMutations() []mutation {
	ms := append([]mutation{}, mutations...)
	ms = append(ms, bookmarkMutations()...)
	ms = append(ms, phaseAndObsoleteMutations...)
	return append(ms, wireMutations...)
}

// TestTransportMutationMatrix attempts every core write path as a read-only
// user and as a writer over both transports. Readers must fail and leave the
// repository exactly as it was; writers must succeed through the same paths.
func TestTransportMutationMatrix(t *testing.T) {
	for _, v := range matrix(t) {
		t.Run(v.name, func(t *testing.T) {
			s := newStack(t, v, stackOptions{})
			tp := newTemplate(s)
			for _, transport := range []string{transportHTTP, transportSSH} {
				t.Run(transport, func(t *testing.T) {
					for _, user := range []string{"reader", "writer"} {
						t.Run(user, func(t *testing.T) { runMutations(t, s, tp, v, transport, user) })
					}
				})
			}
		})
	}
}

func runMutations(t *testing.T, s *stack, tp *template, v versions, transport, user string) {
	// Reading must work for both: a clone and a pull through deny hooks show
	// that serving a publishing repository opens no transaction.
	readName := "read-" + transport + "-" + user
	readServer := filepath.Join(s.root, "repos", readName)
	copyDir(t, tp.path, readServer)
	s.addRepo(readName, readServer)
	u, opts := s.remote(t, transport, user, readName)
	clone := filepath.Join(s.root, "clones", transport+"-"+user)
	before := snapshotRepository(t, s.server, readServer)
	s.client.must(t, "", append(append([]string{}, opts...), "clone", u, clone)...)
	s.client.must(t, clone, append(append([]string{}, opts...), "pull", u)...)
	if after := snapshotRepository(t, s.server, readServer); !reflect.DeepEqual(before, after) {
		t.Fatalf("clone and pull changed the repository:\n%s", before.diff(after))
	}

	for i, m := range allMutations() {
		if m.current && v.legacyClient || m.driver && v.legacyClient || m.readerOnly && user == "writer" {
			continue
		}
		t.Run(m.name, func(t *testing.T) {
			c := &caseEnv{t: t, s: s, tp: tp, transport: transport, user: user, opts: opts}
			c.name = fmt.Sprintf("%s-%s-%d", transport, user, i)
			c.server = filepath.Join(s.root, "repos", c.name)
			c.client = filepath.Join(s.root, "clients", c.name)
			if m.empty {
				s.server.must(t, "", "init", c.server)
			} else {
				copyDir(t, tp.path, c.server)
			}
			copyDir(t, clone, c.client)
			s.addRepo(c.name, c.server)
			c.url, _ = s.remote(t, transport, user, c.name)

			before := snapshotRepository(t, s.server, c.server)
			operationSucceeded, out := m.run(c)
			after := snapshotRepository(t, s.server, c.server)
			t.Logf("succeeded=%v\n%s", operationSucceeded, out)
			switch {
			case user == "reader":
				if operationSucceeded || !reflect.DeepEqual(before, after) {
					t.Fatalf("read-only operation changed repository state (succeeded=%v):\n%s\n%s", operationSucceeded, before.diff(after), out)
				}
			case m.unsupported:
				if operationSucceeded || !reflect.DeepEqual(before.withoutJournal(), after.withoutJournal()) {
					t.Fatalf("unsupported write changed repository state (succeeded=%v):\n%s\n%s", operationSucceeded, before.diff(after), out)
				}
			default:
				if !operationSucceeded {
					t.Fatalf("writer operation failed:\n%s", out)
				}
				if err := m.check(c, before, after); err != nil {
					t.Fatalf("writer operation: %v\n%s", err, out)
				}
			}
			s.server.must(t, c.server, "verify", "-q")
		})
	}
}
