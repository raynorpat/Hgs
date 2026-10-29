// Copyright 2026 The Gogs Authors. All rights reserved.
// Use of this source code is governed by a MIT-style
// license that can be found in the LICENSE file.

package cmd

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"net/http"
	"os"
	"strconv"
	"strings"

	"github.com/codegangsta/cli"

	"github.com/gogits/gogs/modules/hgbridge"
)

// CmdHGHook is run by Mercurial as a transaction hook. It reads no
// configuration, database or log files: everything it needs is in its
// arguments and the session environment Mercurial passes down.
var CmdHGHook = cli.Command{
	Name:        hgbridge.HookCommand,
	Usage:       "Internal Mercurial hook; never run it by hand",
	Description: `Allow or reject a Mercurial transaction: "deny <hook>" always rejects, "check <hook>" asks the Gogs process that started Mercurial`,
	Action:      runHGHook,
}

var errReadOnly = errors.New("this session has read-only access to the repository")

// hgHookTimeout is hgbridge.CallbackTimeout; tests shorten it.
var hgHookTimeout = hgbridge.CallbackTimeout

func runHGHook(c *cli.Context) {
	if err := hgHook(context.Background(), c.Args(), os.Environ()); err != nil {
		fmt.Fprintf(os.Stderr, "gogs: write rejected: %v\n", err)
		os.Exit(1)
	}
}

func hgHook(ctx context.Context, args []string, env []string) error {
	if len(args) != 2 {
		return errors.New("usage: " + hgbridge.HookCommand + " deny|check <hook>")
	}
	switch args[0] {
	case "deny":
		return errReadOnly
	case "check":
		return RunHGHook(ctx, args[1], env)
	case "notify":
		if args[1] != "txnclose" {
			return errors.New("unsupported publication notification")
		}
		if err := runHGCallback(ctx, args[1], env, hgbridge.PublicationPath); err != nil {
			fmt.Fprintln(os.Stderr, "gogs: publication notification deferred; reconciliation will retry")
		}
		return nil
	}
	return fmt.Errorf("unknown hook mode %q", args[0])
}

// RunHGHook is the "check" mode: it presents the session capability from env
// to the parent's control endpoint. Missing context, an unreachable or slow
// endpoint, and any answer but 2xx all reject the transaction.
func RunHGHook(ctx context.Context, hook string, env []string) error {
	return runHGCallback(ctx, hook, env, hgbridge.ControlPath)
}

func runHGCallback(ctx context.Context, hook string, env []string, controlPath string) error {
	vars := make(map[string]string, 3)
	for _, kv := range env {
		if k, v, ok := strings.Cut(kv, "="); ok && strings.HasPrefix(k, "GOGS_HG_") {
			vars[k] = v
		}
	}
	capability, addr := vars[hgbridge.EnvCapability], vars[hgbridge.EnvControl]
	repoID, err := strconv.ParseInt(vars[hgbridge.EnvRepoID], 10, 64)
	if capability == "" || addr == "" || err != nil || repoID <= 0 {
		return errors.New("no write session context")
	}
	// Never send the capability anywhere but the parent on loopback.
	host, _, err := net.SplitHostPort(addr)
	if ip := net.ParseIP(host); err != nil || ip == nil || !ip.IsLoopback() {
		return errors.New("session control endpoint is not on loopback")
	}

	body, err := json.Marshal(hgbridge.HookRequest{Hook: hook, RepoID: repoID, OperationID: vars[hgbridge.EnvOperationID]})
	if err != nil {
		return err
	}
	ctx, cancel := context.WithTimeout(ctx, hgHookTimeout)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, "http://"+addr+controlPath, bytes.NewReader(body))
	if err != nil {
		return err
	}
	req.Header.Set("Authorization", "Bearer "+capability)
	req.Header.Set("Content-Type", "application/json")
	client := &http.Client{
		Transport:     &http.Transport{Proxy: nil},
		CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse },
	}
	resp, err := client.Do(req)
	if err != nil {
		return fmt.Errorf("cannot confirm write access: %v", err)
	}
	resp.Body.Close()
	if resp.StatusCode/100 != 2 {
		return fmt.Errorf("write access not confirmed (%s)", resp.Status)
	}
	return nil
}
