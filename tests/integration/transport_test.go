// Copyright 2026 The Gogs Authors. All rights reserved.
// Use of this source code is governed by a MIT-style
// license that can be found in the LICENSE file.

//go:build integration

package integration

import (
	"bytes"
	"context"
	"crypto/tls"
	"crypto/x509"
	"encoding/base64"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"golang.org/x/crypto/ssh"

	"github.com/gogits/gogs/modules/hg"
	gssh "github.com/gogits/gogs/modules/ssh"
)

// newCase copies the template into a fresh server repository and clones it
// as user over transport.
func newCase(t *testing.T, s *stack, tp *template, transport, user, name string) *caseEnv {
	c := &caseEnv{t: t, s: s, tp: tp, transport: transport, user: user, name: name}
	c.server = filepath.Join(s.root, "repos", name)
	c.client = filepath.Join(s.root, "clients", name)
	copyDir(t, tp.path, c.server)
	s.addRepo(name, c.server)
	c.url, c.opts = s.remote(t, transport, user, name)
	s.client.must(t, "", append(append([]string{}, c.opts...), "clone", c.url, c.client)...)
	return c
}

// TestTransportRevocation revokes the writer after the transaction opened:
// the transaction-close hooks must reject it and the push must roll back.
func TestTransportRevocation(t *testing.T) {
	for _, v := range matrix(t) {
		t.Run(v.name, func(t *testing.T) {
			s := newStack(t, v, stackOptions{})
			tp := newTemplate(s)
			for _, transport := range []string{transportHTTP, transportSSH} {
				for _, exchange := range []struct {
					name string
					args []string
				}{{"bundle2", nil}, {"bundle1", bundle1}} {
					if v.legacyClient && exchange.name == "bundle2" {
						continue
					}
					t.Run(transport+" "+exchange.name, func(t *testing.T) {
						c := newCase(t, s, tp, transport, "writer", "revoke-"+transport+"-"+exchange.name)
						c.commit("revoked change")
						c.s.client.must(t, c.client, "bookmark", "-r", "tip", "revoked")

						before := snapshotRepository(t, s.server, c.server)
						s.authz.reset(func(_ context.Context, call int) {
							if call == 1 {
								s.authz.setWriter(writerID, false)
							}
						})
						ok, out := c.push(append([]string{"-B", "revoked"}, exchange.args...)...)
						calls := s.authz.callCount()
						s.authz.reset(nil)
						s.authz.setWriter(writerID, true)
						after := snapshotRepository(t, s.server, c.server)
						if ok || !reflect.DeepEqual(before.withoutJournal(), after.withoutJournal()) {
							t.Fatalf("push by a writer revoked mid-transaction changed the repository (succeeded=%v):\n%s\n%s", ok, before.diff(after), out)
						}
						if calls < 2 {
							t.Fatalf("only %d hook callbacks; the revocation was not tested at transaction close:\n%s", calls, out)
						}
						s.server.must(t, c.server, "verify", "-q")

						// Restored access: the same push now succeeds.
						if ok, out = c.push(append([]string{"-B", "revoked"}, exchange.args...)...); !ok {
							t.Fatalf("push after access was restored failed:\n%s", out)
						}
						if after = snapshotRepository(t, s.server, c.server); !bookmark(after, "revoked", c.tip()) {
							t.Fatalf("restored push did not land: %s", after.Bookmarks)
						}
					})
				}
			}
		})
	}
}

// TestTransportAmbiguousCompletion drops the connection of a push after
// Mercurial committed it but before the client read the result. The push
// must be present exactly once, the bridge must not replay it, nothing may
// keep running, and the client reconciles by pushing again.
func TestTransportAmbiguousCompletion(t *testing.T) {
	v := matrix(t)[0]
	s := newStack(t, v, stackOptions{})
	tp := newTemplate(s)
	for _, transport := range []string{transportHTTP, transportSSH} {
		t.Run(transport, func(t *testing.T) {
			c := newCase(t, s, tp, transport, "writer", "ambiguous-"+transport)
			c.commit("ambiguous change")
			tip := c.tip()
			bundle := filepath.Join(s.root, "ambiguous-"+transport+".hg")
			s.client.must(t, c.client, "bundle", "-r", tip, "--base", tp.nodes[3], "-t", "none-v2", bundle)
			body, err := os.ReadFile(bundle)
			if err != nil {
				t.Fatal(err)
			}

			before := snapshotRepository(t, s.server, c.server)
			requests, sessions := s.requestCount("unbundle"), s.sessionCount()
			s.authz.reset(nil)
			committed := func() bool {
				out, err := s.server.run(context.Background(), c.server, "log", "--hidden", "-r", tip, "-T", "{node}")
				return err == nil && out == tip
			}
			s.dropAfter(t, transport, c.name, body, committed)
			calls := s.authz.callCount()
			if left := waitNoProcesses(c.server); len(left) > 0 {
				t.Fatalf("worker still running after the client went away:\n%s", strings.Join(left, "\n"))
			}

			after := snapshotRepository(t, s.server, c.server)
			if n := strings.Count(after.Log, `"node": "`); n != strings.Count(before.Log, `"node": "`)+1 {
				t.Fatalf("%d changesets after one pushed changeset, before %d", n, strings.Count(before.Log, `"node": "`))
			}
			if got := s.requestCount("unbundle") - requests + s.sessionCount() - sessions; got != 1 {
				t.Fatalf("%d unbundle requests or sessions reached the server for one push", got)
			}
			if s.authz.callCount() != calls {
				t.Fatal("hook callbacks after the push ended: the push was replayed")
			}
			s.server.must(t, c.server, "verify", "-q")

			// The client reconciles: its changeset is already there.
			ok, out := c.push()
			if !ok || !strings.Contains(out, "no changes found") {
				t.Fatalf("reconciling push after the ambiguous one: succeeded=%v\n%s", ok, out)
			}
			if final := snapshotRepository(t, s.server, c.server); final.Log != after.Log {
				t.Fatalf("reconciling push changed history:\n%s", after.diff(final))
			}
		})
	}
}

// dropAfter forces bundle into repository name as the writer with one raw
// unbundle request, waits until done reports the result is committed, and
// closes the connection without reading the response.
func (s *stack) dropAfter(t *testing.T, transport, name string, bundle []byte, done func() bool) {
	const force = "666f726365"
	if transport == transportHTTP {
		pem, err := os.ReadFile(s.cacerts)
		if err != nil {
			t.Fatal(err)
		}
		pool := x509.NewCertPool()
		pool.AppendCertsFromPEM(pem)
		conn, err := tls.Dial("tcp", s.frontend.Listener.Addr().String(), &tls.Config{RootCAs: pool, ServerName: "localhost"})
		if err != nil {
			t.Fatal(err)
		}
		defer conn.Close()
		fmt.Fprintf(conn, "POST /hg/owner/%s?cmd=unbundle&heads=%s HTTP/1.1\r\nHost: localhost\r\nAuthorization: Basic %s\r\n"+
			"Content-Type: application/mercurial-0.1\r\nContent-Length: %d\r\n\r\n",
			name, force, base64.StdEncoding.EncodeToString([]byte("writer:"+httpUsers["writer"])), len(bundle))
		if _, err = conn.Write(bundle); err != nil {
			t.Fatal(err)
		}
		waitFor(t, "the push to commit", done)
		return
	}
	c, err := ssh.Dial("tcp", s.sshAddr, &ssh.ClientConfig{
		User:            gssh.ServiceUser,
		Auth:            []ssh.AuthMethod{ssh.PublicKeys(s.signers["writer"])},
		HostKeyCallback: ssh.FixedHostKey(s.hostKey),
		Timeout:         hg.StartupTimeout,
	})
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	sess, err := c.NewSession()
	if err != nil {
		t.Fatal(err)
	}
	in, err := sess.StdinPipe()
	if err != nil {
		t.Fatal(err)
	}
	if err = sess.Start("hg -R owner/" + name + " serve --stdio"); err != nil {
		t.Fatal(err)
	}
	req := append(sshCommandBytes("unbundle", [2]string{"heads", force}), sshPayload(bundle)...)
	if _, err = bytes.NewReader(req).WriteTo(in); err != nil {
		t.Fatal(err)
	}
	waitFor(t, "the push to commit", done)
}

// TestKnownGapStreamBundleRevocation records a known gap, see "The gap:
// stream bundles survive revocation" in docs/mercurial/transport-evidence.md.
// A stream clone bundle unbundled into an empty repository writes store
// files outside the transaction journal, so when a writer revoked after
// pretxnopen is refused at transaction close, the history stays. The test
// asserts that current behaviour; it fails once Mercurial or a later design
// closes the gap, and must then become a revocation assertion.
func TestKnownGapStreamBundleRevocation(t *testing.T) {
	v := matrix(t)[0]
	s := newStack(t, v, stackOptions{})
	tp := newTemplate(s)
	bundle, err := os.ReadFile(tp.stream)
	if err != nil {
		t.Fatal(err)
	}
	for _, transport := range []string{transportHTTP, transportSSH} {
		t.Run(transport, func(t *testing.T) {
			name := "known-gap-" + transport
			c := &caseEnv{t: t, s: s, tp: tp, transport: transport, user: "writer", name: name}
			c.server = filepath.Join(s.root, "repos", name)
			s.server.must(t, "", "init", c.server)
			s.addRepo(name, c.server)

			s.authz.reset(func(_ context.Context, call int) {
				if call == 1 {
					s.authz.setWriter(writerID, false)
				}
			})
			ok, out := c.wireUnbundle(bundle)
			calls := s.authz.callCount()
			s.authz.reset(nil)
			s.authz.setWriter(writerID, true)
			after := snapshotRepository(t, s.server, c.server)
			if ok || calls < 2 {
				t.Fatalf("expected the close-time hook to refuse the revoked writer (succeeded=%v, %d callbacks):\n%s", ok, calls, out)
			}
			if !node(after, tp.nodes[3]) {
				t.Fatalf("known gap no longer reproduces: the refused stream bundle left no history. "+
					"Turn this into a revocation assertion and update the evidence doc.\n%s", out)
			}
			t.Logf("known gap reproduced: history present after the close-time refusal\n%s", out)
		})
	}
}
