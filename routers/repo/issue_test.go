package repo

import (
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/Unknwon/macaron"

	"github.com/gogits/gogs/modules/middleware"
	"github.com/gogits/gogs/modules/setting"
)

func TestUploadFilesIgnoresNonMultipartPost(t *testing.T) {
	enabled := setting.AttachmentEnabled
	setting.AttachmentEnabled = true
	defer func() { setting.AttachmentEnabled = enabled }()

	req := httptest.NewRequest("POST", "/owner/repo/issues/new", strings.NewReader("title=t"))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	ctx := &middleware.Context{Context: &macaron.Context{Req: macaron.Request{Request: req}}}

	uploadFiles(ctx, 1, 0) // panicked with a nil MultipartForm before the fix
}

func TestIssueDeadlineNativeAndRetainedForms(t *testing.T) {
	for _, value := range []string{"2027-04-03", "04/03/2027"} {
		date, err := parseIssueDeadline(value)
		if err != nil || date.UTC().Format("2006-01-02") != "2027-04-03" {
			t.Fatalf("deadline %q: %v %v", value, date, err)
		}
	}
	if _, err := parseIssueDeadline("2027-02-30"); err == nil {
		t.Fatal("invalid calendar date accepted")
	}
}
