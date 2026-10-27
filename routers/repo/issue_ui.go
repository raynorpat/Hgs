package repo

import (
	"fmt"
	"github.com/gogits/gogs/models"
	"github.com/gogits/gogs/modules/middleware"
	"github.com/gogits/gogs/modules/setting"
	"github.com/gogits/gogs/modules/ui"
	"io"
	"net/http"
	"regexp"
	"strconv"
	"strings"
	"time"
)

func prepareIssueUI(ctx *middleware.Context) {
	ui.BuildShell(ctx, ui.Repository, "issues")
	ctx.Data["MarkdownContext"] = ctx.Repo.Owner.Name + "/" + ctx.Repo.Repository.Name
}
func validIssueAssignee(rid, uid int64) bool {
	if uid == 0 {
		return true
	}
	users, err := models.GetCollaborators(rid)
	if err != nil {
		return false
	}
	for _, u := range users {
		if u.Id == uid {
			return true
		}
	}
	return false
}
func parseIssueDeadline(value string) (time.Time, error) {
	if date, err := time.Parse("2006-01-02", value); err == nil {
		return date, nil
	}
	return time.Parse("01/02/2006", value)
}

func issueEditor(ctx *middleware.Context, id, value string) ui.MarkdownEditor {
	return ui.MarkdownEditor{ID: id, Value: value, Context: ctx.Repo.Owner.Name + "/" + ctx.Repo.Repository.Name, Label: ctx.Tr("ui.body"), Write: ctx.Tr("ui.write"), Preview: ctx.Tr("ui.preview"), Error: ctx.Tr("ui.preview_failed"), URL: setting.AppSubUrl + "/api/v1/markdown"}
}

func validateIssueLabels(rid int64, value string) bool {
	if value == "" {
		return true
	}
	if !strings.HasPrefix(value, "$") || !strings.HasSuffix(value, "|") {
		return false
	}
	for _, part := range strings.Split(strings.TrimSuffix(strings.TrimPrefix(value, "$"), "|"), "|$") {
		id, err := strconv.ParseInt(part, 10, 64)
		if err != nil {
			return false
		}
		label, err := models.GetLabelById(id)
		if err != nil || label.RepoId != rid {
			return false
		}
	}
	return true
}

var issueLabelColor = regexp.MustCompile(`^#[0-9a-fA-F]{6}$`)

func validateIssueUploads(ctx *middleware.Context) error {
	if !setting.AttachmentEnabled || ctx.Req.MultipartForm == nil {
		return nil
	}
	files := ctx.Req.MultipartForm.File["attachments"]
	if len(files) > setting.AttachmentMaxFiles {
		return ErrTooManyFiles
	}
	for _, header := range files {
		if setting.AttachmentMaxSize > 0 && header.Size > setting.AttachmentMaxSize*1024*1024 {
			return fmt.Errorf("attachment too large")
		}
		f, err := header.Open()
		if err != nil {
			return err
		}
		buf := make([]byte, 512)
		n, err := f.Read(buf)
		f.Close()
		if err != nil && err != io.EOF {
			return err
		}
		kind := http.DetectContentType(buf[:n])
		allowed := false
		for _, candidate := range strings.Split(setting.AttachmentAllowedTypes, "|") {
			candidate = strings.TrimSpace(candidate)
			if candidate == "*/*" || candidate == kind {
				allowed = true
			}
		}
		if !allowed {
			return ErrFileTypeForbidden
		}
	}
	return nil
}
