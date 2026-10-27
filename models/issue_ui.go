package models

import (
	"context"
	"strings"
)

// UIIssueQuery applies live repository authority before totals, filtering and paging.
// Global All retains the existing owned-repositories scope; personal filters may
// include any currently readable repository. No repository history is opened.
type UIIssueQuery struct {
	ActorID, RepositoryID, MilestoneID int64
	Global, Closed                     bool
	View, Labels, Sort                 string
	Page                               int
}
type UIIssuePage struct {
	Issues       []*Issue
	Repositories []*Repository
	Stats        IssueStats
	Total        int
	HasIssues    bool
}

func ListUIIssues(ctx context.Context, q UIIssueQuery) (UIIssuePage, error) {
	out := UIIssuePage{Issues: []*Issue{}, Repositories: []*Repository{}}
	if q.RepositoryID > 0 {
		access, err := RepositoryAccessFor(q.ActorID, q.RepositoryID)
		if err != nil {
			return out, err
		}
		if !access.Read {
			return out, ErrRepoNotExist
		}
	}
	if q.Page < 1 {
		q.Page = 1
	}
	if q.Page > 1000000 {
		q.Page = 1000000
	}
	repositories := map[int64]*Repository{}
	denied := map[int64]bool{}
	shown := map[int64]bool{}
	start := (q.Page - 1) * 20
	for offset := 0; ; offset += 100 {
		if err := ctx.Err(); err != nil {
			return out, err
		}
		var batch []*Issue
		s := x.Where("is_pull=?", false)
		if q.RepositoryID > 0 {
			s.And("repo_id=?", q.RepositoryID)
		}
		switch q.Sort {
		case "oldest":
			s.Asc("created", "id")
		case "recentupdate":
			s.Desc("updated", "id")
		case "leastupdate":
			s.Asc("updated", "id")
		case "mostcomment":
			s.Desc("num_comments", "id")
		case "leastcomment":
			s.Asc("num_comments", "id")
		case "priority":
			s.Desc("priority", "id")
		default:
			s.Desc("created", "id")
		}
		if err := s.Limit(100, offset).Find(&batch); err != nil {
			return out, err
		}
		for _, issue := range batch {
			if denied[issue.RepoId] {
				continue
			}
			r := repositories[issue.RepoId]
			if r == nil {
				access, err := RepositoryAccessFor(q.ActorID, issue.RepoId)
				if err == ErrRepoNotExist {
					denied[issue.RepoId] = true
					continue
				}
				if err != nil {
					return out, err
				}
				if !access.Read {
					denied[issue.RepoId] = true
					continue
				}
				r, err = GetRepositoryById(issue.RepoId)
				if err == ErrRepoNotExist {
					denied[issue.RepoId] = true
					continue
				}
				if err != nil {
					return out, err
				}
				if err = r.GetOwner(); err == ErrUserNotExist {
					denied[issue.RepoId] = true
					continue
				} else if err != nil {
					return out, err
				}
				repositories[r.Id] = r
			}
			if !q.Global || q.View != "all" || r.OwnerId == q.ActorID {
				out.HasIssues = true
			}
			if q.MilestoneID > 0 && issue.MilestoneId != q.MilestoneID {
				continue
			}
			labelsMatch := true
			for _, id := range strings.Split(q.Labels, ",") {
				if id != "" && !strings.Contains(issue.LabelIds, "$"+id+"|") {
					labelsMatch = false
				}
			}
			if !labelsMatch {
				continue
			}
			pair := new(IssueUser)
			if q.ActorID > 0 {
				if _, err := x.Where("issue_id=? AND uid=?", issue.Id, q.ActorID).Get(pair); err != nil {
					return out, err
				}
			}
			assigned := q.ActorID > 0 && issue.AssigneeId == q.ActorID
			created := q.ActorID > 0 && issue.PosterId == q.ActorID
			if issue.IsClosed == q.Closed {
				if !q.Global || r.OwnerId == q.ActorID {
					out.Stats.AllCount++
				}
				if assigned {
					out.Stats.AssignCount++
				}
				if created {
					out.Stats.CreateCount++
				}
				if pair.IsMentioned {
					out.Stats.MentionCount++
				}
			}
			if q.Global && q.View == "all" && r.OwnerId != q.ActorID {
				continue
			}
			if q.View == "assigned" && !assigned || q.View == "created_by" && !created || q.View == "mentioned" && !pair.IsMentioned {
				continue
			}
			if issue.IsClosed {
				out.Stats.ClosedCount++
			} else {
				out.Stats.OpenCount++
			}
			if issue.IsClosed != q.Closed {
				continue
			}
			if !shown[r.Id] {
				shown[r.Id] = true
				out.Repositories = append(out.Repositories, r)
			}
			if out.Total >= start && len(out.Issues) < 20 {
				issue.Repo = r
				issue.IsRead = pair.Id == 0 || pair.IsRead
				if err := issue.GetPoster(); err != nil {
					return out, err
				}
				if err := issue.GetLabels(); err != nil {
					return out, err
				}
				if err := issue.GetAssignee(); err != nil {
					return out, err
				}
				issue.LastActivity = issue.Created
				if issue.Updated.After(issue.LastActivity) {
					issue.LastActivity = issue.Updated
				}
				latest := new(Comment)
				if found, err := x.Where("issue_id=?", issue.Id).Desc("created", "id").Get(latest); err != nil {
					return out, err
				} else if found && latest.Created.After(issue.LastActivity) {
					issue.LastActivity = latest.Created
				}
				out.Issues = append(out.Issues, issue)
			}
			out.Total++
		}
		if len(batch) < 100 {
			break
		}
	}
	return out, nil
}
