// Copyright 2026 The Gogs Authors. All rights reserved.
// Use of this source code is governed by a MIT-style
// license that can be found in the LICENSE file.

package contributions

import (
	"github.com/gogits/gogs/models"
	"net/mail"
	"strings"
)

func authorEmail(author string) string {
	address, err := mail.ParseAddress(strings.TrimSpace(author))
	if err != nil || !strings.Contains(address.Address, "@") {
		return ""
	}
	return models.NormalizeContributionEmail(address.Address)
}
