package middleware

import (
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Unknwon/macaron"
	"github.com/gogits/gogs/modules/base"
	"github.com/macaron-contrib/session"
)

func TestRenderWithErrSubmittedFormState(t *testing.T) {
	for _, submitted := range []bool{false, true} {
		t.Run(map[bool]string{false: "message-only", true: "submitted-form"}[submitted], func(t *testing.T) {
			root := t.TempDir()
			if err := os.WriteFile(filepath.Join(root, "form.tmpl"), []byte(`{{if .HasError}}submitted{{else}}fresh{{end}}|{{.name}}|{{.Flash.ErrorMsg}}`), 0600); err != nil {
				t.Fatal(err)
			}
			m := macaron.New()
			m.Use(macaron.Renderer(macaron.RenderOptions{Directory: root}))
			m.Post("/form", func(c *macaron.Context) {
				ctx := &Context{Context: c, Flash: &session.Flash{}}
				ctx.Data["name"] = "persisted"
				var form interface{}
				if submitted {
					form = &struct {
						Name string `form:"name"`
					}{Name: "safe submitted value"}
				}
				ctx.RenderWithErr("validation message", base.TplName("form"), form)
				if submitted && (!ctx.HasError() || ctx.GetErrMsg() != "validation message") {
					t.Error("current form error contract missing")
				}
				if !submitted && ctx.HasError() {
					t.Error("message-only render became submitted state")
				}
			})
			w := httptest.NewRecorder()
			m.ServeHTTP(w, httptest.NewRequest("POST", "/form", nil))
			want := "fresh|persisted|validation message"
			if submitted {
				want = "submitted|safe submitted value|validation message"
			}
			if w.Code != 200 || strings.TrimSpace(w.Body.String()) != want {
				t.Fatalf("status=%d body=%q, want %q", w.Code, w.Body.String(), want)
			}
		})
	}
}
