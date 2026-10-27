// Copyright 2026 The Gogs Authors. All rights reserved.
// Use of this source code is governed by a MIT-style
// license that can be found in the LICENSE file.

package hg

import (
	"errors"
	"runtime"
	"testing"
)

func TestReviewCheckoutPathPreflight(t *testing.T) {
	for _, path := range []string{"../escape", ".hg/store/data", "dir/.HG/hgrc", ".hgsub", "dir/.hgsubstate", "bad\\path", "\xff"} {
		t.Run(path, func(t *testing.T) {
			if err := reviewCheckoutEntries([]ManifestEntry{{Path: path}}); !errors.Is(err, ErrUnsafeCheckout) {
				t.Fatalf("unsafe path accepted: %v", err)
			}
		})
	}
	if err := reviewCheckoutEntries([]ManifestEntry{{Path: "link", Symlink: true}}); !errors.Is(err, ErrUnsafeCheckout) {
		t.Fatalf("symlink accepted: %v", err)
	}
	if err := reviewCheckoutEntries([]ManifestEntry{{Path: "run.sh", Executable: true}}); err != nil {
		t.Fatalf("ordinary executable content need not be run: %v", err)
	}
	if runtime.GOOS == "windows" {
		for _, path := range []string{"CON", "d/aux.txt", "a:stream", "space ", "dot.", "d/COM1.txt", "COM¹.txt", "lpt²", "HG~1/hgrc", "CON .txt", "café", "control\x7f"} {
			t.Run(path, func(t *testing.T) {
				if err := reviewCheckoutEntries([]ManifestEntry{{Path: path}}); !errors.Is(err, ErrUnsafeCheckout) {
					t.Fatalf("unsupported Windows path accepted: %v", err)
				}
			})
		}
		if err := reviewCheckoutEntries([]ManifestEntry{{Path: "D/a"}, {Path: "d/b"}}); !errors.Is(err, ErrUnsafeCheckout) {
			t.Fatalf("colliding directory accepted: %v", err)
		}
	}
}
