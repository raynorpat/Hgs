package middleware

import (
	"context"
	"errors"
	"strings"

	"github.com/gogits/gogs/models"
	"github.com/gogits/gogs/modules/hg"
	"github.com/gogits/gogs/modules/hgbridge"
	"github.com/gogits/gogs/modules/hosting"
	"github.com/gogits/gogs/modules/profile"
)

// LoadProfileReadme owns one request-local managed read through rendering and
// final authorization. Call immediately before rendering the profile response.
func LoadProfileReadme(ctx *Context, provider hosting.Provider, owner *models.User) (profile.Content, error) {
	absent := profile.Content{State: "absent"}
	actor := int64(0)
	if ctx.IsSigned {
		actor = ctx.User.Id
	}
	name := owner.Name
	if owner.IsOrganization() {
		name = ".github"
	}
	current := func() (int64, error) {
		live, err := models.GetUserById(owner.Id)
		if errors.Is(err, models.ErrUserNotExist) {
			return 0, nil
		}
		if err != nil {
			return 0, err
		}
		if !strings.EqualFold(live.Name, owner.Name) || live.IsOrganization() != owner.IsOrganization() {
			return 0, nil
		}
		repo, err := models.GetRepositoryByName(owner.Id, name)
		if errors.Is(err, models.ErrRepoNotExist) {
			return 0, nil
		}
		if err != nil {
			return 0, err
		}
		access, err := models.RepositoryAccessFor(actor, repo.Id)
		if errors.Is(err, models.ErrRepoNotExist) || errors.Is(err, models.ErrForkLineage) || errors.Is(err, models.ErrInvalidCredential) {
			return 0, nil
		}
		if err != nil {
			return 0, err
		}
		if !access.Read {
			return 0, nil
		}
		return repo.Id, nil
	}
	// Resolve absence without requiring a healthy native service.
	id, err := current()
	if err != nil {
		return profile.Content{State: "unavailable"}, err
	}
	if id == 0 {
		return absent, nil
	}
	if provider == nil || provider.Service() == nil {
		return profile.Content{State: "unavailable"}, errors.New("profile README hosting unavailable")
	}
	service := provider.Service()
	readCtx, cancel := context.WithTimeout(ctx.Req.Context(), hg.BrowserCommandTimeout)
	var release func()
	defer func() {
		cancel()
		if release != nil {
			release()
		}
	}()
	resolve := func(c context.Context, actorID, ownerID int64, repositoryName string) (int64, string, error) {
		if err := c.Err(); err != nil {
			return 0, "", err
		}
		liveID, err := current()
		if err != nil || liveID == 0 {
			return 0, "", err
		}
		if liveID != id {
			return 0, "", nil
		}
		release, err = service.RegisterBrowserRead(actorID, id, cancel)
		if errors.Is(err, hgbridge.ErrDenied) || errors.Is(err, models.ErrRepoNotExist) {
			return 0, "", nil
		}
		if err != nil {
			return 0, "", err
		}
		storage, err := models.RepositoryStoragePath(service.RepositoryRoot(), id)
		if err != nil {
			return 0, "", err
		}
		return id, storage, nil
	}
	content, loadErr := profile.NewReadmeService(service.Runner(), resolve, profileContentURL).Load(readCtx, actor, owner.Id, owner.Name, owner.IsOrganization())
	if readCtx.Err() != nil {
		return absent, readCtx.Err()
	}
	liveID, err := current()
	if err != nil {
		return profile.Content{State: "unavailable"}, err
	}
	if liveID != id {
		return absent, nil
	}
	if readCtx.Err() != nil {
		return absent, readCtx.Err()
	}
	return content, loadErr
}

func profileContentURL(id int64, node hg.NodeID, p string, raw bool) (string, error) {
	if node == "" {
		if p != "" || raw {
			return "", hg.ErrNotFound
		}
	} else if _, err := hg.ParseNodeID(string(node)); err != nil {
		return "", err
	}
	repo, err := models.GetRepositoryById(id)
	if err != nil {
		return "", err
	}
	root, err := repo.RepoLink()
	if err != nil {
		return "", err
	}
	if node == "" {
		return root, nil
	}
	action := "src"
	if raw {
		action = "raw"
	}
	return HgPathURL(root, action, node, p), nil
}
