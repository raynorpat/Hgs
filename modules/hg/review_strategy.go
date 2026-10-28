package hg

import "context"

func reviewMergeStrategy(s ReviewSnapshot, source, target Changeset) MergeStrategy {
	if s.Base == s.Source {
		return MergeAlreadyIntegrated
	}
	if s.Base == s.Target && source.Branch == target.Branch {
		return MergeFastForward
	}
	return MergeTwoParent
}

// ReviewMergeStrategy reads an owned snapshot only. It does not prepare a
// checkout, allocate an artifact, create changesets, or publish anything.
func (r *Runner) ReviewMergeStrategy(ctx context.Context, s ReviewSnapshot) (MergeStrategy, error) {
	ctx, cancel := context.WithTimeout(ctx, BrowserCommandTimeout)
	defer cancel()
	if _, err := loadReviewMetadata(s); err != nil {
		return "", err
	}
	if err := r.reviewCapability(ctx); err != nil {
		return "", err
	}
	bases, err := r.reviewBases(ctx, s.Path, s.Source, s.Target)
	if err != nil {
		return "", err
	}
	if len(bases) != 1 || bases[0] != s.Base {
		return "", ErrAmbiguousHead
	}
	source, err := r.reviewChangeset(ctx, s.Path, s.Source)
	if err != nil {
		return "", err
	}
	target, err := r.reviewChangeset(ctx, s.Path, s.Target)
	if err != nil {
		return "", err
	}
	return reviewMergeStrategy(s, source, target), nil
}
