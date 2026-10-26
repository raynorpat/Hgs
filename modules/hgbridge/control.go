// Copyright 2026 The Gogs Authors. All rights reserved.
// Use of this source code is governed by a MIT-style
// license that can be found in the LICENSE file.

package hgbridge

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"
	"time"
)

// The session environment of a write-capable Mercurial process. Hook
// processes inherit it from Mercurial.
const (
	EnvCapability = "GOGS_HG_CAPABILITY"
	EnvControl    = "GOGS_HG_CONTROL"
	EnvRepoID     = "GOGS_HG_REPO_ID"
)

const (
	// ControlPath is the only path the control endpoint serves. A hook
	// POSTs a HookRequest there with "Authorization: Bearer <capability>";
	// 204 allows the transaction, anything else denies it.
	ControlPath = "/hook"
	// CallbackTimeout bounds one hook callback on both ends.
	CallbackTimeout = 5 * time.Second

	maxControlBody = 4 << 10
)

// HookRequest is the whole callback body. The actor and rights come from the
// stored session, never from the request.
type HookRequest struct {
	Hook   string `json:"hook"`
	RepoID int64  `json:"repo_id"`
}

func (c *Controller) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if r.URL.Path != ControlPath {
		http.NotFound(w, r)
		return
	}
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	capability, ok := strings.CutPrefix(r.Header.Get("Authorization"), "Bearer ")
	if !ok {
		http.Error(w, "write not authorized", http.StatusForbidden)
		return
	}
	var req HookRequest
	dec := json.NewDecoder(http.MaxBytesReader(w, r.Body, maxControlBody))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&req); err != nil {
		http.Error(w, "malformed hook request", http.StatusBadRequest)
		return
	}

	ctx, cancel := context.WithTimeout(r.Context(), CallbackTimeout)
	defer cancel()
	if c.Check(ctx, capability, req.RepoID, req.Hook) != nil {
		http.Error(w, "write not authorized", http.StatusForbidden)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}
