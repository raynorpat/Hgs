// Package hosting owns the Mercurial runner, transports and live session authority.
package hosting

import (
	"context"
	"errors"
	"fmt"
	"net"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"

	cryptossh "golang.org/x/crypto/ssh"

	"github.com/gogits/gogs/models"
	"github.com/gogits/gogs/modules/hg"
	"github.com/gogits/gogs/modules/hgbridge"
	"github.com/gogits/gogs/modules/log"
	"github.com/gogits/gogs/modules/ssh"
)

// Provider supplies the current cmd-owned service to request handlers.
type Provider interface{ Service() *Service }

type Config struct {
	Mercurial                                                                        hg.Config
	RepositoryRoot, HostKeyPath, PublicURL, AppSubURL, SSHAddress, SSHAdvertisedHost string
	SSHAdvertisedPort                                                                int
	TrustedProxyCIDRs                                                                []string
}

type Service struct {
	cfg            Config
	runner         *hg.Runner
	inspector      *hg.Runner
	hookExecutable string
	controller     *hgbridge.Controller
	http           *hgbridge.HTTPBridge
	stdio          *hgbridge.StdioBridge
	ssh            *ssh.Server
	sessions       *registry
	proxies        []*net.IPNet
	cancel         context.CancelFunc
	monitorDone    chan struct{}
	shutdown       sync.Once
	shutdownErr    error
}

func (s *Service) configure(cfg Config) error {
	u, err := url.Parse(cfg.PublicURL)
	if err != nil || u.Host == "" || (u.Scheme != "https" && u.Scheme != "http") || u.User != nil || u.RawQuery != "" || u.Fragment != "" {
		return errors.New("hosting: invalid public URL")
	}
	if cfg.AppSubURL != "" {
		if !strings.HasPrefix(cfg.AppSubURL, "/") || strings.HasSuffix(cfg.AppSubURL, "/") || strings.ContainsAny(cfg.AppSubURL, "%\\?#") {
			return errors.New("hosting: invalid application sub-URL")
		}
		for _, p := range strings.Split(cfg.AppSubURL[1:], "/") {
			if p == "" || p == "." || p == ".." {
				return errors.New("hosting: invalid application sub-URL")
			}
		}
	}
	if cfg.SSHAdvertisedPort < 0 || cfg.SSHAdvertisedPort > 65535 {
		return errors.New("hosting: invalid SSH port")
	}
	if strings.TrimSuffix(u.Path, "/") != cfg.AppSubURL || u.RawPath != "" {
		return errors.New("hosting: public URL and application sub-URL disagree")
	}
	if cfg.SSHAdvertisedPort == 0 {
		cfg.SSHAdvertisedPort = 2222
	}
	if cfg.SSHAdvertisedHost == "" {
		cfg.SSHAdvertisedHost = u.Hostname()
	}
	host := strings.Trim(cfg.SSHAdvertisedHost, "[]")
	if host == "" || strings.ContainsAny(host, "/\\@?#% \t\r\n") || (strings.Contains(host, ":") && net.ParseIP(host) == nil) {
		return errors.New("hosting: invalid advertised SSH host")
	}
	if cfg.SSHAddress != "" {
		if _, _, err := net.SplitHostPort(cfg.SSHAddress); err != nil {
			return fmt.Errorf("hosting: invalid SSH address: %w", err)
		}
	}
	for _, cidr := range cfg.TrustedProxyCIDRs {
		_, network, err := net.ParseCIDR(cidr)
		if err != nil {
			return fmt.Errorf("hosting: invalid proxy CIDR: %w", err)
		}
		s.proxies = append(s.proxies, network)
	}
	s.cfg = cfg
	return nil
}

func New(ctx context.Context, cfg Config) (s *Service, err error) {
	s = &Service{sessions: newRegistry()}
	if err = s.configure(cfg); err != nil {
		return nil, err
	}
	if !filepath.IsAbs(cfg.RepositoryRoot) || !filepath.IsAbs(cfg.HostKeyPath) {
		return nil, errors.New("hosting: repository root and host key must be absolute")
	}
	if s.runner, err = hg.NewRunner(cfg.Mercurial); err != nil {
		return nil, err
	}
	inspectionConfig := cfg.Mercurial
	inspectionConfig.ConfigPath = filepath.Join(filepath.Dir(cfg.Mercurial.ConfigPath), "review-inspection", "hgrc")
	inspectionConfig.MaxProcesses = 1
	if s.inspector, err = hg.NewRunner(inspectionConfig); err != nil {
		return nil, err
	}
	reservations, err := models.PendingReviewReservations()
	if err != nil {
		return nil, err
	}
	for _, reservation := range reservations {
		s.sessions.reservations[reservation.RepoID] = reservation.OperationID
	}
	if err = s.runner.Check(ctx); err != nil {
		return nil, err
	}
	if err = s.reconcileRevokedPublicationsAtStartup(ctx); err != nil {
		return nil, err
	}
	signer, err := loadHostKey(cfg.HostKeyPath)
	if err != nil {
		return nil, err
	}
	if err = models.ReconcileHgRepositories(ctx, cfg.RepositoryRoot); err != nil {
		return nil, err
	}
	ttl := cfg.Mercurial.SessionTimeout
	if ttl == 0 {
		ttl = hg.DefaultSessionTimeout
	}
	if s.controller, err = hgbridge.NewController(s.authorize, ttl); err != nil {
		return nil, err
	}
	owned := s
	defer func() {
		if err != nil {
			if owned.http != nil {
				owned.http.Close()
			}
			if owned.stdio != nil {
				owned.stdio.Close()
			}
			owned.controller.Close(context.Background())
		}
	}()
	executable, err := os.Executable()
	if err != nil {
		return nil, err
	}
	s.hookExecutable = executable
	if s.http, err = hgbridge.NewHTTPBridge(s.runner, s.controller, executable); err != nil {
		return nil, err
	}
	if s.stdio, err = hgbridge.NewStdioBridge(s.runner, s.controller, executable, filepath.Dir(cfg.Mercurial.ConfigPath)); err != nil {
		return nil, err
	}
	s.http.Registrar = s
	s.stdio.Registrar = s
	s.ssh = ssh.NewServer(signer, func(ctx context.Context, key cryptossh.PublicKey) (hgbridge.Principal, error) {
		uid, kid, err := models.AuthenticateRepositoryKey(key)
		return hgbridge.Principal{UserID: uid, KeyID: kid}, err
	}, s.resolve, s.stdio)
	s.ssh.Registrar = s
	monitorCtx, cancel := context.WithCancel(context.Background())
	s.cancel = cancel
	s.monitorDone = make(chan struct{})
	go func() {
		defer close(s.monitorDone)
		t := time.NewTicker(time.Second)
		defer t.Stop()
		for {
			select {
			case <-t.C:
				s.sessions.check()
			case <-monitorCtx.Done():
				return
			}
		}
	}()
	return s, nil
}

func (s *Service) Runner() *hg.Runner     { return s.runner }
func (s *Service) RepositoryRoot() string { return s.cfg.RepositoryRoot }
func (s *Service) AppSubURL() string      { return s.cfg.AppSubURL }

// HTTP serves already-normalized repository transport paths; routers/hg owns external URL parsing.
func (s *Service) HTTP() http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		parts := strings.Split(r.URL.Path, "/")
		if len(parts) != 3 || parts[0] != "" || parts[1] == "" || parts[2] == "" || parts[1] == "." || parts[1] == ".." || parts[2] == "." || parts[2] == ".." {
			http.NotFound(w, r)
			return
		}
		copy := r.Clone(r.Context())
		u := *r.URL
		u.Path = "/"
		u.RawPath = ""
		copy.URL = &u
		s.ServeRepository(w, copy, parts[1], parts[2])
	})
}
func (s *Service) ListenSSH(ln net.Listener) error { return s.ssh.Serve(ln) }
func (s *Service) DrainRepository(ctx context.Context, id int64) error {
	return s.sessions.drain(ctx, id)
}
func (s *Service) Shutdown(ctx context.Context) error {
	s.shutdown.Do(func() {
		active := s.sessions.stopAdmissions()
		// Stop SSH accepts immediately, allowing its existing sessions the same drain deadline.
		sshDone := make(chan error, 1)
		go func() { sshDone <- s.ssh.Shutdown(ctx) }()
		err := waitSessions(ctx, active)
		cleanupCtx, cancelCleanup := context.WithTimeout(context.Background(), hg.ShutdownTimeout)
		defer cancelCleanup()
		if err != nil {
			for _, entry := range active {
				entry.cancel()
			}
			s.http.Close()
			s.stdio.Close()
			err = errors.Join(err, waitSessions(cleanupCtx, active))
		}
		var sshErr error
		select {
		case sshErr = <-sshDone:
		case <-cleanupCtx.Done():
			sshErr = cleanupCtx.Err()
		}
		s.http.Close()
		s.stdio.Close()
		s.cancel()
		select {
		case <-s.monitorDone:
		case <-cleanupCtx.Done():
			err = errors.Join(err, cleanupCtx.Err())
		}
		s.shutdownErr = errors.Join(err, sshErr, s.controller.Close(cleanupCtx))
	})
	return s.shutdownErr
}
func (s *Service) authorize(ctx context.Context, g hgbridge.Grant) error {
	s.sessions.mu.Lock()
	reserved := s.sessions.reservations[g.RepoID] != ""
	s.sessions.mu.Unlock()
	if reserved {
		return hgbridge.ErrDenied
	}
	if !g.CanWrite || s.validatePrincipal(g.Principal) != nil {
		return hgbridge.ErrDenied
	}
	access, err := models.RepositoryAccessFor(g.Principal.UserID, g.RepoID)
	if err != nil || !access.Write {
		return hgbridge.ErrDenied
	}
	return nil
}
func (s *Service) resolve(ctx context.Context, p hgbridge.Principal, owner, name string) (hgbridge.Target, hgbridge.Grant, error) {
	fail := func() (hgbridge.Target, hgbridge.Grant, error) {
		return hgbridge.Target{}, hgbridge.Grant{}, hgbridge.ErrDenied
	}
	if p.UserID != 0 && s.validatePrincipal(p) != nil {
		return fail()
	}
	user, err := models.GetUserByName(owner)
	if err != nil {
		return fail()
	}
	repo, err := models.GetRepositoryByName(user.Id, name)
	if err != nil {
		return fail()
	}
	access, err := models.RepositoryAccessFor(p.UserID, repo.Id)
	if err != nil || !access.Read {
		return fail()
	}
	path, err := models.RepositoryStoragePath(s.cfg.RepositoryRoot, repo.Id)
	if err != nil {
		return fail()
	}
	return hgbridge.Target{RepoID: repo.Id, Path: path}, hgbridge.Grant{Principal: p, RepoID: repo.Id, CanWrite: access.Write}, nil
}

// Register tracks readers and writers without issuing a capability. RepoID 0 tracks an authenticated SSH connection waiting for exec.
func (s *Service) Register(g hgbridge.Grant, cancel context.CancelFunc) (func(), error) {
	return s.register(g, cancel, false)
}

// RegisterBrowserRead tracks a separately authorized in-process browser read.
// It can never issue a transport/write grant or accept a browser cookie as a
// wire credential. Callers must authorize the UI operation before starting it.
func (s *Service) RegisterBrowserRead(userID, repoID int64, cancel context.CancelFunc) (func(), error) {
	if repoID <= 0 {
		return nil, hgbridge.ErrDenied
	}
	return s.register(hgbridge.Grant{Principal: hgbridge.Principal{UserID: userID}, RepoID: repoID}, cancel, true)
}
func (s *Service) validatePrincipal(p hgbridge.Principal) error {
	if err := models.ValidateRepositoryCredential(p.UserID, p.KeyID, p.TokenID); err != nil {
		return err
	}
	if p.KeyID == 0 && p.TokenID == 0 {
		return models.ValidateRepositoryPasswordVersion(p.UserID, p.PasswordVersion)
	}
	return nil
}
func (s *Service) register(g hgbridge.Grant, cancel context.CancelFunc, browserRead bool) (func(), error) {
	var owner int64
	var private bool
	if g.RepoID != 0 {
		repo, err := models.GetRepositoryById(g.RepoID)
		if err != nil {
			return nil, err
		}
		owner, private = repo.OwnerId, repo.IsPrivate
	}
	valid := func() bool {
		p := g.Principal
		if p.UserID != 0 {
			var err error
			if browserRead {
				err = models.ValidateRepositoryCredential(p.UserID, 0, 0)
			} else {
				err = s.validatePrincipal(p)
			}
			if err != nil {
				return false
			}
		}
		if g.RepoID == 0 {
			return p.UserID > 0
		}
		repo, err := models.GetRepositoryById(g.RepoID)
		if err != nil || repo.OwnerId != owner || repo.IsPrivate != private {
			return false
		}
		access, err := models.RepositoryAccessFor(p.UserID, g.RepoID)
		return err == nil && access.Read && (!g.CanWrite || access.Write)
	}
	if !valid() {
		return nil, hgbridge.ErrDenied
	}
	return s.sessions.register(g, cancel, valid)
}
func (s *Service) secure(r *http.Request) bool {
	if r.TLS != nil {
		return true
	}
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		return false
	}
	ip := net.ParseIP(host)
	for _, network := range s.proxies {
		if network.Contains(ip) {
			values := r.Header.Values("X-Forwarded-Proto")
			return len(values) == 1 && values[0] == "https"
		}
	}
	return false
}
func (s *Service) ServeRepository(w http.ResponseWriter, r *http.Request, owner, name string) {
	_, credentials := r.Header["Authorization"]
	cmd := r.URL.Query().Get("cmd")
	if !s.secure(r) && (credentials || r.Method == http.MethodPost || cmd == "unbundle" || cmd == "pushkey" || cmd == "batch") {
		http.Error(w, "TLS required", http.StatusForbidden)
		return
	}
	var p hgbridge.Principal
	challenge := func() {
		w.Header().Set("WWW-Authenticate", `Basic realm="Mercurial"`)
		http.Error(w, "authentication required", http.StatusUnauthorized)
	}
	if credentials {
		username, password, ok := r.BasicAuth()
		if !ok {
			challenge()
			return
		}
		identity, err := models.AuthenticateRepositoryHTTPSIdentity(username, password)
		if err != nil {
			if errors.Is(err, models.ErrInvalidCredential) {
				challenge()
			} else {
				log.Error(4, "hosting: repository authentication backend failed: %v", err)
				http.Error(w, "repository authentication unavailable", http.StatusServiceUnavailable)
			}
			return
		}
		p = hgbridge.Principal{UserID: identity.UserID, TokenID: identity.TokenID, PasswordVersion: identity.PasswordVersion}
	}
	target, grant, err := s.resolve(r.Context(), p, owner, name)
	if err != nil {
		if p.UserID == 0 {
			challenge()
		} else {
			http.NotFound(w, r)
		}
		return
	}
	if err = s.http.Serve(r.Context(), w, r, target, grant); err != nil {
		http.Error(w, "repository transport unavailable", http.StatusServiceUnavailable)
	}
}

// CloneLinks uses only validated operator configuration, never the request Host.
func (s *Service) CloneLinks(owner, repository string) models.CloneLink {
	base, _ := url.Parse(s.cfg.PublicURL)
	base.Path = s.cfg.AppSubURL + "/hg/" + owner + "/" + repository
	base.RawPath = s.cfg.AppSubURL + "/hg/" + url.PathEscape(owner) + "/" + url.PathEscape(repository)
	links := models.CloneLink{HTTPS: base.String()}
	if s.cfg.SSHAddress != "" {
		host := strings.Trim(s.cfg.SSHAdvertisedHost, "[]")
		port := s.cfg.SSHAdvertisedPort
		if port == 0 {
			port = 2222
		}
		u := &url.URL{Scheme: "ssh", User: url.User("hg"), Host: net.JoinHostPort(host, strconv.Itoa(port)), Path: "/" + owner + "/" + repository, RawPath: "/" + url.PathEscape(owner) + "/" + url.PathEscape(repository)}
		links.SSH = u.String()
	}
	return links
}
