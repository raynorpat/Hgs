// Package maintenance owns immutable process-start admission policy.
package maintenance

import (
	"errors"
	"net/http"
	"strconv"
	"strings"
	"unicode"
	"unicode/utf8"

	"github.com/gogits/gogs/modules/statuspage"
)

type Config struct {
	Enabled           bool
	Message           string
	RetryAfterSeconds int
}

var ErrMaintenance = errors.New("service temporarily unavailable for maintenance")

func (c Config) Validate() error {
	if c.RetryAfterSeconds < 60 || c.RetryAfterSeconds > 86400 {
		return errors.New("MAINTENANCE_RETRY_AFTER must be between 60 and 86400 seconds")
	}
	if !utf8.ValidString(c.Message) || utf8.RuneCountInString(c.Message) > 300 {
		return errors.New("MAINTENANCE_MESSAGE must contain at most 300 Unicode characters")
	}
	for _, r := range c.Message {
		if unicode.IsControl(r) {
			return errors.New("MAINTENANCE_MESSAGE must be plain text without control characters")
		}
	}
	return nil
}

type State struct {
	config Config
	subURL string
	pages  *statuspage.Renderer
}

func New(c Config, subURL string, pages *statuspage.Renderer) (*State, error) {
	if err := c.Validate(); err != nil {
		return nil, err
	}
	return &State{c, subURL, pages}, nil
}
func (s *State) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	path := r.URL.Path
	if (r.Method == http.MethodGet || r.Method == http.MethodHead) && r.URL.RawPath == "" && strings.HasPrefix(path, s.subURL+"/") {
		relative := strings.TrimPrefix(path, s.subURL)
		if relative == "/status/help" {
			s.pages.Help(w, r)
			return
		}
		if s.pages.Asset(w, r, relative) {
			return
		}
	}
	relative := strings.TrimPrefix(path, s.subURL)
	if relative == "/api" || strings.HasPrefix(relative, "/api/") || relative == "/hg" || strings.HasPrefix(relative, "/hg/") || strings.Contains(strings.ToLower(r.Header.Get("User-Agent")), "mercurial") || strings.Contains(r.Header.Get("Accept"), "application/mercurial") {
		w.Header().Set("Cache-Control", "no-store")
		w.Header().Set("Retry-After", strconv.Itoa(s.config.RetryAfterSeconds))
		w.Header().Set("Content-Type", "text/plain; charset=utf-8")
		w.WriteHeader(503)
		if r.Method != http.MethodHead {
			w.Write([]byte(ErrMaintenance.Error() + "\n"))
		}
		return
	}
	s.pages.Render(w, r, statuspage.Model{Code: 503, Message: s.config.Message, RetryAfterSeconds: s.config.RetryAfterSeconds})
}
