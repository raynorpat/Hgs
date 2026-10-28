package cmd

import (
	"context"
	"errors"
	"net"
	"net/http"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/gogits/gogs/modules/collaboration"
	"github.com/gogits/gogs/modules/hg"
	"github.com/gogits/gogs/modules/hosting"
	"github.com/gogits/gogs/modules/log"
	"github.com/gogits/gogs/modules/middleware"
	"github.com/gogits/gogs/modules/setting"
	hgroutes "github.com/gogits/gogs/routers/hg"
)

// hostingHolder is owned by runWeb, including initialization after installation.
// Requests obtain the current service through the injected hosting.Provider.
type hostingHolder struct {
	mu          sync.RWMutex
	once        sync.Once
	service     *hosting.Service
	sshListener net.Listener
	err         error
	closed      bool
	errors      chan error
}

func (h *hostingHolder) Service() *hosting.Service {
	h.mu.RLock()
	defer h.mu.RUnlock()
	if h.closed {
		return nil
	}
	return h.service
}

type fixedProvider struct{ s *hosting.Service }

func (p fixedProvider) Service() *hosting.Service { return p.s }

func (h *hostingHolder) Initialize() error {
	h.once.Do(func() {
		h.mu.Lock()
		defer h.mu.Unlock()
		if h.closed {
			h.err = errors.New("hosting: server shutting down")
			return
		}
		cfg := hosting.Config{Mercurial: hg.Config{Executable: setting.Mercurial.Executable, ConfigPath: setting.Mercurial.ConfigPath, MaxProcesses: setting.Mercurial.MaxProcesses, SessionTimeout: setting.Mercurial.SessionTimeout}, RepositoryRoot: setting.RepoRootPath, ReviewArtifactRoot: setting.Mercurial.ReviewArtifactRoot, ReviewOperationTimeout: setting.Mercurial.ReviewOperationTimeout, HostKeyPath: setting.SSHHostKeyPath, PublicURL: setting.AppUrl, AppSubURL: setting.AppSubUrl, SSHAddress: setting.SSHAddress, SSHAdvertisedHost: setting.SSHAdvertisedHost, SSHAdvertisedPort: setting.SSHPort, TrustedProxyCIDRs: setting.TrustedProxyCIDRs}
		if setting.DisableSSH {
			cfg.SSHAddress = ""
		}
		service, err := hosting.New(context.Background(), cfg)
		if err != nil {
			h.err = err
			return
		}
		if err := collaboration.ReconcileComparisons(context.Background(), service, filepath.Join(setting.StaticRootPath, "public"), setting.AvatarUploadPath); err != nil {
			ctx, cancel := context.WithTimeout(context.Background(), time.Second)
			defer cancel()
			service.Shutdown(ctx)
			h.err = err
			return
		}
		// Hosting already concluded revoked publications; resume or settle the rest before admission.
		if err := collaboration.New(fixedProvider{service}).ReconcileOperations(context.Background()); err != nil {
			log.Warn("merge reconciliation: %v", err)
		}
		if cfg.SSHAddress != "" && !setting.DisableSSH {
			ln, err := net.Listen("tcp", cfg.SSHAddress)
			if err != nil {
				ctx, cancel := context.WithTimeout(context.Background(), time.Second)
				defer cancel()
				service.Shutdown(ctx)
				h.err = err
				return
			}
			h.sshListener = ln
			go func() {
				if err := service.ListenSSH(ln); err != nil {
					select {
					case h.errors <- err:
					default:
					}
				}
			}()
		}
		h.service = service
	})
	return h.err
}
func (h *hostingHolder) Shutdown(ctx context.Context) error {
	h.mu.Lock()
	h.closed = true
	service := h.service
	if h.sshListener != nil {
		h.sshListener.Close()
	}
	h.mu.Unlock()
	if service != nil {
		return service.Shutdown(ctx)
	}
	return nil
}
func (h *hostingHolder) Handler(browser http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		transportPath := r.URL.Path
		if !strings.HasPrefix(transportPath, "/hg/") && transportPath != "/hg" && setting.AppSubUrl != "" && strings.HasPrefix(transportPath, setting.AppSubUrl+"/") {
			transportPath = strings.TrimPrefix(transportPath, setting.AppSubUrl)
		}
		if transportPath == "/hg" || strings.HasPrefix(transportPath, "/hg/") {
			service := h.Service()
			if service == nil {
				http.Error(w, "Mercurial hosting unavailable", http.StatusServiceUnavailable)
				return
			}
			hgroutes.Handler(service).ServeHTTP(w, r)
			return
		}
		r = middleware.WithBrowserResponseController(r, w)
		browser.ServeHTTP(w, r)
		if middleware.BrowserResponseAborted(r) {
			// net/http terminates the incomplete HTTP/1 response or HTTP/2 stream.
			// Keep this outside Macaron Recovery, which consumes handler panics.
			panic(http.ErrAbortHandler)
		}
	})
}
