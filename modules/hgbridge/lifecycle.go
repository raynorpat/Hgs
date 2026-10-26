// Package-level transport lifecycle injection keeps model dependencies out of bridges.
package hgbridge

import "context"

// Registrar admits and tracks a worker until release. cancel must stop its worker and relay independently of a client disconnect.
type Registrar interface {
	Register(Grant, context.CancelFunc) (release func(), err error)
}
