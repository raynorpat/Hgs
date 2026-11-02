package hg

import (
	"bufio"
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"time"

	"github.com/gogits/gogs/modules/log"
)

// ErrNoCommonRoot means two directories share nothing but a filesystem root.
var ErrNoCommonRoot = errors.New("hg: directories have no common root below the filesystem root")

// resolve makes p absolute and resolves symlinks in its longest existing prefix.
func resolve(p string) (string, error) {
	p, err := filepath.Abs(p)
	if err != nil {
		return "", err
	}
	r, err := filepath.EvalSymlinks(p)
	if errors.Is(err, fs.ErrNotExist) && filepath.Dir(p) != p {
		parent, perr := resolve(filepath.Dir(p))
		return filepath.Join(parent, filepath.Base(p)), perr
	}
	return r, err
}

func sameName(a, b string) bool {
	if runtime.GOOS == "windows" {
		return strings.EqualFold(a, b)
	}
	return a == b
}

// within reports whether p is dir or below it.
func within(p, dir string) bool {
	rel, err := filepath.Rel(dir, p)
	return err == nil && rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator))
}

// CommonRoot returns the deepest directory containing both a and b, after
// resolving symlinks. A filesystem or volume root does not count.
func CommonRoot(a, b string) (string, error) {
	a, err := resolve(a)
	if err != nil {
		return "", err
	}
	if b, err = resolve(b); err != nil {
		return "", err
	}
	if !sameName(filepath.VolumeName(a), filepath.VolumeName(b)) {
		return "", ErrNoCommonRoot
	}
	for d := a; filepath.Dir(d) != d; d = filepath.Dir(d) {
		if within(b, d) {
			return d, nil
		}
	}
	return "", ErrNoCommonRoot
}

// ReadServer owns the hgqlserve child and restarts it when it exits.
type ReadServer struct {
	binary, root, tokenFile string
	client                  *ReadClient

	mu      sync.Mutex
	child   *Child
	stopped bool
	stop    chan struct{}
	done    chan struct{}
}

// StartReadServer starts hgqlserve over root, points client at it and returns
// once a query succeeds.
func StartReadServer(ctx context.Context, binary, root, tokenDir string, client *ReadClient) (*ReadServer, error) {
	if binary == "" {
		return nil, errors.New("hg: HGQLSERVE is not set")
	}
	if !filepath.IsAbs(binary) {
		return nil, fmt.Errorf("hg: HGQLSERVE %q is not an absolute path", binary)
	}
	if fi, err := os.Stat(binary); err != nil || fi.IsDir() {
		return nil, fmt.Errorf("hg: HGQLSERVE %q is missing: %v", binary, err)
	}
	if err := os.MkdirAll(tokenDir, 0700); err != nil {
		return nil, fmt.Errorf("hg: create token directory: %v", err)
	}
	s := &ReadServer{binary: binary, root: root, tokenFile: filepath.Join(tokenDir, "token"), client: client,
		stop: make(chan struct{}), done: make(chan struct{})}
	child, err := s.launch(ctx)
	if err != nil {
		return nil, err
	}
	s.child = child
	go s.supervise()
	return s, nil
}

// launch starts a child with a fresh token and waits until it answers.
func (s *ReadServer) launch(ctx context.Context) (*Child, error) {
	raw := make([]byte, 32)
	if _, err := rand.Read(raw); err != nil {
		return nil, err
	}
	token := hex.EncodeToString(raw)
	// The service reads the file once at start, so each start gets a new one.
	// Removing it first stops an existing file keeping wider permissions.
	if err := os.Remove(s.tokenFile); err != nil && !errors.Is(err, fs.ErrNotExist) {
		return nil, fmt.Errorf("hg: remove old token file: %v", err)
	}
	if err := os.WriteFile(s.tokenFile, []byte(token), 0600); err != nil {
		return nil, fmt.Errorf("hg: write token file: %v", err)
	}
	cmd := exec.Command(s.binary, "--root", s.root, "-l", "127.0.0.1:0", "--token-file", s.tokenFile)
	cmd.Dir = s.root
	for _, k := range append(inheritedEnv, platformEnv...) {
		if v, ok := os.LookupEnv(k); ok {
			cmd.Env = append(cmd.Env, k+"="+v)
		}
	}
	c, err := startChild(cmd)
	if err != nil {
		return nil, fmt.Errorf("hg: start HGQLSERVE: %v", err)
	}
	go func() {
		c.err = c.tree.wait(cmd)
		c.Stdin.Close()
		close(c.done)
	}()
	stderr := &stderrTail{done: make(chan struct{})}
	go stderr.scan(c.Stderr)

	ctx, cancel := context.WithTimeout(ctx, StartupTimeout)
	defer cancel()
	fail := func(err error) (*Child, error) {
		c.kill(ErrStopped)
		<-c.done
		s.client.SetEndpoint("", "")
		c.Stdout.Close()
		c.Stderr.Close()
		return nil, err
	}
	out := bufio.NewReader(c.Stdout)
	addr := make(chan string, 1)
	go func() {
		for {
			line, err := out.ReadString('\n')
			if a, ok := strings.CutPrefix(line, "Listening on "); ok {
				addr <- strings.TrimSpace(a)
				break
			}
			if err != nil {
				break
			}
		}
		// Keep draining so the child never blocks on a full pipe.
		io.Copy(io.Discard, out)
	}()
	select {
	case a := <-addr:
		s.client.SetEndpoint(a, token)
	case <-c.done:
		return fail(fmt.Errorf("hg: HGQLSERVE exited before listening: %s", stderr.last()))
	case <-ctx.Done():
		return fail(fmt.Errorf("hg: HGQLSERVE did not start listening: %w", ctx.Err()))
	}
	for {
		if err := s.client.Query(ctx, "Ready", "query Ready { __typename }", nil, &struct{}{}); err == nil {
			return c, nil
		}
		select {
		case <-time.After(20 * time.Millisecond):
		case <-c.done:
			return fail(errors.New("hg: HGQLSERVE exited before it was ready"))
		case <-ctx.Done():
			return fail(fmt.Errorf("hg: HGQLSERVE was not ready: %w", ctx.Err()))
		}
	}
}

const (
	stderrLineMax = 1024
	stderrTailMax = 5
)

// stderrTail logs a child's stderr line by line and keeps the last few lines.
type stderrTail struct {
	mu    sync.Mutex
	lines []string
	done  chan struct{}
}

func (t *stderrTail) scan(r io.Reader) {
	defer close(t.done)
	br := bufio.NewReaderSize(r, stderrLineMax)
	rest := false // still in a line already cut at stderrLineMax
	for {
		line, more, err := br.ReadLine()
		if err != nil {
			return
		}
		if !rest {
			s := strings.ToValidUTF8(string(line), "?")
			log.Warn("hgqlserve: %s", s)
			t.mu.Lock()
			if t.lines = append(t.lines, s); len(t.lines) > stderrTailMax {
				t.lines = t.lines[1:]
			}
			t.mu.Unlock()
		}
		rest = more
	}
}

// last waits briefly for stderr to end, then returns its last lines.
func (t *stderrTail) last() string {
	select {
	case <-t.done:
	case <-time.After(time.Second):
	}
	t.mu.Lock()
	defer t.mu.Unlock()
	return strings.Join(t.lines, "; ")
}

const (
	restartBackoffMin = 100 * time.Millisecond
	restartBackoffMax = 5 * time.Second
	restartStable     = 30 * time.Second
)

func (s *ReadServer) supervise() {
	defer close(s.done)
	backoff := restartBackoffMin
	for {
		s.mu.Lock()
		c := s.child
		s.mu.Unlock()
		started := c.started
		<-c.done
		// A dead child's port may be reused by another listener.
		s.client.SetEndpoint("", "")
		for {
			select {
			case <-s.stop:
				return
			default:
			}
			if time.Since(started) >= restartStable {
				backoff = restartBackoffMin
			}
			log.Warn("hgqlserve exited (%v); restarting in %v", c.err, backoff)
			select {
			case <-time.After(backoff):
			case <-s.stop:
				return
			}
			if backoff *= 2; backoff > restartBackoffMax {
				backoff = restartBackoffMax
			}
			ctx, cancel := context.WithCancel(context.Background())
			go func() {
				select {
				case <-s.stop:
					cancel()
				case <-ctx.Done():
				}
			}()
			next, err := s.launch(ctx)
			cancel()
			if err != nil {
				log.Error(4, "hgqlserve restart failed: %v", err)
				started = time.Now()
				continue
			}
			s.mu.Lock()
			if s.stopped {
				s.mu.Unlock()
				next.kill(ErrStopped)
				<-next.done
				return
			}
			s.child = next
			s.mu.Unlock()
			break
		}
	}
}

// Stop ends the service for good and waits for the child to be gone.
func (s *ReadServer) Stop(ctx context.Context) error {
	s.mu.Lock()
	if !s.stopped {
		s.stopped = true
		close(s.stop)
	}
	c := s.child
	s.mu.Unlock()
	c.kill(ErrStopped)
	select {
	case <-s.done:
	case <-ctx.Done():
		return ctx.Err()
	}
	<-c.done
	s.client.SetEndpoint("", "")
	c.Stdout.Close()
	c.Stderr.Close()
	return nil
}
