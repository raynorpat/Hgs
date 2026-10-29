//go:build integration

package integration

import (
	"context"
	"net"
	"net/http"
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/gogits/gogs/models"
	"github.com/gogits/gogs/modules/mailer"
	"github.com/gogits/gogs/modules/setting"
)

// Startup-only state and an owned mail sink; all account actions use normal
// production auth/activation routes, with no session bypass.
func TestActivationBrowserFixture(t *testing.T) {
	if os.Getenv("UI_ACTIVATION_FIXTURE") != "1" {
		t.Skip("opt-in owned activation browser fixture")
	}
	f := accountApp(t, true)
	capture, restore := mailer.CaptureIntegrationMail()
	defer restore()
	oldMail := setting.MailService
	oldURL := setting.AppUrl
	t.Cleanup(func() { setting.MailService = oldMail; setting.AppUrl = oldURL })
	setting.Service.RegisterEmailConfirm = true
	setting.Service.ActiveCodeLives = 180
	setting.MailService = &setting.Mailer{From: "fixture@example.invalid"}
	u := &models.User{Name: "pending-review", Email: "pending-review@example.invalid", Passwd: "password", PendingActivation: true}
	if err := models.CreateUser(u); err != nil {
		t.Fatal(err)
	}
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	setting.AppUrl = "http://" + listener.Addr().String() + "/code/"
	stop := make(chan struct{})
	var once sync.Once
	assets := http.StripPrefix("/code/", http.FileServer(http.Dir("../../public")))
	server := &http.Server{Handler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == "GET" && r.URL.Path == "/fixture/stop" {
			w.Write([]byte("stopping owned fixture"))
			once.Do(func() { close(stop) })
			return
		}
		for _, prefix := range []string{"/code/css/", "/code/js/", "/code/img/", "/code/fonts/"} {
			if strings.HasPrefix(r.URL.Path, prefix) {
				assets.ServeHTTP(w, r)
				return
			}
		}
		f.m.ServeHTTP(w, r)
	})}
	done := make(chan error, 1)
	go func() { done <- server.Serve(listener) }()
	t.Logf("ACTIVATION_BROWSER_URL=%suser/activate; pending username=pending-review password=password; normal admin username=administrator password=password; stop=http://%s/fixture/stop", setting.AppUrl, listener.Addr())
	timer := time.NewTimer(30 * time.Minute)
	defer timer.Stop()
loop:
	for {
		select {
		case msg := <-capture:
			t.Logf("OWNED_ACTIVATION_MAIL=%s", msg.Body)
		case <-stop:
			break loop
		case <-timer.C:
			break loop
		}
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := server.Shutdown(ctx); err != nil {
		t.Fatal(err)
	}
	if err := <-done; err != http.ErrServerClosed {
		t.Fatal(err)
	}
}
