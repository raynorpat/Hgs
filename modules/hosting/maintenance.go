// Copyright 2026 The Gogs Authors. All rights reserved.
// Use of this source code is governed by a MIT-style
// license that can be found in the LICENSE file.

package hosting

import (
	"context"
	"github.com/gogits/gogs/models"
	"github.com/gogits/gogs/modules/hg"
	"github.com/gogits/gogs/modules/hgbridge"
	"github.com/gogits/gogs/modules/log"
)

// WithMaintenanceRead is actorless, read-only, and retains lifecycle admission
// until the synchronous callback and all its managed native children have joined.
func (s *Service) WithMaintenanceRead(ctx context.Context, repoID int64, read func(context.Context, string) error) error {
	if repoID <= 0 || read == nil {
		return hgbridge.ErrDenied
	}
	repo, err := models.GetRepositoryById(repoID)
	if err != nil {
		return err
	}
	if repo.State != models.RepositoryReady {
		return hgbridge.ErrDenied
	}
	limit := s.cfg.Mercurial.SessionTimeout
	if limit <= 0 {
		limit = hg.DefaultSessionTimeout
	}
	ctx, cancel := context.WithTimeout(ctx, limit)
	defer cancel()
	valid := func() bool {
		current, err := models.GetRepositoryById(repoID)
		return err == nil && current.State == models.RepositoryReady && current.StorageNonce == repo.StorageNonce
	}
	release, err := s.sessions.register(hgbridge.Grant{RepoID: repoID}, cancel, valid)
	if err != nil {
		return err
	}
	defer release()
	if !valid() {
		return hgbridge.ErrDenied
	}
	path, err := models.ReadyRepositoryStorage(s.RepositoryRoot(), repoID)
	if err != nil {
		return err
	}
	if err = ctx.Err(); err != nil {
		return err
	}
	return read(ctx, path)
}

func (s *Service) notifyRepositoryPublished(id int64) {
	if s.cfg.RepositoryPublished == nil {
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), hgbridge.CallbackTimeout)
	defer cancel()
	if err := s.cfg.RepositoryPublished(ctx, id); err != nil {
		log.Warn("contribution publication notification deferred for repository %d", id)
	}
}
