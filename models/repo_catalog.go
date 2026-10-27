package models

import (
	"context"
	"strings"
)

// CatalogQuery filters repository/owner names, never source code.
type CatalogQuery struct {
	Text, Visibility, Scope string
	OwnerID                 int64
	Page, PageSize          int
}
type CatalogPage struct {
	Repositories          []*Repository
	Total, Page, PageSize int
}

// NormalizeCatalogQuery bounds untrusted input at the model boundary as well as routes.
func NormalizeCatalogQuery(q CatalogQuery) CatalogQuery {
	text := []rune(strings.TrimSpace(q.Text))
	if len(text) > 200 {
		text = text[:200]
	}
	q.Text = string(text)
	if q.Visibility != "public" && q.Visibility != "private" {
		q.Visibility = "all"
	}
	if q.Scope != "mine" {
		q.Scope = "accessible"
	}
	if q.Page < 1 {
		q.Page = 1
	}
	if q.Page > 1000000 {
		q.Page = 1000000
	}
	if q.PageSize < 1 || q.PageSize > 100 {
		q.PageSize = 30
	}
	return q
}

// ListAccessibleRepositories applies the shared live read policy before counting
// or paging. Bounded metadata batches keep memory bounded without duplicating the
// policy in a second SQL authorization implementation. No Hg storage is opened.
func ListAccessibleRepositories(ctx context.Context, actorID int64, q CatalogQuery) (CatalogPage, error) {
	q = NormalizeCatalogQuery(q)
	page := CatalogPage{Page: q.Page, PageSize: q.PageSize, Repositories: []*Repository{}}
	if err := ctx.Err(); err != nil {
		return page, err
	}
	if actorID == 0 {
		if q.Scope == "mine" {
			return page, nil
		}
		q.Visibility = "public"
	} else if _, err := enabledAccount(actorID); err == ErrInvalidCredential {
		return page, nil
	} else if err != nil {
		return page, err
	}
	start := (q.Page - 1) * q.PageSize
	needle := strings.ToLower(q.Text)
	for offset := 0; ; offset += 100 {
		var batch []*Repository
		session := x.Where("state=?", RepositoryReady)
		if q.OwnerID > 0 {
			session.And("owner_id=?", q.OwnerID)
		}
		if q.Scope == "mine" {
			session.And("owner_id=?", actorID)
		}
		if q.Visibility != "all" {
			session.And("is_private=?", q.Visibility == "private")
		}
		err := session.Desc("updated", "id").Limit(100, offset).Find(&batch)
		if err != nil {
			return page, err
		}
		for _, repo := range batch {
			if err := ctx.Err(); err != nil {
				return page, err
			}
			access, err := RepositoryAccessFor(actorID, repo.Id)
			if err == ErrRepoNotExist {
				continue
			}
			if err != nil {
				return page, err
			}
			if !access.Read {
				continue
			}
			if err = repo.GetOwner(); err == ErrUserNotExist {
				continue
			} else if err != nil {
				return page, err
			}
			if needle != "" && !strings.Contains(strings.ToLower(repo.Name), needle) && !strings.Contains(strings.ToLower(repo.Owner.Name), needle) {
				continue
			}
			if page.Total >= start && len(page.Repositories) < q.PageSize {
				page.Repositories = append(page.Repositories, repo)
			}
			page.Total++
		}
		if len(batch) < 100 {
			break
		}
	}
	return page, nil
}
