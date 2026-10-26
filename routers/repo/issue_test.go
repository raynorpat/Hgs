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
