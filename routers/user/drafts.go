package user

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"time"

	"github.com/gogits/gogs/models"
	"github.com/gogits/gogs/modules/collaboration"
	"github.com/gogits/gogs/modules/editordraft"
	"github.com/gogits/gogs/modules/middleware"
	"github.com/gogits/gogs/modules/setting"
	"github.com/macaron-contrib/csrf"
)

func NewDraftService() *editordraft.Service {
	return editordraft.NewService(models.NewEditorDraftStore(), func(ctx context.Context, actor int64, t editordraft.Target) error {
		return models.AuthorizeEditorDraft(ctx, actor, t.RepositoryID, t.Kind, t.ItemID)
	}, time.Now)
}

type draftRequest struct {
	Target                    editordraft.Target
	CollectionMode            string
	PageSecret, DisposalToken string
	Version                   int64
	Payload                   editordraft.Payload
}

func draftDecode(ctx *middleware.Context) (draftRequest, bool) {
	ctx.Resp.Header().Set("Cache-Control", "private, no-store")
	var in draftRequest
	ctx.Req.Request.Body = http.MaxBytesReader(ctx.Resp, ctx.Req.Request.Body, 768<<10)
	d := json.NewDecoder(ctx.Req.Request.Body)
	d.DisallowUnknownFields()
	if err := d.Decode(&in); err != nil {
		draftError(ctx, editordraft.ErrInvalidPayload)
		return in, false
	}
	var extra interface{}
	if d.Decode(&extra) != io.EOF {
		draftError(ctx, editordraft.ErrInvalidPayload)
		return in, false
	}
	return in, true
}
func draftError(ctx *middleware.Context, err error) {
	status, code := 503, "save_failed"
	switch {
	case errors.Is(err, editordraft.ErrUnauthenticated):
		status, code = 401, "unauthenticated"
	case errors.Is(err, editordraft.ErrForbidden):
		status, code = 403, "forbidden"
	case errors.Is(err, editordraft.ErrExpired):
		status, code = 410, "expired"
	case errors.Is(err, editordraft.ErrDiscarded):
		status, code = 410, "discarded"
	case errors.Is(err, editordraft.ErrStaleVersion):
		status, code = 409, "stale_version"
	case errors.Is(err, editordraft.ErrLimitExceeded):
		status, code = 409, "limit_exceeded"
	case errors.Is(err, editordraft.ErrInvalidPayload):
		status, code = 422, "invalid_payload"
	}
	ctx.Resp.Header().Set("Cache-Control", "private, no-store")
	ctx.JSON(status, map[string]string{"code": code})
}
func draftCollection(ctx *middleware.Context) string {
	c, err := ctx.Req.Cookie("hgs_draft")
	if err != nil || len(c.Value) != 64 {
		return ""
	}
	b, err := hex.DecodeString(c.Value)
	if err != nil || len(b) != 32 {
		return ""
	}
	return c.Value
}
func setDraftCollection(ctx *middleware.Context, id string) {
	http.SetCookie(ctx.Resp, &http.Cookie{Name: "hgs_draft", Value: id, Path: setting.AppSubUrl + "/user/drafts", HttpOnly: true, Secure: setting.SessionCookieSecure(setting.AppUrl, setting.SessionConfig.Secure) || setting.Protocol == setting.HTTPS || ctx.Req.TLS != nil, SameSite: http.SameSiteLaxMode, MaxAge: 1800})
}
func DraftSession(ctx *middleware.Context, token csrf.CSRF) {
	if !middleware.DraftRequestPolicy(ctx, token, false) {
		return
	}
	ctx.JSON(200, map[string]interface{}{"code": "ok", "actorID": ctx.User.Id, "csrf": token.GetToken(), "collectionPresent": draftCollection(ctx) != ""})
}
func DraftCheck(ctx *middleware.Context, token csrf.CSRF) {
	if !middleware.DraftRequestPolicy(ctx, token, true) {
		return
	}
	var in struct {
		Target   editordraft.Target
		Metadata map[string]string
	}
	ctx.Req.Request.Body = http.MaxBytesReader(ctx.Resp, ctx.Req.Request.Body, 8192)
	d := json.NewDecoder(ctx.Req.Request.Body)
	d.DisallowUnknownFields()
	var extra interface{}
	if err := d.Decode(&in); err != nil {
		draftError(ctx, editordraft.ErrInvalidPayload)
		return
	}
	if d.Decode(&extra) != io.EOF {
		draftError(ctx, editordraft.ErrInvalidPayload)
		return
	}
	if err := models.CheckEditorDraft(ctx.Req.Context(), ctx.User.Id, in.Target.RepositoryID, in.Target.Kind, in.Target.ItemID, in.Metadata); err != nil {
		draftError(ctx, err)
		return
	}
	ctx.JSON(200, map[string]string{"code": "ok"})
}
func DraftOpen(ctx *middleware.Context, token csrf.CSRF, s *editordraft.Service) {
	if !middleware.DraftRequestPolicy(ctx, token, true) {
		return
	}
	in, ok := draftDecode(ctx)
	if !ok {
		return
	}
	collection := draftCollection(ctx)
	if collection == "" {
		if in.CollectionMode == "random" {
			var err error
			collection, err = editordraft.RandomID()
			if err != nil {
				draftError(ctx, err)
				return
			}
		} else if in.CollectionMode == "session" && middleware.EstablishedDraftSession(ctx) {
			mac := hmac.New(sha256.New, []byte(setting.SecretKey))
			fmt.Fprintf(mac, "hgs-editor-draft-collection-v1\x00%s\x00%d\x00%s", ctx.Session.ID(), ctx.User.Id, setting.AppSubUrl)
			collection = hex.EncodeToString(mac.Sum(nil))
		} else {
			ctx.JSON(409, map[string]string{"code": "recovery_unavailable"})
			return
		}
	}
	g, err := s.Open(ctx.Req.Context(), ctx.User.Id, collection, in.Target)
	if err != nil {
		draftError(ctx, err)
		return
	}
	setDraftCollection(ctx, collection)
	ctx.JSON(200, map[string]interface{}{"code": "ok", "grant": g})
}
func DraftSave(ctx *middleware.Context, token csrf.CSRF, s *editordraft.Service, collaboration *collaboration.Service) {
	if !middleware.DraftRequestPolicy(ctx, token, true) {
		return
	}
	in, ok := draftDecode(ctx)
	if !ok {
		return
	}
	collection := draftCollection(ctx)
	if collection == "" {
		ctx.JSON(409, map[string]string{"code": "recovery_unavailable"})
		return
	}
	// Verify this page capability before inspecting any payload-dependent target.
	if _, err := s.Restore(ctx.Req.Context(), ctx.User.Id, collection, ctx.Params(":page"), in.PageSecret); err != nil {
		draftError(ctx, err)
		return
	}
	if err := draftMetadata(ctx, collaboration, ctx.Params(":page"), in.Payload); err != nil {
		draftError(ctx, err)
		return
	}
	snapshot, err := s.Save(ctx.Req.Context(), ctx.User.Id, collection, ctx.Params(":page"), in.PageSecret, in.Version, in.Payload)
	if err != nil {
		draftError(ctx, err)
		return
	}
	setDraftCollection(ctx, collection)
	ctx.JSON(200, map[string]interface{}{"code": "ok", "snapshot": snapshot})
}
func DraftRestore(ctx *middleware.Context, token csrf.CSRF, s *editordraft.Service, collaboration *collaboration.Service) {
	if !middleware.DraftRequestPolicy(ctx, token, true) {
		return
	}
	in, ok := draftDecode(ctx)
	if !ok {
		return
	}
	collection := draftCollection(ctx)
	if collection == "" {
		ctx.JSON(409, map[string]string{"code": "recovery_unavailable"})
		return
	}
	snapshot, err := s.Restore(ctx.Req.Context(), ctx.User.Id, collection, ctx.Params(":page"), in.PageSecret)
	if err != nil {
		draftError(ctx, err)
		return
	}
	if err := draftMetadata(ctx, collaboration, ctx.Params(":page"), snapshot.Payload); err != nil {
		draftError(ctx, err)
		return
	}
	ctx.JSON(200, map[string]interface{}{"code": "ok", "snapshot": snapshot})
}

func draftMetadata(ctx *middleware.Context, s *collaboration.Service, page string, p editordraft.Payload) error {
	r, err := models.NewEditorDraftStore().Find(ctx.Req.Context(), page)
	if err != nil {
		return err
	}
	if err = editordraft.ValidatePayload(r.Kind, p); err != nil {
		return err
	}
	if r.Kind == "pull_comment" && p.Metadata["comparison_id"] != "" {
		id, _ := strconv.ParseInt(p.Metadata["comparison_id"], 10, 64)
		line, _ := strconv.Atoi(p.Metadata["line"])
		_, err = s.ValidatePullCommentAnchor(ctx.Req.Context(), ctx.User.Id, r.ItemID, &collaboration.InlineAnchor{ComparisonID: id, Path: p.Metadata["path"], Side: p.Metadata["side"], Line: line})
		if err != nil {
			if errors.Is(err, models.ErrPullInvalid) {
				return editordraft.ErrInvalidPayload
			}
			if errors.Is(err, models.ErrInvalidCredential) || errors.Is(err, models.ErrPullNotExist) {
				return editordraft.ErrForbidden
			}
			return err
		}
	}
	return nil
}
func DraftDiscard(ctx *middleware.Context, s *editordraft.Service) {
	in, ok := draftDecode(ctx)
	if !ok {
		return
	}
	if err := s.Discard(ctx.Req.Context(), ctx.Params(":page"), in.DisposalToken); err != nil {
		draftError(ctx, err)
		return
	}
	ctx.JSON(200, map[string]string{"code": "ok"})
}
