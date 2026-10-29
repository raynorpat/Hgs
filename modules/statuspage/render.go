// Package statuspage renders public failure responses without application services.
package statuspage

import (
	"bytes"
	"errors"
	"fmt"
	"html/template"
	"image/png"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"sync/atomic"
	"unicode/utf8"

	"gopkg.in/ini.v1"
)

type Model struct {
	Code                                                      int
	Title, Message, Illustration, HomeURL, HelpURL, RequestID string
	RetryAfterSeconds                                         int
}

type Renderer struct {
	templates *template.Template
	subURL    string
	assets    map[string][]byte
	locales   map[string]map[string]string
}

var current atomic.Pointer[Renderer]

// Configure is called during startup, before any requests are admitted.
func Configure(root, subURL string) *Renderer {
	p := New(root, subURL)
	current.Store(p)
	return p
}

func New(root, subURL string) *Renderer {
	p := &Renderer{subURL: subURL, assets: make(map[string][]byte), locales: make(map[string]map[string]string)}
	p.templates, _ = template.ParseFiles(filepath.Join(root, "templates/status/layout.tmpl"), filepath.Join(root, "templates/status/404.tmpl"), filepath.Join(root, "templates/status/500.tmpl"), filepath.Join(root, "templates/status/503.tmpl"))
	for _, name := range []string{"css/app.css", "css/status.css", "img/hgs-beaker.svg", "img/hgs-maintenance.png", "img/hgs-error.png", "img/hgs-not-found.png"} {
		if b, err := os.ReadFile(filepath.Join(root, "public", filepath.FromSlash(name))); err == nil {
			if strings.HasSuffix(name, ".png") {
				if _, err := png.DecodeConfig(bytes.NewReader(b)); err != nil {
					continue
				}
			}
			p.assets["/"+name] = b
		}
	}
	for _, lang := range []string{"en-US", "de-DE"} {
		if f, err := ini.Load(filepath.Join(root, "conf/locale/locale_"+lang+".ini")); err == nil {
			p.locales[lang] = f.Section("status_page").KeysHash()
		}
	}
	return p
}

var english = map[string]string{
	"404_title": "We couldn't find that page", "404_label": "Page not found", "404_message": "The page may have moved, or you may not have access to it. Check the address or return to the home page.", "404_note": "If you followed a saved link, it may no longer be available.",
	"500_title": "Something went wrong", "500_label": "Internal server error", "500_message": "An unexpected error prevented us from completing your request. You can return to the home page or get help.", "500_note": "If you were submitting a form, check whether it succeeded before trying again.",
	"503_title": "We'll be back soon", "503_label": "Service maintenance", "503_message": "The service is temporarily unavailable while maintenance is in progress.", "503_note": "Thank you for your patience. Check again when you are ready.",
	"home": "Go to home", "help": "Get help", "check": "Check again", "skip": "Skip to content", "service": "Mercurial service", "reference": "Reference ID", "tagline": "A painless, self-hosted Mercurial service.", "legal": "Based on Gogs. Existing copyright and license notices apply.", "adaptation": "Hardhat, laboratory and searching adaptations.",
	"help_title": "A little help", "help_message": "During maintenance, repository and account actions are unavailable. Check again manually later. If an error continues, contact your instance administrator. If a page is missing, confirm the address and your access with the repository owner.",
	"language": "Language",
}

func (p *Renderer) language(r *http.Request) string {
	lang := r.URL.Query().Get("lang")
	if lang == "" {
		if c, e := r.Cookie("lang"); e == nil {
			lang = c.Value
		}
	}
	if lang == "" {
		lang = strings.Split(r.Header.Get("Accept-Language"), ",")[0]
	}
	if strings.HasPrefix(strings.ToLower(lang), "de") {
		return "de-DE"
	}
	return "en-US"
}
func (p *Renderer) text(lang, key string) string {
	if s := p.locales[lang][key]; s != "" {
		return s
	}
	return english[key]
}
func Render(w http.ResponseWriter, r *http.Request, m Model) error {
	p := current.Load()
	if p == nil {
		p = &Renderer{}
	}
	return p.Render(w, r, m)
}

var opaqueID = regexp.MustCompile(`^[A-Za-z0-9_-]{8,64}$`)

// boundedBuffer prevents a damaged local template from allocating an unbounded response.
type boundedBuffer struct{ bytes.Buffer }

func (b *boundedBuffer) Write(p []byte) (int, error) {
	if b.Len()+len(p) > 65536 {
		return 0, errors.New("status page too large")
	}
	return b.Buffer.Write(p)
}

func (p *Renderer) Render(w http.ResponseWriter, r *http.Request, m Model) error {
	if m.Code != 404 && m.Code != 500 && m.Code != 503 {
		m.Code = 500
	}
	return p.render(w, r, m, false)
}
func (p *Renderer) Help(w http.ResponseWriter, r *http.Request) {
	p.render(w, r, Model{Code: 200}, true)
}
func (p *Renderer) render(w http.ResponseWriter, r *http.Request, m Model, help bool) error {
	requestPath := strings.TrimPrefix(r.URL.Path, p.subURL)
	plain := !help && (requestPath == "/api" || strings.HasPrefix(requestPath, "/api/") || requestPath == "/hg" || strings.HasPrefix(requestPath, "/hg/") || strings.Contains(strings.ToLower(r.Header.Get("User-Agent")), "mercurial") || strings.Contains(r.Header.Get("Accept"), "application/mercurial"))
	lang := p.language(r)
	key := strconv.Itoa(m.Code)
	m.Title = p.text(lang, key+"_title")
	message := p.text(lang, key+"_message")
	if help {
		m.Title = p.text(lang, "help_title")
		message = p.text(lang, "help_message")
	}
	if m.Code != 503 || !utf8.ValidString(m.Message) || utf8.RuneCountInString(m.Message) > 300 {
		m.Message = ""
	}
	m.HomeURL = p.subURL + "/?lang=" + lang
	m.HelpURL = p.subURL + "/status/help?lang=" + lang
	if !opaqueID.MatchString(m.RequestID) {
		m.RequestID = ""
	}
	m.Illustration = map[int]string{404: "/img/hgs-not-found.png", 500: "/img/hgs-error.png", 503: "/img/hgs-maintenance.png"}[m.Code]
	data := struct {
		Model
		Lang, SubURL, Description, Label, Note, Primary string
		Text                                            map[string]string
		Help                                            bool
	}{Model: m, Lang: lang, SubURL: p.subURL, Description: message, Label: p.text(lang, key+"_label"), Note: p.text(lang, key+"_note"), Primary: p.text(lang, "home"), Text: map[string]string{}, Help: help}
	for k := range english {
		data.Text[k] = p.text(lang, k)
	}
	if m.Code == 503 {
		data.Primary = p.text(lang, "check")
	}
	var b boundedBuffer
	var err error
	if plain {
		// Transport failures never depend on HTML resources.
	} else if p.templates == nil || len(p.assets["/css/status.css"]) == 0 || len(p.assets["/css/app.css"]) == 0 || len(p.assets["/img/hgs-beaker.svg"]) == 0 || (!help && len(p.assets[m.Illustration]) == 0) {
		err = errors.New("status page resources unavailable")
	} else {
		err = p.templates.ExecuteTemplate(&b, "status-layout", data)
	}
	h := w.Header()
	h.Del("Content-Length")
	h.Del("Location")
	for _, name := range []string{"Content-Disposition", "Content-Range", "Accept-Ranges", "ETag", "Last-Modified"} {
		h.Del(name)
	}
	h.Set("Cache-Control", "no-store")
	h.Set("X-Content-Type-Options", "nosniff")
	h.Del("Retry-After")
	if m.Code == 503 {
		retry := m.RetryAfterSeconds
		if retry < 60 || retry > 86400 {
			retry = 300
		}
		h.Set("Retry-After", strconv.Itoa(retry))
	}
	if err != nil || plain {
		h.Set("Content-Type", "text/plain; charset=utf-8")
	} else {
		h.Set("Content-Type", "text/html; charset=utf-8")
	}
	w.WriteHeader(m.Code)
	if r.Method == http.MethodHead {
		return err
	}
	if err != nil || plain {
		_, writeErr := fmt.Fprintf(w, "%d %s\n%s\n", m.Code, m.Title, message)
		if writeErr != nil {
			return writeErr
		}
		return err
	}
	_, err = w.Write(b.Bytes())
	return err
}

// Asset serves only the exact preloaded public status resources, never a directory.
func (p *Renderer) Asset(w http.ResponseWriter, r *http.Request, name string) bool {
	b, ok := p.assets[name]
	if !ok {
		return false
	}
	contentType := "image/png"
	if strings.HasSuffix(name, ".css") {
		contentType = "text/css; charset=utf-8"
	}
	if strings.HasSuffix(name, ".svg") {
		contentType = "image/svg+xml"
	}
	w.Header().Set("Content-Type", contentType)
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("X-Content-Type-Options", "nosniff")
	w.WriteHeader(200)
	if r.Method != http.MethodHead {
		w.Write(b)
	}
	return true
}
