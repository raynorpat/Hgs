// Copyright 2014 The Gogs Authors. All rights reserved.
// Use of this source code is governed by a MIT-style
// license that can be found in the LICENSE file.
package avatar

import (
	"bytes"
	"context"
	"crypto/sha256"
	"fmt"
	"image"
	"image/color"
	"image/jpeg"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/gogits/gogs/modules/setting"
)

type providerTransport struct {
	base   http.RoundTripper
	target *url.URL
}

func (p providerTransport) RoundTrip(r *http.Request) (*http.Response, error) {
	if r.URL.Scheme != "https" || (r.URL.Host != "gravatar.com" && r.URL.Host != "gravatar.duoshuo.com") {
		return nil, fmt.Errorf("unexpected provider %s", r.URL)
	}
	copy := r.Clone(r.Context())
	u := *r.URL
	copy.URL = &u
	copy.URL.Host = p.target.Host
	return p.base.RoundTrip(copy)
}
func fakeProvider(t *testing.T, handler http.HandlerFunc) {
	t.Helper()
	server := httptest.NewTLSServer(handler)
	t.Cleanup(server.Close)
	target, _ := url.Parse(server.URL)
	oldClient, oldSource, oldDisabled := client, setting.GravatarSource, setting.DisableGravatar
	client = &http.Client{Transport: providerTransport{server.Client().Transport, target}}
	setting.GravatarSource = "https://gravatar.com/avatar/"
	setting.DisableGravatar = false
	t.Cleanup(func() { client, setting.GravatarSource, setting.DisableGravatar = oldClient, oldSource, oldDisabled })
}
func testImage(t *testing.T) []byte {
	t.Helper()
	img := image.NewRGBA(image.Rect(0, 0, 12, 12))
	img.Set(0, 0, color.RGBA{255, 0, 0, 255})
	var b bytes.Buffer
	if err := jpeg.Encode(&b, img, nil); err != nil {
		t.Fatal(err)
	}
	return b.Bytes()
}
func TestGravatarNormalizationAndCache(t *testing.T) {
	email := " Person@Example.Invalid "
	want := fmt.Sprintf("%x", sha256.Sum256([]byte("person@example.invalid")))
	if got := HashEmail(email); got != want {
		t.Errorf("official hash = %s, want SHA256 %s", got, want)
	}
	var requests atomic.Int32
	data := testImage(t)
	fakeProvider(t, func(w http.ResponseWriter, r *http.Request) {
		requests.Add(1)
		if r.URL.Path != "/avatar/"+want || r.URL.Query().Get("size") != "200" {
			t.Errorf("wrong provider request %s", r.URL)
		}
		w.Write(data)
	})
	a := New(want, t.TempDir())
	if err := a.UpdateTimeout(time.Second); err != nil {
		t.Fatalf("cache fetch: %v", err)
	}
	if requests.Load() != 1 || !a.HasCache() {
		t.Fatal("valid image was not cached")
	}
	var b bytes.Buffer
	if err := a.Encode(&b, 28); err != nil {
		t.Fatal(err)
	}
	cfg, _, err := image.DecodeConfig(&b)
	if err != nil || cfg.Width != 28 || cfg.Height != 28 {
		t.Fatalf("resized image %#v %v", cfg, err)
	}
}
func TestAvatarFailureFallback(t *testing.T) {
	for _, mode := range []string{"invalid image", "timeout", "disabled", "corrupt cache", "invalid hash", "invalid size", "redirect"} {
		t.Run(mode, func(t *testing.T) {
			var calls atomic.Int32
			var cancelled atomic.Bool
			data := testImage(t)
			fakeProvider(t, func(w http.ResponseWriter, r *http.Request) {
				calls.Add(1)
				switch mode {
				case "timeout":
					select {
					case <-r.Context().Done():
						cancelled.Store(true)
					case <-time.After(2 * time.Second):
					}
				case "redirect":
					http.Redirect(w, r, "http://gravatar.com/avatar/other", 302)
				default:
					w.Write([]byte("not an image"))
				}
			})
			dir := t.TempDir()
			fallback := filepath.Join(dir, "default.jpg")
			if err := os.WriteFile(fallback, data, 0600); err != nil {
				t.Fatal(err)
			}
			hash := fmt.Sprintf("%x", sha256.Sum256([]byte("fixture@example.invalid")))
			if mode == "disabled" {
				setting.DisableGravatar = true
			}
			if mode == "corrupt cache" {
				os.WriteFile(filepath.Join(dir, hash+".image"), []byte("broken"), 0600)
			}
			if mode == "invalid hash" {
				hash = "not-a-hash"
			}
			size := "28"
			if mode == "invalid size" {
				size = "2049"
			}
			req := httptest.NewRequest("GET", "/avatar/"+hash+"?size="+size, nil)
			if mode == "timeout" {
				ctx, cancel := context.WithTimeout(req.Context(), 30*time.Millisecond)
				defer cancel()
				req = req.WithContext(ctx)
			}
			w := httptest.NewRecorder()
			CacheServer(dir, fallback).ServeHTTP(w, req)
			if mode == "invalid hash" || mode == "invalid size" {
				if w.Code != 400 || calls.Load() != 0 {
					t.Fatalf("invalid request: status %d, fetches %d", w.Code, calls.Load())
				}
				return
			}
			cfg, _, err := image.DecodeConfig(bytes.NewReader(w.Body.Bytes()))
			if w.Code != 200 || err != nil || cfg.Width != 28 || cfg.Height != 28 {
				t.Errorf("fallback status=%d cfg=%#v error=%v", w.Code, cfg, err)
			}
			if mode == "disabled" && calls.Load() != 0 {
				t.Error("disabled avatar fetched remotely")
			}
			if mode == "timeout" {
				deadline := time.Now().Add(time.Second)
				for !cancelled.Load() && time.Now().Before(deadline) {
					time.Sleep(time.Millisecond)
				}
				if !cancelled.Load() {
					t.Error("request cancellation did not reach provider")
				}
			}
			files, _ := os.ReadDir(dir)
			for _, f := range files {
				if strings.Contains(f.Name(), ".part") {
					t.Error("stranded temporary image")
				}
			}
		})
	}
}

func TestGravatarProviderAndPresentation(t *testing.T) {
	oldSource, oldSub, oldCache, oldDisabled := setting.GravatarSource, setting.AppSubUrl, setting.Service.EnableCacheAvatar, setting.DisableGravatar
	t.Cleanup(func() {
		setting.GravatarSource, setting.AppSubUrl, setting.Service.EnableCacheAvatar, setting.DisableGravatar = oldSource, oldSub, oldCache, oldDisabled
	})
	setting.AppSubUrl = "/code"
	setting.DisableGravatar = false
	setting.Service.EnableCacheAvatar = false
	for _, tc := range []struct{ source, key string }{
		{setting.OfficialGravatarSource, "9dc98d058b1d6f4688703fdded7387b1851cddf5e946b37e324c029fc811905f"},
		{setting.LegacyGravatarSource, "2da3d935e5683aa6ba2f6858d90d78a0"},
	} {
		setting.GravatarSource = tc.source
		if got := ProviderHash(" Person@Example.Invalid "); got != tc.key {
			t.Errorf("provider hash %s", got)
		}
		p := Present(Link(" Person@Example.Invalid "), "Person", 160)
		if p.URL != tc.source+tc.key+"?size=160" || p.Fallback != "/code/img/avatar_default.jpg" || p.Name != "Person" || p.Size != 160 {
			t.Errorf("presentation %+v", p)
		}
		if !validHash(tc.key) {
			t.Error("provider rejects own key")
		}
		wrong := strings.Repeat("a", 32)
		if len(tc.key) == 32 {
			wrong = strings.Repeat("a", 64)
		}
		if validHash(wrong) {
			t.Error("provider accepted another hash format")
		}
	}
	p := Present("/code/avatar/"+strings.Repeat("a", 32)+"?s=80&size=200", "Person", 28)
	if strings.Contains(p.URL, "s=") || strings.Count(p.URL, "size=") != 1 || !strings.HasSuffix(p.URL, "size=28") {
		t.Errorf("size query %s", p.URL)
	}
}

func TestAvatarFailureBounds(t *testing.T) {
	for _, mode := range []string{"bytes", "dimensions", "truncated image", "http redirect", "other host redirect", "same host redirect", "unknown source"} {
		t.Run(mode, func(t *testing.T) {
			data := testImage(t)
			bad := []byte("corrupt")
			switch mode {
			case "bytes":
				bad = bytes.Repeat([]byte("x"), (4<<20)+1)
			case "dimensions":
				var b bytes.Buffer
				jpeg.Encode(&b, image.NewRGBA(image.Rect(0, 0, 2049, 2)), nil)
				bad = b.Bytes()
			case "truncated image":
				bad = data[:len(data)-10]
			}
			var calls atomic.Int32
			fakeProvider(t, func(w http.ResponseWriter, r *http.Request) {
				calls.Add(1)
				switch mode {
				case "http redirect":
					http.Redirect(w, r, "http://gravatar.com/avatar/other", 302)
				case "other host redirect":
					http.Redirect(w, r, "https://elsewhere.invalid/avatar/other", 302)
				case "same host redirect":
					http.Redirect(w, r, "https://gravatar.com/avatar/other", 302)
				default:
					w.Write(bad)
				}
			})
			if mode == "unknown source" {
				setting.GravatarSource = "https://elsewhere.invalid/avatar/"
			}
			dir := t.TempDir()
			fallback := filepath.Join(dir, "fallback.jpg")
			os.WriteFile(fallback, data, 0600)
			w := httptest.NewRecorder()
			CacheServer(dir, fallback).ServeHTTP(w, httptest.NewRequest("GET", "/avatar/"+strings.Repeat("a", 64)+"?s=40", nil))
			cfg, _, err := image.DecodeConfig(bytes.NewReader(w.Body.Bytes()))
			if w.Code != 200 || err != nil || cfg.Width != 40 {
				t.Fatalf("fallback status %d config %+v error %v", w.Code, cfg, err)
			}
			entries, _ := os.ReadDir(dir)
			if len(entries) != 1 {
				t.Errorf("invalid image left cache files %v", entries)
			}
			want := int32(1)
			if mode == "unknown source" {
				want = 0
			}
			if calls.Load() != want {
				t.Errorf("provider calls %d want %d", calls.Load(), want)
			}
		})
	}
}
func TestGravatarCacheValidationAndReuse(t *testing.T) {
	data := testImage(t)
	var calls atomic.Int32
	fakeProvider(t, func(w http.ResponseWriter, r *http.Request) { calls.Add(1); w.Write(data) })
	dir := t.TempDir()
	fallback := filepath.Join(dir, "default.jpg")
	os.WriteFile(fallback, data, 0600)
	handler := CacheServer(dir, fallback)
	hash := strings.Repeat("a", 64)
	for _, suffix := range []string{"not-hex?size=28", strings.Repeat("a", 32) + "?size=28", strings.Repeat("A", 64) + "?size=28", hash + "?size=0", hash + "?size=-1", hash + "?size=28junk", hash + "?size=28&size=40", hash + "?size=2049"} {
		w := httptest.NewRecorder()
		handler.ServeHTTP(w, httptest.NewRequest("GET", "/avatar/"+suffix, nil))
		if w.Code != 400 {
			t.Errorf("invalid %q status %d", suffix, w.Code)
		}
	}
	if calls.Load() != 0 {
		t.Fatal("invalid input caused provider request")
	}
	for _, size := range []int{28, 160} {
		w := httptest.NewRecorder()
		handler.ServeHTTP(w, httptest.NewRequest("GET", fmt.Sprintf("/avatar/%s?size=%d", hash, size), nil))
		cfg, _, err := image.DecodeConfig(bytes.NewReader(w.Body.Bytes()))
		if err != nil || cfg.Width != size || cfg.Height != size {
			t.Errorf("cache image %#v %v", cfg, err)
		}
	}
	if calls.Load() != 1 {
		t.Errorf("cache reuse fetched %d times", calls.Load())
	}
}

func TestGravatarLegacyCache(t *testing.T) {
	data := testImage(t)
	var calls atomic.Int32
	fakeProvider(t, func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		if r.Host != "gravatar.duoshuo.com" || r.URL.Path != "/avatar/2da3d935e5683aa6ba2f6858d90d78a0" {
			t.Errorf("changed legacy provider %s %s", r.Host, r.URL.Path)
		}
		w.Write(data)
	})
	setting.GravatarSource = setting.LegacyGravatarSource
	a := New("2da3d935e5683aa6ba2f6858d90d78a0", t.TempDir())
	if err := a.UpdateTimeout(time.Second); err != nil {
		t.Fatal(err)
	}
	if calls.Load() != 1 || !a.HasCache() {
		t.Fatal("legacy image not cached")
	}
	wrong := New(strings.Repeat("a", 64), t.TempDir())
	if err := wrong.UpdateTimeout(time.Second); err == nil || calls.Load() != 1 {
		t.Fatal("legacy accepted official hash")
	}
}

func TestGravatarConcurrentCacheWrites(t *testing.T) {
	data := testImage(t)
	var calls atomic.Int32
	fakeProvider(t, func(w http.ResponseWriter, r *http.Request) { calls.Add(1); w.Write(data) })
	dir := t.TempDir()
	fallback := filepath.Join(t.TempDir(), "fallback.jpg")
	if err := os.WriteFile(fallback, data, 0600); err != nil {
		t.Fatal(err)
	}
	hash := strings.Repeat("a", 64)
	handler := CacheServer(dir, fallback)
	done := make(chan *httptest.ResponseRecorder, 6)
	for i := 0; i < 6; i++ {
		go func() {
			w := httptest.NewRecorder()
			handler.ServeHTTP(w, httptest.NewRequest("GET", "/avatar/"+hash+"?size=28", nil))
			done <- w
		}()
	}
	for i := 0; i < 6; i++ {
		w := <-done
		cfg, _, err := image.DecodeConfig(bytes.NewReader(w.Body.Bytes()))
		if w.Code != 200 || err != nil || cfg.Width != 28 || cfg.Height != 28 {
			t.Errorf("concurrent response status=%d config=%+v error=%v", w.Code, cfg, err)
		}
	}
	a := New(hash, dir)
	if calls.Load() == 0 || !a.HasCache() || a.Expired() {
		t.Fatal("no fresh provider image published")
	}
	if _, err := decodeFile(a.imagePath); err != nil {
		t.Fatalf("published image invalid: %v", err)
	}
	entries, _ := os.ReadDir(dir)
	if len(entries) != 1 || entries[0].Name() != hash+".image" {
		t.Errorf("concurrent fetch left artifacts %v", entries)
	}
}
