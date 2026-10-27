package hg

import (
	"context"
	"strings"
	"testing"
)

func TestReviewPublicationStateResultInspection(t *testing.T) {
	validOther := `{"node":"` + string(baseNode) + `","rev":0,"p1node":"` + strings.Repeat("0", 40) + `","p2node":"` + strings.Repeat("0", 40) + `","author":"t","desc":"root","branch":"default","date":[0,0]}` + "\n"
	for _, tc := range []struct {
		name, output string
		wantError    bool
	}{
		{"command_failure", "EXIT", true},
		{"malformed_output", "{", true},
		{"wrong_node", validOther, true},
		{"multiple_nodes", validOther + validOther, true},
		{"missing_node", "", false},
		{"invalid_node", "", true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			r, path := newBrowseRunner(t, "")
			writeFakeFixture(t, r, "fake-review-result", tc.output)
			writeFakeFixture(t, r, "fake-log", `{"rev":0,"node":"`+string(baseNode)+`","branch":"default","bookmarks":["selected"],"tags":[]}`)
			selection := ReviewSelection{Ref: RefSelection{Kind: RefBookmark, Name: "selected"}}
			endpoint, err := r.InspectReviewEndpoint(context.Background(), path, selection)
			if err != nil || endpoint.Node != baseNode {
				t.Fatalf("independent reference inspection: %+v %v", endpoint, err)
			}
			q := PublishReviewRequest{TargetPath: path, Expected: endpoint, Prepared: PreparedMerge{Target: baseNode, Source: newerDefaultNode, Result: newerDefaultNode, Mode: "fast_forward", DestinationBranch: "default", Bookmark: "selected"}}
			if tc.name == "invalid_node" {
				q.Prepared.Result = "invalid"
			}
			imported, complete, err := r.ReviewPublicationState(context.Background(), q)
			if (err != nil) != tc.wantError || imported || complete {
				t.Fatalf("state=(%v,%v,%v), wantError=%v", imported, complete, err, tc.wantError)
			}
		})
	}
}
