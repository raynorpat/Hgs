// Copyright 2026 The Gogs Authors. All rights reserved.
// Use of this source code is governed by a MIT-style
// license that can be found in the LICENSE file.

package hgbridge

import (
	"errors"
	"fmt"
	"path/filepath"
	"runtime"
	"strings"

	"github.com/gogits/gogs/modules/hg"
)

// hooks are the veto-capable hooks every session installs. txnclose and
// other post-commit hooks are advisory and never authorize anything.
var hooks = []string{"pretxnopen", "prepushkey", "pretxnclose", "pretxnclose-bookmark", "pretxnclose-phase"}

func isHook(name string) bool {
	for _, h := range hooks {
		if h == name {
			return true
		}
	}
	return false
}

// HookCommand is the application subcommand the hooks run.
const HookCommand = "internal-hg-hook"

// HookConfig returns the hook settings of one Mercurial invocation, keyed
// "hooks.<name>" with the shell command as value; apply each as
// "--config hooks.<name>=<command>". Readers get "deny" hooks that fail
// without any callback; writers get "check" hooks that call the controller.
// Commands contain only the quoted executable and fixed arguments.
func HookConfig(executable string, canWrite bool) (map[string]string, error) {
	if !filepath.IsAbs(executable) {
		return nil, fmt.Errorf("hgbridge: hook executable %q is not an absolute path", executable)
	}
	if err := hg.CheckPath(executable); err != nil {
		return nil, err
	}
	exe, err := quoteFor(runtime.GOOS, executable)
	if err != nil {
		return nil, err
	}
	mode := "deny"
	if canWrite {
		mode = "check"
	}
	cfg := make(map[string]string, len(hooks))
	for _, h := range hooks {
		cfg["hooks."+h] = exe + " " + HookCommand + " " + mode + " " + h
	}
	return cfg, nil
}

// quoteFor quotes exe for the shell Mercurial runs hooks with: cmd.exe on
// Windows, /bin/sh elsewhere.
func quoteFor(goos, exe string) (string, error) {
	if strings.ContainsFunc(exe, func(r rune) bool { return r < 0x20 || r == 0x7f }) {
		return "", errors.New("hgbridge: hook executable path contains control characters")
	}
	if goos != "windows" {
		return "'" + strings.ReplaceAll(exe, "'", `'\''`) + "'", nil
	}
	// cmd.exe expands %VAR% (and !VAR! when delayed expansion is enabled)
	// even inside double quotes, which cannot themselves be escaped there.
	// Mercurial's opt-in hooks.tonative.<name> rewrite (shelltocmdexe) turns
	// '...' into "...", $VAR into %VAR% and ~\ into %USERPROFILE%, and drops
	// the \ of \~ and \$; 8.3 names such as PROGRA~1 are left alone.
	if strings.ContainsAny(exe, `"%!'$`) || strings.Contains(exe, `~\`) || strings.Contains(exe, `~/`) || strings.Contains(exe, `\~`) {
		return "", fmt.Errorf("hgbridge: hook executable path %q cannot be quoted safely for cmd.exe", exe)
	}
	return `"` + exe + `"`, nil
}
