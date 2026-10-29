//go:build integration

package integration

import (
	"bufio"
	"context"
	"fmt"
	"github.com/Unknwon/macaron"
	"github.com/gogits/gogs/models"
	"github.com/gogits/gogs/modules/mailer"
	"github.com/gogits/gogs/modules/middleware"
	"github.com/gogits/gogs/modules/setting"
	"github.com/gogits/gogs/routers/user"
	"github.com/macaron-contrib/csrf"
	"net"
	"net/url"
	"strings"
	"testing"
	"time"
)

func TestContributionEmailProof(t *testing.T) {
	f := accountApp(t, true)
	old := setting.MailService
	t.Cleanup(func() { setting.MailService = old })
	setting.MailService = nil
	f.m.Post("/user/settings/email/verify", middleware.Toggle(&middleware.ToggleOptions{SignInRequire: true}), func(c *macaron.Context, x csrf.CSRF) { csrf.Validate(c, x) }, user.SettingsEmailVerify)
	f.m.Get("/user/activate_email", user.ActivateEmail)
	page := f.request(t, "GET", "/user/settings/email", f.member.Name, nil, false)
	if strings.Contains(page.Body.String(), `action="/code/user/settings/email/verify"`) {
		t.Fatal("disabled mail offers a verification action")
	}
	if w := f.request(t, "POST", "/user/settings/email/verify", f.member.Name, url.Values{"id": {"0"}}, false); w.Code != 400 {
		t.Fatalf("missing CSRF: %d", w.Code)
	}
	if w := f.request(t, "POST", "/user/settings/email/verify", f.member.Name, url.Values{"id": {"0"}}, true); w.Code != 503 {
		t.Fatalf("disabled mail: %d", w.Code)
	}
	setting.MailService = &setting.Mailer{From: "fixture@example.invalid"}
	sink, restore := mailer.CaptureIntegrationMail()
	defer restore()
	if w := f.request(t, "POST", "/user/settings/email/verify", f.member.Name, url.Values{"id": {"999999"}}, true); w.Code != 404 {
		t.Fatalf("foreign selection: %d", w.Code)
	}
	if w := f.request(t, "POST", "/user/settings/email/verify", f.member.Name, url.Values{"id": {"0"}}, true); w.Code != 302 {
		t.Fatalf("verification send: %d %s", w.Code, w.Body)
	}
	message := <-sink
	if len(message.To) != 1 || message.To[0] != f.member.Email || strings.Contains(message.Info, f.member.Email) {
		t.Fatalf("mail recipient/privacy: %+v", message)
	}
	// Deliver only to an owned loopback SMTP sink; no external account is used.
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer listener.Close()
	setting.MailService.Host = listener.Addr().String()
	smtpResult := make(chan string, 1)
	go func() {
		conn, err := listener.Accept()
		if err != nil {
			smtpResult <- "accept failed"
			return
		}
		defer conn.Close()
		conn.SetDeadline(time.Now().Add(5 * time.Second))
		fmt.Fprint(conn, "220 owned-fixture ESMTP\r\n")
		scanner := bufio.NewScanner(conn)
		var body strings.Builder
		data := false
		for scanner.Scan() {
			line := scanner.Text()
			if data {
				if line == "." {
					data = false
					fmt.Fprint(conn, "250 accepted\r\n")
				} else {
					body.WriteString(line)
					body.WriteByte('\n')
				}
				continue
			}
			switch {
			case strings.HasPrefix(line, "EHLO"), strings.HasPrefix(line, "HELO"):
				fmt.Fprint(conn, "250 owned-fixture\r\n")
			case strings.HasPrefix(line, "MAIL FROM:"), strings.HasPrefix(line, "RCPT TO:"):
				fmt.Fprint(conn, "250 accepted\r\n")
			case line == "DATA":
				data = true
				fmt.Fprint(conn, "354 send message\r\n")
			case line == "QUIT":
				fmt.Fprint(conn, "221 bye\r\n")
				smtpResult <- body.String()
				return
			default:
				fmt.Fprint(conn, "500 unsupported\r\n")
			}
		}
		smtpResult <- "incomplete SMTP exchange"
	}()
	if n, err := mailer.Send(message); err != nil || n != 1 {
		t.Fatalf("owned fake SMTP delivery: n=%d err=%v", n, err)
	}
	if received := <-smtpResult; !strings.Contains(received, "user/activate_email?code=") {
		t.Fatal("fake SMTP did not receive confirmation")
	}
	start := strings.Index(message.Body, "user/activate_email?code=")
	if start < 0 {
		t.Fatal("confirmation link absent")
	}
	fragment := message.Body[start:]
	end := strings.Index(fragment, "\"")
	if end < 0 {
		t.Fatal("link missing closing quote")
	}
	link, err := url.Parse("/" + fragment[:end])
	if err != nil {
		t.Fatal(err)
	}
	if link.Query().Get("email") != "" {
		t.Fatal("new confirmation URL leaks lookup address")
	}
	if w := f.request(t, "POST", "/user/settings/email/verify", f.member.Name, url.Values{"id": {"0"}}, true); w.Code != 429 {
		t.Fatalf("resend limit: %d", w.Code)
	}
	if w := f.request(t, "GET", link.String(), "", nil, false); w.Code != 302 {
		t.Fatalf("confirmation: %d", w.Code)
	}
	if id, err := models.VerifiedContributionAuthor(context.Background(), f.member.Email); err != nil || id != f.member.Id {
		t.Fatalf("proof: %d %v", id, err)
	}
	select {
	case <-sink:
		t.Fatal("unsolicited duplicate mail")
	default:
	}
}
