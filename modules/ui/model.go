// Package ui composes shared application navigation from existing request authority.
package ui

type ContextKind string

const (
	Workspace  ContextKind = "workspace"
	Repository ContextKind = "repository"
	Admin      ContextKind = "admin"
	Anonymous  ContextKind = "anonymous"
	// Capability URLs are set only by server composition after handlers exist.
	// Tasks 4, 13, 17 and 7 own discovery, pull requests, help and sign-out respectively.
	SearchURL       = "UISearchURL"
	RecentsURL      = "UIRecentsURL"
	PullRequestsURL = "UIPullRequestsURL"
	HelpURL         = "UIHelpURL"
	SignOutURL      = "UISignOutURL" // Requires a CSRF-protected POST handler.
)

type NavItem struct {
	Key, Label, URL, Icon string
	Active                bool
	Count                 *int64
}
type Shell struct {
	Kind                                           ContextKind
	Title, SubURL, AvatarURL, AvatarName, Initials string
	SignedIn, CanAdmin                             bool
	Navigation                                     []NavItem
}

// Field supplies associated labels, hints and errors to shared/form_field.
// Values must be non-secret; password fields deliberately never render a value.
type Field struct {
	ID, Name, Label, Type, Value, Hint, Error string
	Required                                  bool
}
type PageLink struct {
	Label, URL        string
	Current, Disabled bool
}

// MarkdownEditor is the shared sanitized preview contract for one editor instance.
type MarkdownEditor struct{ ID, Value, Context, Label, Write, Preview, Error, URL string }
