package cmd

import (
	"bufio"
	"fmt"
	"io"
	"net"
	"net/textproto"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/gogits/gogs/modules/mailer"
	"github.com/gogits/gogs/modules/setting"
	"gopkg.in/ini.v1"
)

var viewMailerOnce sync.Once

type viewMail struct{ recipients, body string }

func fakeViewSMTP(t *testing.T) (<-chan viewMail, func(int) []viewMail) {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	oldMail, oldCfg, oldNotify := setting.MailService, setting.Cfg, setting.Service.EnableNotifyMail
	setting.MailService = &setting.Mailer{Host: ln.Addr().String(), From: "gogs@example.test"}
	setting.Cfg = ini.Empty()
	viewMailerOnce.Do(mailer.NewMailerContext)
	mails := make(chan viewMail, 20)
	var wg sync.WaitGroup
	accepted := make(chan struct{})
	go func() {
		defer close(accepted)
		for {
			conn, err := ln.Accept()
			if err != nil {
				return
			}
			wg.Add(1)
			go func() {
				defer wg.Done()
				defer conn.Close()
				conn.SetDeadline(time.Now().Add(5 * time.Second))
				r := textproto.NewReader(bufio.NewReader(conn))
				w := bufio.NewWriter(conn)
				write := func(s string) { fmt.Fprint(w, s+"\r\n"); w.Flush() }
				write("220 localhost SMTP")
				var recipients, body string
				for {
					line, err := r.ReadLine()
					if err != nil {
						return
					}
					switch {
					case strings.HasPrefix(line, "EHLO"), strings.HasPrefix(line, "HELO"):
						write("250 localhost")
					case strings.HasPrefix(line, "MAIL FROM:"):
						write("250 ok")
					case strings.HasPrefix(line, "RCPT TO:"):
						recipients += line + "\n"
						write("250 ok")
					case line == "DATA":
						write("354 continue")
						b, err := io.ReadAll(r.DotReader())
						if err != nil {
							return
						}
						body = string(b)
						write("250 queued")
					case line == "QUIT":
						write("221 bye")
						mails <- viewMail{recipients, body}
						return
					default:
						write("250 ok")
					}
				}
			}()
		}
	}()
	t.Cleanup(func() {
		ln.Close()
		<-accepted
		wg.Wait()
		setting.MailService, setting.Cfg, setting.Service.EnableNotifyMail = oldMail, oldCfg, oldNotify
	})
	wait := func(n int) []viewMail {
		t.Helper()
		result := []viewMail{}
		for i := 0; i < n; i++ {
			select {
			case m := <-mails:
				result = append(result, m)
			case <-time.After(5 * time.Second):
				t.Fatalf("only %d/%d queued notifications delivered", len(result), n)
			}
		}
		wg.Wait()
		return result
	}
	return mails, wait
}
