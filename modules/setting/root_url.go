package setting

import (
	"fmt"
	"net/url"
	"path"
	"strings"
)

// NormalizeRootURL is shared by startup and installer before either mutates
// persistent state. Application paths are canonical without an alternate RawPath.
func NormalizeRootURL(raw string) (normalized, subURL string, err error) {
	u, err := url.Parse(raw)
	if err != nil {
		return "", "", fmt.Errorf("invalid ROOT_URL: %w", err)
	}
	subURL = strings.TrimSuffix(u.Path, "/")
	if u.Host == "" || (u.Scheme != "http" && u.Scheme != "https") || u.User != nil || u.RawQuery != "" || u.ForceQuery || u.Fragment != "" || u.RawPath != "" || strings.ContainsAny(subURL, "%\\?#") || (subURL != "" && (path.Clean(subURL) != subURL || strings.HasSuffix(subURL, "/"))) {
		return "", "", fmt.Errorf("invalid ROOT_URL: expected an HTTP(S) origin and canonical application path")
	}
	u.Path = subURL + "/"
	return u.String(), subURL, nil
}
