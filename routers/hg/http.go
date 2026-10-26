package hg

import (
	"net/http"
	"net/url"
	"strings"

	"github.com/gogits/gogs/modules/hosting"
)

// Handler is the raw transport entry point, before browser middleware.
func Handler(service *hosting.Service) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		owner, repo, ok := transportPath(r.URL.EscapedPath(), service.AppSubURL())
		if !ok {
			http.NotFound(w, r)
			return
		}
		copy := r.Clone(r.Context())
		u := *r.URL
		copy.URL = &u
		u.Path = "/" + owner + "/" + repo
		u.RawPath = ""
		service.HTTP().ServeHTTP(w, copy)
	})
}

func transportPath(escaped, sub string) (owner, repo string, ok bool) {
	escapedSub := (&url.URL{Path: sub}).EscapedPath()
	// An already stripped transport root has exactly these components.
	rootParts := strings.Split(strings.TrimSuffix(escaped, "/"), "/")
	alreadyStripped := len(rootParts) == 4 && rootParts[0] == "" && rootParts[1] == "hg"
	if !alreadyStripped && sub != "" && strings.HasPrefix(escaped, escapedSub+"/") {
		escaped = strings.TrimPrefix(escaped, escapedSub)
	}
	parts := strings.Split(escaped, "/")
	if len(parts) == 5 && parts[4] == "" {
		parts = parts[:4]
	}
	if len(parts) != 4 || parts[0] != "" || parts[1] != "hg" {
		return "", "", false
	}
	names := make([]string, 2)
	for i, p := range parts[2:] {
		v, err := url.PathUnescape(p)
		if err != nil || v == "" || v == "." || v == ".." || strings.ContainsAny(v, "/\\\x00\r\n") {
			return "", "", false
		}
		names[i] = v
	}
	return names[0], names[1], true
}
