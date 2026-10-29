//go:build integration

package integration

import "testing"

func TestMozillaSubsetIdentity(t *testing.T) {
	id := mozillaIdentity{URL: mozillaURL}
	classifyMozillaIdentity(&id, true, false)
	if id.URL != "" || id.UpstreamReference != mozillaURL || id.SourceClassification != "transformed-subset" || id.VerificationState != "TRANSFORMED_SUBSET_LOCAL_INTEGRITY_VERIFIED" || id.IntegrityVerification != "local-hg-verify" {
		t.Fatalf("ambiguous subset provenance %+v", id)
	}
	classifyMozillaIdentity(&id, true, true)
	if id.VerificationState != mozillaSourceUnverified || id.IntegrityVerification != "unverified" {
		t.Fatalf("unverified subset promoted %+v", id)
	}
	full := mozillaIdentity{URL: mozillaURL}
	classifyMozillaIdentity(&full, false, false)
	if full.URL != mozillaURL || full.SourceClassification != "upstream-clone" || full.VerificationState != "VERIFIED" {
		t.Fatalf("upstream identity changed %+v", full)
	}
}
