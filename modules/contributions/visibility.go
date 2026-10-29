// Copyright 2026 The Gogs Authors. All rights reserved.
// Use of this source code is governed by a MIT-style
// license that can be found in the LICENSE file.

package contributions

import (
	"context"
	"errors"

	"github.com/gogits/gogs/models"
)

func (s *Service) queryRead(ctx context.Context, viewer, repo int64) (bool, error) {
	if s.read == nil {
		return false, nil
	}
	allowed, err := s.read(ctx, viewer, repo)
	if errors.Is(err, models.ErrRepoNotExist) || errors.Is(err, models.ErrForkLineage) || errors.Is(err, models.ErrUserNotExist) {
		return false, nil
	}
	return allowed, err
}
