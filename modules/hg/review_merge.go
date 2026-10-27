// Copyright 2026 The Gogs Authors. All rights reserved.
// Use of this source code is governed by a MIT-style
// license that can be found in the LICENSE file.

package hg

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"unicode/utf8"
)

func (r *Runner) reviewManifest(ctx context.Context, path string, node NodeID) ([]ManifestEntry, error) {
	var out bytes.Buffer
	if err := r.reviewRun(ctx, path, []string{"manifest", "-r", nodeRevset(node), "-v"}, &out); err != nil {
		return nil, err
	}
	return parseManifest(out.Bytes())
}

// Deliberately conservative: symlinks and subrepositories remain browsable but
// require local integration when a two-parent merge materializes files.
// Store-only modes do not inspect checkout representations or update subrepos.
func reviewCheckoutEntries(entries []ManifestEntry) error {
	seen := map[string]string{}
	for _, entry := range entries {
		if entry.Symlink || checkFile(entry.Path) != nil || !utf8.ValidString(entry.Path) {
			return fmt.Errorf("%w: unsupported file %q", ErrUnsafeCheckout, entry.Path)
		}
		components := strings.Split(entry.Path, "/")
		for i, part := range components {
			lower := strings.ToLower(part)
			if lower == ".hgsub" || lower == ".hgsubstate" || lower == ".hg" {
				return fmt.Errorf("%w: repository metadata path %q", ErrUnsafeCheckout, entry.Path)
			}
			if runtime.GOOS == "windows" {
				if strings.ContainsAny(part, "<>:\"|?*~\x00\r\n") || strings.TrimRight(part, ". ") != part {
					return fmt.Errorf("%w: Windows path %q", ErrUnsafeCheckout, entry.Path)
				}
				for _, c := range part {
					if c < 32 || c >= 127 {
						return fmt.Errorf("%w: Windows materialization requires printable ASCII paths", ErrUnsafeCheckout)
					}
				}
				stem := strings.TrimRight(strings.ToLower(strings.SplitN(part, ".", 2)[0]), " .")
				if stem == "con" || stem == "prn" || stem == "aux" || stem == "nul" || stem == "conin$" || stem == "conout$" || len(stem) == 4 && (strings.HasPrefix(stem, "com") || strings.HasPrefix(stem, "lpt")) && strings.ContainsRune("123456789", rune(stem[3])) {
					return fmt.Errorf("%w: Windows device path %q", ErrUnsafeCheckout, entry.Path)
				}
				if err := CheckPath(part); err != nil {
					return fmt.Errorf("%w: %v", ErrUnsafeCheckout, err)
				}
			}
			// Windows local-byte decoding/Unicode aliases and HFS/APFS normalization
			// require a stricter audited representation layer. Windows above and macOS
			// below conservatively accept ASCII materialization only.
			if runtime.GOOS == "darwin" {
				for _, c := range part {
					if c > 127 {
						return fmt.Errorf("%w: macOS Unicode materialization unsupported", ErrUnsafeCheckout)
					}
				}
			}
			if runtime.GOOS == "windows" || runtime.GOOS == "darwin" {
				prefix := strings.Join(components[:i+1], "/")
				key := strings.ToLower(prefix)
				if previous, ok := seen[key]; ok && previous != prefix {
					return fmt.Errorf("%w: case-colliding path %q", ErrUnsafeCheckout, entry.Path)
				}
				seen[key] = prefix
			}
		}
	}
	return nil
}

func (r *Runner) PrepareReviewMerge(ctx context.Context, s ReviewSnapshot, output, author, message string) (PreparedMerge, error) {
	ctx, cancel := r.reviewOperationContext(ctx)
	defer cancel()
	result := PreparedMerge{Source: s.Source, Target: s.Target}
	metadata, err := loadReviewMetadata(s)
	if err != nil {
		return result, err
	}
	if strings.TrimSpace(author) == "" || len(author) > 1024 || strings.ContainsAny(author, "\x00\r\n") || strings.TrimSpace(message) == "" || len(message) > 16384 || strings.ContainsRune(message, 0) {
		return result, fmt.Errorf("hg: invalid merge metadata")
	}
	if err = r.reviewCapability(ctx); err != nil {
		return result, err
	}
	bases, err := r.reviewBases(ctx, s.Path, s.Source, s.Target)
	if err != nil {
		return result, err
	}
	if len(bases) != 1 || bases[0] != s.Base {
		return result, ErrAmbiguousHead
	}
	target, err := r.reviewChangeset(ctx, s.Path, s.Target)
	if err != nil {
		return result, err
	}
	source, err := r.reviewChangeset(ctx, s.Path, s.Source)
	if err != nil {
		return result, err
	}
	result.DestinationBranch = target.Branch
	if metadata.TargetSelection.Ref.Kind == RefBookmark {
		result.Bookmark = metadata.TargetSelection.Ref.Name
	}
	switch {
	case s.Base == s.Source:
		result.Mode = "already_integrated"
		result.Result = s.Target
	case s.Base == s.Target && source.Branch == target.Branch:
		result.Mode = "fast_forward"
		result.Result = s.Source
	default:
		result.Mode = "two_parent"
	}
	if result.Mode == "two_parent" {
		var checkoutEntries []ManifestEntry
		for _, node := range []NodeID{s.Source, s.Target, s.Base} {
			entries, err := r.reviewManifest(ctx, s.Path, node)
			if err != nil {
				return result, err
			}
			checkoutEntries = append(checkoutEntries, entries...)
		}
		if err = reviewCheckoutEntries(checkoutEntries); err != nil {
			return result, err
		}
	}
	result.Path, err = newReviewArtifact(output, s.Path)
	if err != nil {
		return result, err
	}
	keep := false
	defer func() {
		if !keep {
			os.RemoveAll(filepath.Dir(result.Path))
		}
	}()
	if err = r.reviewClone(ctx, s.Path, s.Target, result.Path); err != nil {
		return result, err
	}
	if err = r.reviewRun(ctx, result.Path, []string{"pull", "-r", string(s.Source), "--", s.Path}, nil); err != nil {
		return result, err
	}
	if result.Mode == "two_parent" {
		// Only this branch materializes files; neither live store is updated.
		if err = r.reviewRun(ctx, result.Path, []string{"update", "--clean", "-r", string(s.Target)}, nil); err != nil {
			return result, err
		}
		if err = r.reviewRun(ctx, result.Path, []string{"merge", "--tool", ":merge", "-r", string(s.Source)}, nil); err != nil {
			// An unresolved-file listing distinguishes a real conflict from a timeout,
			// command failure or resource limit. Never label those merge success.
			var unresolved bytes.Buffer
			if probe := r.reviewRun(ctx, result.Path, []string{"resolve", "--list", "-T", "{mergestatus}\n"}, &unresolved); probe == nil && strings.Contains(unresolved.String(), "U") {
				return result, ErrMergeConflict
			}
			return result, err
		}
		if err = r.reviewRun(ctx, result.Path, []string{"commit", "-u", author, "-m", message}, nil); err != nil {
			return result, err
		}
		var out bytes.Buffer
		if err = r.reviewRun(ctx, result.Path, []string{"log", "-r", ".", "-T", "{node}"}, &out); err != nil {
			return result, err
		}
		result.Result, err = ParseNodeID(out.String())
		if err != nil {
			return result, err
		}
		cs, err := r.reviewChangeset(ctx, result.Path, result.Result)
		if err != nil {
			return result, err
		}
		if len(cs.Parents) != 2 || cs.Parents[0] != s.Target || cs.Parents[1] != s.Source || cs.Branch != target.Branch {
			return result, fmt.Errorf("hg: native merge violated ordered parents or destination branch")
		}
	}
	// Publication is a separate guarded operation. This artifact carries the
	// selected bookmark identity but never moves a bookmark in a public store.
	if err = writeReviewMetadata(result.Path, metadata); err != nil {
		return result, err
	}
	keep = true
	return result, nil
}
