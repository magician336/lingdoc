package workspace

import (
	"context"
	"testing"

	"github.com/Tencent/WeKnora/internal/lingdoc/candidateadoption"
	"github.com/Tencent/WeKnora/internal/lingdoc/writingaction"
	"github.com/stretchr/testify/require"
)

type revokingReviewModel struct{ revoke func() }

func (*revokingReviewModel) RunMode() string { return "mock" }
func (m *revokingReviewModel) RewriteSelected(_ context.Context, _ Actor, prompt SelectedRewritePrompt) (SelectedRewriteOutput, error) {
	statement := prompt.Sources[0].QuotedText
	m.revoke()
	return SelectedRewriteOutput{SourceIDs: []string{"existing"}, ReviewItems: []candidateadoption.ReviewItem{{Statement: statement}}, RunMode: "mock"}, nil
}

func TestReviewRejectsModelOutputWhenSourceOrAcademicEvidenceIsRevoked(t *testing.T) {
	for _, scenario := range []string{"source access revoked", "project permission revoked", "academic retraction"} {
		t.Run(scenario, func(t *testing.T) {
			service, workspace, sources, _ := reviewFixture(t)
			request := reviewRequest()
			selector := &academicSnapshotSelectorStub{selected: snapshotSelectionFixture()}
			var revoke func()
			if scenario == "academic retraction" {
				service.selector = selector
				request.Context = &writingaction.ContextSelection{Include: map[writingaction.ContextItem][]writingaction.ContextEntry{
					writingaction.ContextCards: {{ID: "card", Version: 2}},
				}}
				// A real retraction makes the core selector reject the card.
				revoke = func() { selector.denied = true }
			} else if scenario == "project permission revoked" {
				revoke = func() { workspace.denied = true }
			} else {
				revoke = func() { sources.denied = true }
			}
			service.model = &revokingReviewModel{revoke: revoke}
			report, err := service.CreateReviewReport(context.Background(), reviewActor(), "project", "chapter", "review-revoked-during-model", request)
			require.ErrorIs(t, err, ErrReviewNotAuthorized)
			require.Empty(t, report.ReportID)
			require.Empty(t, report.Findings)
			var count int64
			require.NoError(t, service.reviewStore().db.Model(&reviewReportRow{}).Count(&count).Error)
			require.Zero(t, count, "revoked model output must not be persisted")
		})
	}
}
