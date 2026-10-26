// Copyright 2026 The Gogs Authors. All rights reserved.
// Use of this source code is governed by a MIT-style
// license that can be found in the LICENSE file.

package ssh

import (
	"errors"
	"strings"
)

// ErrUnsupportedCommand is returned for every remote command other than a
// native Mercurial client's request to serve a repository over stdio.
var ErrUnsupportedCommand = errors.New(`ssh: only "hg -R <owner>/<repository> serve --stdio" is supported`)

// ParseCommand accepts exactly the remote command native Mercurial clients
// send, "<hg> -R <owner>/<repository> serve --stdio", single-spaced, where
// the executable and the path are bare words or quoted for sh in single
// quotes as Mercurial's sshpeer._serverquote does. The executable is
// ignored. Owner and repository are non-empty, not "." or "..", and contain
// only ASCII letters, digits, '-', '_' and '.'. The command is never passed
// to a shell; the caller builds its own argument vector.
func ParseCommand(cmd string) (owner, repository string, err error) {
	words, ok := splitWords(cmd)
	if !ok || len(words) != 5 || words[1] != "-R" || words[3] != "serve" || words[4] != "--stdio" {
		return "", "", ErrUnsupportedCommand
	}
	if _, ok := unquote(words[0]); !ok {
		return "", "", ErrUnsupportedCommand
	}
	var path string
	path, ok = unquote(words[2])
	if !ok {
		return "", "", ErrUnsupportedCommand
	}
	owner, repository, ok = strings.Cut(path, "/")
	if !ok || !validName(owner) || !validName(repository) {
		return "", "", ErrUnsupportedCommand
	}
	return owner, repository, nil
}

// splitWords splits cmd at single spaces outside single quotes.
func splitWords(cmd string) ([]string, bool) {
	var words []string
	for {
		end := strings.IndexByte(cmd, ' ')
		if strings.HasPrefix(cmd, "'") {
			end = strings.IndexByte(cmd[1:], '\'') + 2
			if end == 1 || end < len(cmd) && cmd[end] != ' ' {
				return nil, false
			}
		}
		if end < 0 || end >= len(cmd) {
			return append(words, cmd), true
		}
		words, cmd = append(words, cmd[:end]), cmd[end+1:]
	}
}

// unquote returns a word that is either bare, made only of the characters
// _serverquote leaves unquoted, or one single-quoted string without inner
// quotes. Control characters are never allowed.
func unquote(w string) (string, bool) {
	if len(w) >= 3 && w[0] == '\'' && w[len(w)-1] == '\'' {
		w = w[1 : len(w)-1]
		for i := 0; i < len(w); i++ {
			if w[i] == '\'' || w[i] < 0x20 || w[i] == 0x7f {
				return "", false
			}
		}
		return w, true
	}
	if w == "" {
		return "", false
	}
	for i := 0; i < len(w); i++ {
		if !isAlnum(w[i]) && !strings.ContainsRune("@%_+=:,./-", rune(w[i])) {
			return "", false
		}
	}
	return w, true
}

func validName(s string) bool {
	if s == "" || s == "." || s == ".." {
		return false
	}
	for i := 0; i < len(s); i++ {
		if !isAlnum(s[i]) && s[i] != '-' && s[i] != '_' && s[i] != '.' {
			return false
		}
	}
	return true
}

func isAlnum(c byte) bool {
	return 'a' <= c && c <= 'z' || 'A' <= c && c <= 'Z' || '0' <= c && c <= '9'
}
