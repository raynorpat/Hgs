package contributions

import "context"

// Reader exposes only the permission-checked index query to HTTP handlers.
type Reader interface {
	Read(context.Context, int64, Scope, Query) (Page, error)
}

// Provider resolves the current initialized index; nil means unavailable.
type Provider interface{ Contributions() Reader }
