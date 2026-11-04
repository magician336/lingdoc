package workspacecore

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/Tencent/WeKnora/internal/types"
)

// This material reader models the trusted material port. Production raw-byte
// extraction and chapter source authorization have separate HTTP tests.
type syntheticAcademicMaterialReader struct {
	*testSourceReader
	onRead func(string)
}

func (r syntheticAcademicMaterialReader) ReadAcademicSnapshotMaterial(ctx context.Context, actor Actor, projectID, chapterID, sourceID string) (AcademicSnapshotMaterial, error) {
	source, err := r.ReadResearchSourceForChapter(ctx, actor, projectID, chapterID, sourceID)
	if err != nil {
		return AcademicSnapshotMaterial{}, err
	}
	raw := []byte(source.QuotedText)
	if r.onRead != nil {
		r.onRead(chapterID)
	}
	return AcademicSnapshotMaterial{Source: source, Raw: raw, SourceText: source.QuotedText, RawSHA256: sha256Hex(string(raw))}, nil
}

func TestAcademicConfirmationRequiresEveryReviewerChapterAndOldReplayStaysInvalid(t *testing.T) {
	ctx := context.Background()
	f, card, _ := academicSnapshotReadFixture(t, false)
	chapters, err := f.svc.ListChapters(ctx, f.owner, f.project.ID)
	if err != nil || len(chapters) < 2 {
		t.Fatalf("need two synthetic chapters: %v", err)
	}
	chapterA, chapterB := chapters[0].ID, chapters[1].ID
	err = f.svc.repository.Transaction(ctx, TransactionOptions{}, func(tx Transaction) error {
		current, old, e := loadStoredObject[EvidenceCard](tx, f.project.ID, EvidenceCardKind, card.ID)
		if e != nil {
			return e
		}
		current.Version++
		current.ScopeChapterIDs = sortedUniqueIDs([]string{chapterA, chapterB})
		card = current
		return storeStoredObject(tx, old, f.project.ID, EvidenceCardKind, card.ID, card.Version, card)
	})
	if err != nil {
		t.Fatal(err)
	}
	f.svc.agentRuntime.Sources = syntheticAcademicMaterialReader{testSourceReader: f.source}
	reviewer := Actor{TenantID: f.owner.TenantID, UserID: "synthetic-reviewer", Role: types.TenantRoleAdmin}
	seedTenantMember(t, f.svc, reviewer)
	setScopes := func(scopes []string, sourceAllowed bool) {
		t.Helper()
		p, e := f.svc.GetProject(ctx, f.owner, f.project.ID)
		if e != nil {
			t.Fatal(e)
		}
		members := make([]Member, 0, len(p.Members)+1)
		for _, m := range p.Members {
			if m.UserID != reviewer.UserID {
				members = append(members, m)
			}
		}
		roles := []string{"reviewer"}
		functionScopes := map[string][]string{"reviewer": scopes}
		if sourceAllowed {
			roles = append(roles, "researcher")
			functionScopes["researcher"] = []string{"asset:asset-1"}
		}
		members = append(members, Member{UserID: reviewer.UserID, Role: "collaborator", GovernanceRole: "member", FunctionRoles: roles, FunctionScopes: functionScopes, Status: "active"})
		if e = f.svc.repository.Transaction(ctx, TransactionOptions{}, func(tx Transaction) error { return tx.ReplaceMembers(p.ID, members) }); e != nil {
			t.Fatal(e)
		}
	}
	input := ConfirmationInput{VersionExpectation: f.expectation(card.Version), Confirmed: true, Reason: "synthetic review"}
	setScopes([]string{chapterA}, true)
	if body, _, _, e := f.svc.ConfirmEvidenceCard(ctx, reviewer, f.project.ID, card.ID, "multichapter-denied", input); e == nil || len(body) != 0 {
		t.Fatalf("reviewer without second chapter accepted: %v", e)
	}
	setScopes([]string{chapterA, chapterB}, true)
	reads := 0
	f.svc.agentRuntime.Sources = syntheticAcademicMaterialReader{testSourceReader: f.source, onRead: func(string) {
		reads++
		if reads == 2 {
			setScopes([]string{chapterA, chapterB}, false)
		}
	}}
	if body, _, _, e := f.svc.ConfirmEvidenceCard(ctx, reviewer, f.project.ID, card.ID, "multichapter-source-revoked", input); e == nil || len(body) != 0 {
		t.Fatalf("revoked research asset scope accepted: %v", e)
	}
	f.svc.agentRuntime.Sources = syntheticAcademicMaterialReader{testSourceReader: f.source}
	setScopes([]string{chapterA, chapterB}, true)
	raw, _, replay, err := f.svc.ConfirmEvidenceCard(ctx, reviewer, f.project.ID, card.ID, "multichapter-original", input)
	if err != nil || replay {
		t.Fatalf("authorized reviewer failed: %v", err)
	}
	var first EvidenceCard
	if err = json.Unmarshal(raw, &first); err != nil {
		t.Fatal(err)
	}
	setScopes([]string{chapterA}, true)
	if body, _, _, e := f.svc.ConfirmEvidenceCard(ctx, reviewer, f.project.ID, card.ID, "multichapter-original", input); e == nil || len(body) != 0 {
		t.Fatalf("replay after second chapter revoked: %v", e)
	}
	setScopes([]string{chapterA, chapterB}, true)
	if _, _, replay, e := f.svc.ConfirmEvidenceCard(ctx, reviewer, f.project.ID, card.ID, "multichapter-original", input); e != nil || !replay {
		t.Fatalf("restored reviewer replay: %v", e)
	}
	raw, _, _, err = f.svc.ReviewEvidenceCard(ctx, f.owner, f.project.ID, card.ID, "multichapter-needs-review", ReviewEvidenceCardInput{VersionExpectation: f.expectation(first.Version), Status: "NEEDS_REVIEW", Confirmed: true, Reason: "recheck"})
	if err != nil {
		t.Fatal(err)
	}
	var review EvidenceCard
	if err = json.Unmarshal(raw, &review); err != nil {
		t.Fatal(err)
	}
	newInput := ConfirmationInput{VersionExpectation: f.expectation(review.Version), Confirmed: true, Reason: "new human decision"}
	if _, _, _, err = f.svc.ConfirmEvidenceCard(ctx, reviewer, f.project.ID, card.ID, "multichapter-new-confirmation", newInput); err != nil {
		t.Fatal(err)
	}
	if body, _, _, e := f.svc.ConfirmEvidenceCard(ctx, reviewer, f.project.ID, card.ID, "multichapter-original", input); e == nil || len(body) != 0 {
		t.Fatalf("old key returned newer confirmation: %v", e)
	}
}
