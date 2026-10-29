// Copyright 2014 The Gogs Authors. All rights reserved.
// Use of this source code is governed by a MIT-style
// license that can be found in the LICENSE file.

// Package avatar supplies provider-aware image links and a bounded local cache.
package avatar

import (
	"bytes"
	"context"
	"crypto/md5"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"image"
	"image/jpeg"
	_ "image/png"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"
	"unicode"

	"github.com/gogits/gogs/modules/setting"
	"github.com/nfnt/resize"
)

const maxImageBytes = 4 << 20
const maxImageSide = 2048

// HashEmail is the official Gravatar SHA-256 identifier.
func HashEmail(email string) string {
	sum := sha256.Sum256([]byte(strings.ToLower(strings.TrimSpace(email))))
	return hex.EncodeToString(sum[:])
}

// ProviderHash keeps the explicitly supported legacy provider's MD5 identity.
// Callers recompute from the email rather than reinterpret persisted old hashes.
func ProviderHash(email string) string {
	source, err := setting.ResolveGravatarSource(setting.GravatarSource)
	if err != nil || strings.TrimSpace(email) == "" {
		return ""
	}
	if source == setting.LegacyGravatarSource {
		sum := md5.Sum([]byte(strings.ToLower(strings.TrimSpace(email))))
		return hex.EncodeToString(sum[:])
	}
	return HashEmail(email)
}

func validHash(hash string) bool {
	source, err := setting.ResolveGravatarSource(setting.GravatarSource)
	if err != nil {
		return false
	}
	length := 64
	if source == setting.LegacyGravatarSource {
		length = 32
	}
	if len(hash) != length || strings.ToLower(hash) != hash {
		return false
	}
	_, err = hex.DecodeString(hash)
	return err == nil
}

func FallbackURL() string { return setting.AppSubUrl + "/img/avatar_default.jpg" }

func Link(email string) string {
	if setting.DisableGravatar {
		return FallbackURL()
	}
	hash := ProviderHash(email)
	if hash == "" {
		return FallbackURL()
	}
	if setting.Service.EnableCacheAvatar {
		return setting.AppSubUrl + "/avatar/" + hash
	}
	source, _ := setting.ResolveGravatarSource(setting.GravatarSource)
	return source + hash
}

// Presentation is the complete shared template contract. Size queries are built
// here, so every caller uses one validated size and never edits a URL in markup.
type Presentation struct {
	URL, Fallback, Name, Initials string
	Size                          int
}

func Present(rawURL, name string, size int) Presentation {
	if size < 1 || size > maxImageSide {
		size = 28
	}
	p := Presentation{URL: rawURL, Fallback: FallbackURL(), Name: name, Size: size, Initials: "?"}
	for _, r := range name {
		if unicode.IsLetter(r) || unicode.IsDigit(r) {
			p.Initials = string(unicode.ToUpper(r))
			break
		}
	}
	u, err := url.Parse(rawURL)
	if err == nil {
		source, _ := setting.ResolveGravatarSource(setting.GravatarSource)
		if strings.HasPrefix(rawURL, setting.AppSubUrl+"/avatar/") || source != "" && strings.HasPrefix(rawURL, source) {
			q := u.Query()
			q.Del("s")
			q.Set("size", strconv.Itoa(size))
			u.RawQuery = q.Encode()
			p.URL = u.String()
		}
	}
	return p
}

// Avatar retains the existing ten-minute cache lifetime and 200px source image.
type Avatar struct {
	Hash           string
	AlterImage     string
	imagePath      string
	expireDuration time.Duration
}

func New(hash, cacheDir string) *Avatar {
	a := &Avatar{Hash: hash, expireDuration: 10 * time.Minute}
	if validHash(hash) {
		a.imagePath = filepath.Join(cacheDir, hash+".image")
	}
	return a
}
func (a *Avatar) HasCache() bool {
	info, err := os.Stat(a.imagePath)
	return err == nil && info.Mode().IsRegular()
}
func (a *Avatar) Modtime() (time.Time, error) {
	info, err := os.Stat(a.imagePath)
	if err != nil {
		return time.Time{}, err
	}
	return info.ModTime(), nil
}
func (a *Avatar) Expired() bool {
	mtime, err := a.Modtime()
	return err != nil || time.Since(mtime) > a.expireDuration
}

func decodeImage(r io.Reader) (image.Image, error) {
	data, err := io.ReadAll(io.LimitReader(r, maxImageBytes+1))
	if err != nil {
		return nil, err
	}
	if len(data) > maxImageBytes {
		return nil, errors.New("avatar exceeds byte limit")
	}
	cfg, format, err := image.DecodeConfig(bytes.NewReader(data))
	if err != nil {
		return nil, err
	}
	if format != "jpeg" && format != "png" || cfg.Width < 1 || cfg.Height < 1 || cfg.Width > maxImageSide || cfg.Height > maxImageSide || cfg.Width*cfg.Height > 4<<20 {
		return nil, errors.New("invalid avatar dimensions or format")
	}
	img, _, err := image.Decode(bytes.NewReader(data))
	return img, err
}
func decodeFile(path string) (image.Image, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	return decodeImage(f)
}
func (a *Avatar) Encode(w io.Writer, size int) error {
	if size < 1 || size > maxImageSide {
		return errors.New("invalid avatar size")
	}
	img, err := decodeFile(a.imagePath)
	if err != nil {
		img, err = decodeFile(a.AlterImage)
	}
	if err != nil {
		return err
	}
	return jpeg.Encode(w, resize.Resize(uint(size), uint(size), img, resize.NearestNeighbor), nil)
}

var client = &http.Client{}

func (a *Avatar) update(ctx context.Context) error {
	if setting.DisableGravatar || !validHash(a.Hash) || a.imagePath == "" {
		return errors.New("remote avatar unavailable")
	}
	source, err := setting.ResolveGravatarSource(setting.GravatarSource)
	if err != nil {
		return err
	}
	u, _ := url.Parse(source + a.Hash)
	u.RawQuery = url.Values{"d": {"retro"}, "r": {"pg"}, "size": {"200"}}.Encode()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u.String(), nil)
	if err != nil {
		return err
	}
	req.Header.Set("Accept", "image/jpeg,image/png")
	// Never follow redirects, including same-host redirects: configured identity
	// and HTTPS are authoritative, and failure uses the local fallback.
	scoped := *client
	scoped.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }
	resp, err := scoped.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("avatar response status %d", resp.StatusCode)
	}
	data, err := io.ReadAll(io.LimitReader(resp.Body, maxImageBytes+1))
	if err != nil {
		return err
	}
	if len(data) > maxImageBytes {
		return errors.New("avatar exceeds byte limit")
	}
	if _, err = decodeImage(bytes.NewReader(data)); err != nil {
		return err
	}
	if err = ctx.Err(); err != nil {
		return err
	}
	if err = os.MkdirAll(filepath.Dir(a.imagePath), 0755); err != nil {
		return err
	}
	f, err := os.CreateTemp(filepath.Dir(a.imagePath), "avatar-*.part")
	if err != nil {
		return err
	}
	tmp := f.Name()
	defer os.Remove(tmp)
	_, writeErr := f.Write(data)
	closeErr := f.Close()
	if writeErr != nil {
		return writeErr
	}
	if closeErr != nil {
		return closeErr
	}
	if err = ctx.Err(); err != nil {
		return err
	}
	if err = os.Rename(tmp, a.imagePath); err != nil {
		// Windows may deny a simultaneous replacement after another request
		// publishes this key. Keep that winner only when it is fresh and valid.
		if !a.Expired() {
			if _, decodeErr := decodeFile(a.imagePath); decodeErr == nil {
				return nil
			}
		}
		return err
	}
	return nil
}
func (a *Avatar) Update() { _ = a.UpdateTimeout(time.Second) }
func (a *Avatar) UpdateTimeout(timeout time.Duration) error {
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()
	return a.update(ctx)
}

type service struct{ cacheDir, altImage string }

func (s *service) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	size := 80
	for _, key := range []string{"s", "size"} {
		if values, ok := r.URL.Query()[key]; ok {
			if len(values) != 1 {
				http.Error(w, "invalid avatar size", 400)
				return
			}
			v, err := strconv.Atoi(values[0])
			if err != nil || v < 1 || v > maxImageSide {
				http.Error(w, "invalid avatar size", 400)
				return
			}
			size = v
		}
	}
	hash := r.URL.Path[strings.LastIndex(r.URL.Path, "/")+1:]
	_, sourceErr := setting.ResolveGravatarSource(setting.GravatarSource)
	if sourceErr == nil && !validHash(hash) {
		http.Error(w, "invalid avatar identifier", 400)
		return
	}
	a := New(hash, s.cacheDir)
	a.AlterImage = s.altImage
	useCache := !setting.DisableGravatar && sourceErr == nil
	if useCache && a.Expired() {
		ctx, cancel := context.WithTimeout(r.Context(), time.Second)
		err := a.update(ctx)
		cancel()
		if err != nil {
			useCache = false
		}
	}
	if !useCache {
		a.imagePath = ""
	}
	var body bytes.Buffer
	if err := a.Encode(&body, size); err != nil {
		http.Error(w, "avatar unavailable", http.StatusNotFound)
		return
	}
	w.Header().Set("Content-Type", "image/jpeg")
	// Failures and disabled configuration must not be retained as remote cache hits.
	w.Header().Set("Cache-Control", "no-cache")
	w.Write(body.Bytes())
}
func CacheServer(cacheDir, defaultImgPath string) http.Handler {
	return &service{cacheDir, defaultImgPath}
}
