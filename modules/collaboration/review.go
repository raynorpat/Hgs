package collaboration

import (
	"bytes"
	"context"
	"errors"
	"io"

	"github.com/gogits/gogs/models"
)

// InlineAnchor names a line of the base or source side of one immutable comparison.
type InlineAnchor struct {
	ComparisonID int64
	Path, Side   string
	Line         int
}

func (s *Service) AssignReviewers(ctx context.Context, actorID, pullID int64, reviewerIDs []int64) error {
	return models.AssignPullReviewers(ctx, actorID, pullID, reviewerIDs)
}

func (s *Service) ReviewPull(ctx context.Context, actorID, pullID, comparisonID int64, state models.ReviewState) error {
	return models.SubmitPullReview(ctx, actorID, pullID, comparisonID, state)
}

func (s *Service) DismissReview(ctx context.Context, actorID, pullID, reviewerID int64, reason string) error {
	return models.DismissPullReview(ctx, actorID, pullID, reviewerID, reason)
}

func (s *Service) ResolveThread(ctx context.Context, actorID, pullID, threadID int64, resolved bool) error {
	return models.ResolvePullThread(ctx, actorID, pullID, threadID, resolved)
}

// CommentPull stores a comment; an anchor must name an existing text line of
// the comparison's base or source file, read under the actor's current access.
func (s *Service) CommentPull(ctx context.Context, actorID, pullID int64, body string, anchor *InlineAnchor) (int64, error) {
	a, err := s.ValidatePullCommentAnchor(ctx, actorID, pullID, anchor)
	if err != nil {
		return 0, err
	}
	return models.AddPullComment(ctx, actorID, pullID, body, a)
}

// ValidatePullCommentAnchor performs the same current authorized immutable-file
// check for temporary recovery as for final comment submission.
func (s *Service) ValidatePullCommentAnchor(ctx context.Context, actorID, pullID int64, anchor *InlineAnchor) (*models.PullCommentAnchor, error) {
	var a *models.PullCommentAnchor
	if anchor != nil {
		if anchor.Side != "base" && anchor.Side != "source" || anchor.Line < 1 {
			return nil, models.ErrPullInvalid
		}
		var buf bytes.Buffer
		if err := s.ReadComparisonFile(ctx, actorID, pullID, anchor.ComparisonID, anchor.Side, anchor.Path, &limitedWriter{w: &buf, n: maxAnchorFile}); err != nil {
			if errors.Is(err, errAnchorFileTooLarge) || errors.Is(err, ErrComparisonUnavailable) {
				return nil, models.ErrPullInvalid
			}
			return nil, err
		}
		b := buf.Bytes()
		lines := bytes.Count(b, []byte{'\n'})
		if len(b) > 0 && b[len(b)-1] != '\n' {
			lines++
		}
		if bytes.IndexByte(b, 0) >= 0 || anchor.Line > lines {
			return nil, models.ErrPullInvalid
		}
		a = &models.PullCommentAnchor{ComparisonID: anchor.ComparisonID, Path: anchor.Path, Side: anchor.Side, Line: anchor.Line}
	}
	return a, nil
}

const maxAnchorFile = 4 << 20

var errAnchorFileTooLarge = errors.New("anchor file too large")

type limitedWriter struct {
	w io.Writer
	n int
}

func (l *limitedWriter) Write(p []byte) (int, error) {
	if len(p) > l.n {
		return 0, errAnchorFileTooLarge
	}
	l.n -= len(p)
	return l.w.Write(p)
}
