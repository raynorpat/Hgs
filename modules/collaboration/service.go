// Package collaboration composes local repository collaboration operations.
package collaboration

import "github.com/gogits/gogs/modules/hosting"

// Resolve the current cmd-owned host per operation, including after installation.
type Service struct{ Provider hosting.Provider }

func New(provider hosting.Provider) *Service { return &Service{Provider: provider} }
