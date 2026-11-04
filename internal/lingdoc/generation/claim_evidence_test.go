package generation

import (
	"context"
	"testing"

	"github.com/Tencent/WeKnora/internal/lingdoc/candidateadoption"
)

func TestGenerationExplicitEvidenceBindingsPublishOnlyExactDeclarations(t *testing.T) {
	for _, tc := range []struct {
		name   string
		mutate func(*Draft)
		want   Status
	}{
		{"valid", func(d *Draft) {}, StatusSucceeded},
		{"missing claims", func(d *Draft) { d.Claims = nil }, StatusFailed},
		{"wrong anchor", func(d *Draft) { d.Claims[0].References[0].AnchorID = "another" }, StatusFailed},
		{"uncovered text", func(d *Draft) { d.Claims[0].Text = "x [[source:source-1]]" }, StatusFailed},
	} {
		t.Run(tc.name, func(t *testing.T) {
			svc, repo := evidenceFixture()
			repo.run = Run{}
			svc.Evidence = &testEvidenceValidator{}
			run, err := svc.Start(context.Background(), Actor{TenantID: 1, UserID: "user-1"}, "project-1", "claim-bind-"+tc.name, evidenceStartRequest())
			if err != nil {
				t.Fatal(err)
			}
			repo.input.EvidenceConstraints[0].Anchors = []EvidenceAnchor{{ID: "anchor", Version: 1, SourceID: "source-1", Locator: "page 1", SelectionEnd: 3, QuotedTextHash: "digest"}}
			draft := Draft{BodyMarkdown: "合成事实 [[source:source-1]]", Sources: []Source{{ID: "source-1"}}, Claims: []candidateadoption.ClaimEvidence{{Text: "合成事实 [[source:source-1]]", References: []candidateadoption.ClaimEvidenceReference{{CardID: "card", CardVersion: 2, AnchorID: "anchor", AnchorVersion: 1, SourceID: "source-1", Locator: "model-forged"}}}}}
			tc.mutate(&draft)
			svc.Model = testModel{draft: draft}
			result, err := svc.Execute(context.Background(), run.ID)
			if err != nil || result.Status != tc.want {
				t.Fatalf("status=%+v err=%v", result, err)
			}
			if tc.want == StatusSucceeded {
				if len(repo.candidate.Claims) != 1 || repo.candidate.Claims[0].References[0].Locator != "page 1" {
					t.Fatalf("stored claims=%+v", repo.candidate.Claims)
				}
			} else if repo.candidate.ID != "" || repo.failure == nil || repo.failure.Code != "invalid_claim_evidence" {
				t.Fatalf("published invalid candidate %+v failure=%+v", repo.candidate, repo.failure)
			}
		})
	}
}
