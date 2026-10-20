package workspacecore

import (
	"context"
	"encoding/json"
	"errors"
	"reflect"
	"slices"
	"strconv"
	"strings"
	"testing"
	"time"
)

func r25EvidenceCard(t *testing.T, f g2Fixture, id string) EvidenceCard {
	t.Helper()
	var card EvidenceCard
	if err := f.svc.repository.Transaction(context.Background(), TransactionOptions{ReadOnly: true}, func(tx Transaction) error {
		var err error
		card, _, err = g2Load[EvidenceCard](tx, f.project.ID, G2CardKind, id)
		return err
	}); err != nil {
		t.Fatal(err)
	}
	return card
}

func r25SemanticCounts(t *testing.T, f g2Fixture) map[string]int64 {
	t.Helper()
	out := map[string]int64{}
	for _, table := range []string{"lingdoc_g2_objects", "lingdoc_g2_object_versions", "lingdoc_g3_records", "lingdoc_project_audits", "lingdoc_operations"} {
		var n int64
		if err := f.svc.testDB().Table(table).Count(&n).Error; err != nil {
			t.Fatal(err)
		}
		out[table] = n
	}
	return out
}

func r25ConfirmedEvidenceFixture(t *testing.T) (g2Fixture, ResearchClaim, EvidenceCard, SourceAnchor) {
	t.Helper()
	f := newG2Fixture(t, false)
	g2SeedNativeEvidence(t, f)
	a := f.anchor(t, "source-1", "r25-original-anchor")
	b := f.anchor(t, "source-2", "r25-replacement-anchor")
	claim := f.claim(t)
	card := f.card(t, []SourceAnchor{a}, &claim, "r25-original-card", "supports")
	raw, _, _, err := f.svc.ConfirmEvidenceCard(context.Background(), f.owner, f.project.ID, card.ID, "r25-card-confirm", G2ConfirmedInput{G2Expectation: f.expectation(card.Version), Confirmed: true, Reason: "合成原文人工核对，非科学证明"})
	return f, claim, g2Decode[EvidenceCard](t, raw, err), b
}

func TestFormalGuardKeepsUnconfirmedCollectionEditable(t *testing.T) {
	f := newG2Fixture(t, false)
	a := f.anchor(t, "source-1", "r25-candidate-anchor")
	claim := f.claim(t)
	card := f.card(t, []SourceAnchor{a}, &claim, "r25-candidate-card", "supports")
	// Existing automatically confirmed dependency edges are deliberately present.
	// They are not evidence that the collected card or its claim is confirmed.
	raw, _, _, err := f.svc.SaveResearchClaim(context.Background(), f.owner, f.project.ID, "r25-edit-draft-claim", SaveResearchClaimInput{G2Expectation: f.expectation(claim.Version), ClaimID: claim.ID, Text: "普通收集阶段修订主张", Scope: claim.Scope, Status: "active", Reason: "尚未进入正式依据", Confirmed: true})
	claim = g2Decode[ResearchClaim](t, raw, err)
	card = r25EvidenceCard(t, f, card.ID)
	raw, _, _, err = f.svc.SaveEvidenceCard(context.Background(), f.owner, f.project.ID, "r25-edit-draft-card", SaveEvidenceCardInput{G2Expectation: f.expectation(card.Version), CardID: card.ID, ClaimRef: &G2VersionRef{ID: claim.ID, Version: claim.Version}, AnchorRefs: []G2VersionRef{{ID: a.ID, Version: a.Version}}, SupportType: "refutes", Scope: "未确认候选范围", ScopeChapterIDs: []string{f.chapter.ID}, Confirmed: true})
	card = g2Decode[EvidenceCard](t, raw, err)
	p, err := f.svc.GetProject(context.Background(), f.owner, f.project.ID)
	if err != nil {
		t.Fatal(err)
	}
	if claim.Version != 2 || card.Status != "CANDIDATE" || card.Confirmation != nil || p.CurrentContextRevision != f.project.CurrentContextRevision {
		t.Fatal("ordinary collection became a formal change or auto confirmation")
	}
}

func TestFormalClaimAndCardRejectDirectReplacement(t *testing.T) {
	f, claim, card, anchor := r25ConfirmedEvidenceFixture(t)
	before := r25SemanticCounts(t, f)
	_, _, _, err := f.svc.SaveResearchClaim(context.Background(), f.owner, f.project.ID, "r25-reject-formal-claim", SaveResearchClaimInput{G2Expectation: f.expectation(claim.Version), ClaimID: claim.ID, Text: "禁止绕过正式基线的主张替换", Scope: claim.Scope, Status: "active", Reason: "反例", Confirmed: true})
	if !errors.Is(err, ErrInvalidState) || !strings.Contains(err.Error(), "ChangeSet") {
		t.Fatalf("formal claim bypass: %v", err)
	}
	_, _, _, err = f.svc.SaveEvidenceCard(context.Background(), f.owner, f.project.ID, "r25-reject-formal-card", SaveEvidenceCardInput{G2Expectation: f.expectation(card.Version), CardID: card.ID, ClaimRef: card.ClaimRef, AnchorRefs: []G2VersionRef{{ID: anchor.ID, Version: anchor.Version}}, SupportType: "refutes", Scope: card.Scope, ScopeChapterIDs: card.ScopeChapterIDs, Confirmed: true})
	if !errors.Is(err, ErrInvalidState) {
		t.Fatalf("formal card bypass: %v", err)
	}
	if !reflect.DeepEqual(before, r25SemanticCounts(t, f)) {
		t.Fatal("rejected direct saves left domain/history/audit/operation writes")
	}
	if got := r25EvidenceCard(t, f, card.ID); got.Version != card.Version || got.Status != "CONFIRMED" || got.Confirmation == nil {
		t.Fatal("direct rejection invalidated formal card")
	}
	if err := f.svc.repository.Transaction(context.Background(), TransactionOptions{ReadOnly: true}, func(tx Transaction) error {
		got, _, err := g2Load[ResearchClaim](tx, f.project.ID, G2ClaimKind, claim.ID)
		if err == nil && (got.Version != claim.Version || got.Text != claim.Text) {
			t.Fatal("direct rejection changed formal claim")
		}
		return err
	}); err != nil {
		t.Fatal(err)
	}
	p, err := f.svc.GetProject(context.Background(), f.owner, f.project.ID)
	if err != nil || p.CurrentContextRevision != f.project.CurrentContextRevision {
		t.Fatalf("rejected change advanced context: %v", err)
	}
}

func TestFormalClaimGuardRecognizesRouteAndConfirmedChapter(t *testing.T) {
	for _, consumer := range []string{"ready_route", "draft_route", "confirmed_chapter"} {
		t.Run(consumer, func(t *testing.T) {
			f := newG2Fixture(t, false)
			claim := f.claim(t)
			ref := ObjectVersionRef{Kind: "ResearchClaim", ID: claim.ID, Version: strconv.FormatInt(claim.Version, 10)}
			if consumer == "ready_route" || consumer == "draft_route" {
				status := "ready"
				if consumer == "draft_route" {
					status = "planned"
				}
				c := semanticDraft(t, f.svc, f.owner, f.project, "r25-formal-route", SemanticDelta{Kind: "RouteNode", TargetID: "r25-route", ExpectedVersion: 0, ProposedRoute: &RouteNode{Name: "合成路线", Status: status, Inputs: []ObjectVersionRef{ref}}})
				if consumer == "ready_route" {
					semanticApply(t, f.svc, f.owner, f.project, c, "r25-formal-route")
				}
			} else {
				chapter := extendedChapter(t, f.svc, f.owner, f.project)
				if err := f.svc.testDB().Exec("UPDATE lingdoc_chapter_versions SET confirmation_valid = ? WHERE project_id = ? AND id = ?", true, f.project.ID, *chapter.CurrentVersionID).Error; err != nil {
					t.Fatal(err)
				}
				// The version flag alone is not a formal confirmation. Production
				// chapterView also requires a matching, valid confirmation record.
				if err := f.svc.repository.Transaction(context.Background(), TransactionOptions{ReadOnly: true}, func(tx Transaction) error {
					current, err := tx.Chapter(f.project.ID, chapter.ID)
					if err == nil && current.ConfirmationValid {
						t.Fatal("fixture version bit alone became a formal confirmation")
					}
					return err
				}); err != nil {
					t.Fatal(err)
				}
				confirmationID := "r25-formal-chapter-confirmation"
				confirmationTime := time.Now().UTC()
				details, err := json.Marshal(map[string]any{
					"id": confirmationID, "chapter_id": chapter.ID,
					"chapter_version_id": *chapter.CurrentVersionID,
					"spec_revision":      f.project.SpecRevision, "template_version": f.project.TemplateVersion,
					"actor_user_id": f.owner.UserID, "created_at": confirmationTime, "valid": true,
					"asset_versions": []any{}, "review_decisions": []any{},
				})
				if err != nil {
					t.Fatal(err)
				}
				if err := f.svc.testDB().Create(&chapterConfirmationRow{ID: confirmationID, ChapterID: chapter.ID, ChapterVersionID: *chapter.CurrentVersionID, Valid: true, DetailsJSON: string(details), CreatedAt: confirmationTime}).Error; err != nil {
					t.Fatal(err)
				}
				confirmation, err := f.svc.GetCurrentConfirmation(context.Background(), f.owner, f.project.ID, chapter.ID)
				if err != nil || string(confirmation) == "null" {
					t.Fatalf("fixture lacks a real current confirmation: %s %v", confirmation, err)
				}
				target := ObjectVersionRef{Kind: "ChapterVersion", ID: chapter.ID, Version: *chapter.CurrentVersionID}
				raw, _, _, err := f.svc.CreateDependencyEdge(context.Background(), f.owner, f.project.ID, "r25-formal-chapter-link", DependencyEdgeInput{ExpectedContextRevision: f.project.CurrentContextRevision, Source: ref, Target: target, Action: "REVIEW_REQUIRED", Relation: "actual_claim_reference", Origin: "user", Reason: "合成已确认章节的真实依赖"})
				edge := g2Decode[DependencyEdge](t, raw, err)
				if edge.Status != "confirmed" || edge.Source != ref || edge.Target != target {
					t.Fatalf("fixture lacks an exact confirmed dependency: %+v", edge)
				}
				if err := f.svc.repository.Transaction(context.Background(), TransactionOptions{ReadOnly: true}, func(tx Transaction) error {
					current, err := currentImpactObject(tx, f.project, target)
					if err == nil && (!current.Confirmed || current.Ref != target) {
						t.Fatalf("fixture is not a current confirmed chapter consumer: %+v", current)
					}
					return err
				}); err != nil {
					t.Fatal(err)
				}
			}
			var err error
			f.project, err = f.svc.GetProject(context.Background(), f.owner, f.project.ID)
			if err != nil {
				t.Fatal(err)
			}
			before := r25SemanticCounts(t, f)
			_, _, _, err = f.svc.SaveResearchClaim(context.Background(), f.owner, f.project.ID, "r25-formal-use-reject", SaveResearchClaimInput{G2Expectation: f.expectation(claim.Version), ClaimID: claim.ID, Text: "直接改已正式使用主张", Scope: claim.Scope, Status: "active", Reason: "反例", Confirmed: true})
			if consumer == "draft_route" {
				if err != nil {
					t.Fatalf("unapplied route proposal blocked collection: %v", err)
				}
			} else {
				if !errors.Is(err, ErrFormalChangeRequired) {
					t.Fatalf("formal consumer bypass: %v", err)
				}
				if !reflect.DeepEqual(before, r25SemanticCounts(t, f)) {
					t.Fatal("rejected formal edit left domain/history/audit/operation writes")
				}
				if err := f.svc.repository.Transaction(context.Background(), TransactionOptions{ReadOnly: true}, func(tx Transaction) error {
					current, _, err := g2Load[ResearchClaim](tx, f.project.ID, G2ClaimKind, claim.ID)
					if err == nil && !reflect.DeepEqual(current, claim) {
						t.Fatalf("rejected formal edit changed claim: %+v", current)
					}
					return err
				}); err != nil {
					t.Fatal(err)
				}
			}
		})
	}
}

func r25CardDelta(card EvidenceCard, anchor SourceAnchor) SemanticDelta {
	input := cardSemanticInput(card)
	input.AnchorRefs = []G2VersionRef{{ID: anchor.ID, Version: anchor.Version}}
	input.SupportType, input.Scope, input.Limitations = "refutes", "变更后的证据范围", "新的限制，不能继承旧确认"
	return SemanticDelta{Kind: "EvidenceCard", TargetID: card.ID, ExpectedVersion: card.Version, BeforeHash: "forged", SourceIDs: []string{"forged-source"}, ProposedCard: &input}
}

func TestEvidenceCardDraftApplyAndInverseInvalidateChapter(t *testing.T) {
	f, _, card, replacement := r25ConfirmedEvidenceFixture(t)
	chapter := extendedChapter(t, f.svc, f.owner, f.project)
	if err := f.svc.testDB().Exec("UPDATE lingdoc_chapter_versions SET confirmation_valid = ? WHERE project_id = ? AND id = ?", true, f.project.ID, *chapter.CurrentVersionID).Error; err != nil {
		t.Fatal(err)
	}
	if _, _, _, err := f.svc.CreateDependencyEdge(context.Background(), f.owner, f.project.ID, "r25-card-chapter-link", DependencyEdgeInput{ExpectedContextRevision: f.project.CurrentContextRevision, Source: ObjectVersionRef{Kind: "EvidenceCard", ID: card.ID, Version: strconv.FormatInt(card.Version, 10)}, Target: ObjectVersionRef{Kind: "ChapterVersion", ID: chapter.ID, Version: *chapter.CurrentVersionID}, Action: "REVIEW_REQUIRED", Relation: "actual_card_reference", Origin: "user", Reason: "实际被已确认章节使用"}); err != nil {
		t.Fatal(err)
	}
	c := semanticDraft(t, f.svc, f.owner, f.project, "r25-card-semantic-draft", r25CardDelta(card, replacement))
	if c.SemanticDeltas[0].BeforeHash == "forged" || !slices.Equal(c.SemanticDeltas[0].SourceIDs, []string{"source-1", "source-2"}) {
		t.Fatal("source union or server before pin missing")
	}
	if !slices.ContainsFunc(f.policy.calls, func(call fakeSourceCall) bool {
		return call.projectID == f.project.ID && call.actorID == f.owner.UserID && slices.Contains(call.sourceIDs, "source-1") && slices.Contains(call.sourceIDs, "source-2") && !slices.Contains(call.sourceIDs, "forged-source")
	}) {
		t.Fatal("real service source policy did not receive both old and replacement sources under current actor/project")
	}
	if got := r25EvidenceCard(t, f, card.ID); got.Version != card.Version || got.Confirmation == nil {
		t.Fatal("draft changed formal evidence")
	}
	semanticApply(t, f.svc, f.owner, f.project, c, "r25-card-semantic")
	p, err := f.svc.GetProject(context.Background(), f.owner, f.project.ID)
	if err != nil {
		t.Fatal(err)
	}
	got := r25EvidenceCard(t, f, card.ID)
	if got.Version != card.Version+1 || got.Status != "CANDIDATE" || got.Confirmation != nil || got.Basis.ContextRevision != p.CurrentContextRevision || got.SupportType != "refutes" || !slices.Equal(got.SourceIDs, []string{"source-2"}) || p.CurrentContextRevision != f.project.CurrentContextRevision+1 || p.SpecRevision != f.project.SpecRevision {
		t.Fatalf("wrong formal card transition: %+v", got)
	}
	readback, err := f.svc.ListEvidenceCards(context.Background(), f.owner, p.ID)
	if err != nil {
		t.Fatal(err)
	}
	if !slices.ContainsFunc(readback, func(item EvidenceCard) bool {
		return item.ID == got.ID && item.Version == got.Version && item.SupportType == "refutes" && item.Status == "CANDIDATE" && item.Confirmation == nil
	}) {
		t.Fatal("service readback lost the applied candidate semantics")
	}
	audits, err := f.svc.ListAuditEvents(context.Background(), f.owner, p.ID)
	if err != nil {
		t.Fatal(err)
	}
	foundAudit := false
	for _, event := range audits {
		if event.Action != "g2.evidence.semantic_delta" || event.Target != card.ID {
			continue
		}
		var detail struct {
			Previous int64 `json:"previous_version"`
			Version  int64 `json:"version"`
			Context  int64 `json:"context_revision"`
		}
		body, err := json.Marshal(event.Details)
		if err != nil {
			t.Fatal(err)
		}
		if err := json.Unmarshal(body, &detail); err != nil {
			t.Fatal(err)
		}
		foundAudit = event.Reason == c.Reason && detail.Previous == card.Version && detail.Version == got.Version && detail.Context == p.CurrentContextRevision
	}
	if !foundAudit {
		t.Fatal("applied exact card version/context/reason missing from persisted audit readback")
	}
	f.project = p
	counts := r25SemanticCounts(t, f)
	_, _, _, directErr := f.svc.SaveEvidenceCard(context.Background(), f.owner, p.ID, "r25-no-post-apply-bypass", SaveEvidenceCardInput{G2Expectation: f.expectation(got.Version), CardID: got.ID, ClaimRef: got.ClaimRef, AnchorRefs: got.AnchorRefs, SupportType: "supports", Scope: got.Scope, ScopeChapterIDs: got.ScopeChapterIDs, Confirmed: true})
	if !errors.Is(directErr, ErrInvalidState) || !reflect.DeepEqual(counts, r25SemanticCounts(t, f)) {
		t.Fatalf("applied CANDIDATE semantic identity lost its formal-change guard: %v", directErr)
	}
	chapters, err := f.svc.ListChapters(context.Background(), f.owner, f.project.ID)
	if err != nil {
		t.Fatal(err)
	}
	for _, item := range chapters {
		if item.ID == chapter.ID && item.ConfirmationValid {
			t.Fatal("actual dependent chapter remained confirmed")
		}
	}
	if err := f.svc.repository.Transaction(context.Background(), TransactionOptions{ReadOnly: true}, func(tx Transaction) error {
		port, _ := g2Storage(tx)
		row, err := port.G2ObjectVersion(p.ID, G2CardKind, card.ID, card.Version)
		if err != nil {
			return err
		}
		var history EvidenceCard
		if err := json.Unmarshal(row.Payload, &history); err != nil {
			return err
		}
		if history.Status != "CONFIRMED" || history.Confirmation == nil || history.SupportType != "supports" {
			t.Fatal("historical confirmation rewritten")
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	// Normal source confirmation adds a lifecycle version after semantic Apply.
	// It must not make an unchanged relation impossible to reverse.
	raw, _, _, err := f.svc.ConfirmEvidenceCard(context.Background(), f.owner, p.ID, got.ID, "r25-card-reconfirm", G2ConfirmedInput{G2Expectation: f.expectation(got.Version), Confirmed: true, Reason: "按修改后的关系与新原文重新核对"})
	confirmed := g2Decode[EvidenceCard](t, raw, err)
	if confirmed.Version != got.Version+1 || confirmed.Status != "CONFIRMED" || confirmed.Confirmation == nil || confirmed.Confirmation.ContextRevision != p.CurrentContextRevision {
		t.Fatal("normal post-apply card confirmation failed")
	}
	raw, _, _, err = f.svc.ReverseChangeSet(context.Background(), f.owner, p.ID, c.ID, "r25-card-inverse-draft")
	inverse := g2Decode[ChangeSet](t, raw, err)
	if inverse.Status != "draft" || inverse.SemanticDeltas[0].ExpectedVersion != confirmed.Version || inverse.SemanticDeltas[0].ProposedCard.SupportType != "supports" {
		t.Fatal("inverse did not propose original meaning")
	}
	semanticApply(t, f.svc, f.owner, p, inverse, "r25-card-inverse")
	got = r25EvidenceCard(t, f, card.ID)
	if got.Version != card.Version+3 || got.SupportType != "supports" || got.Status != "CANDIDATE" || got.Confirmation != nil {
		t.Fatal("inverse restored old confirmation or rewound history")
	}
}

func TestEvidenceCardApplyRechecksSourcesAndRollsBack(t *testing.T) {
	for _, failure := range []string{"source_revoked", "permission_revoked", "original_changed", "audit", "task"} {
		t.Run(failure, func(t *testing.T) {
			f, _, card, replacement := r25ConfirmedEvidenceFixture(t)
			c := semanticDraft(t, f.svc, f.owner, f.project, "r25-card-failure-draft", r25CardDelta(card, replacement))
			if _, _, _, err := f.svc.AssessChangeSet(context.Background(), f.owner, f.project.ID, c.ID, "r25-card-failure-assess"); err != nil {
				t.Fatal(err)
			}
			switch failure {
			case "source_revoked":
				f.policy.verdict = ErrSourceUnavailable
			case "permission_revoked":
				if err := f.svc.testDB().Exec("UPDATE tenant_members SET status = ? WHERE tenant_id = ? AND user_id = ?", "suspended", f.owner.TenantID, f.owner.UserID).Error; err != nil {
					t.Fatal(err)
				}
			case "original_changed":
				if err := f.svc.testDB().Exec("UPDATE chunks SET source_content = ? WHERE id = ?", "替换后的原文不再匹配锚点", replacement.SourceID).Error; err != nil {
					t.Fatal(err)
				}
			case "audit":
				if err := f.svc.testDB().Exec("CREATE TRIGGER r25_card_audit_fail BEFORE INSERT ON lingdoc_project_audits WHEN NEW.action = 'g2.evidence.semantic_delta' BEGIN SELECT RAISE(ABORT, 'r25 card audit unavailable'); END").Error; err != nil {
					t.Fatal(err)
				}
			case "task":
				if err := f.svc.testDB().Exec("CREATE TRIGGER r25_card_task_fail BEFORE INSERT ON lingdoc_g3_records WHEN NEW.kind = 'task' BEGIN SELECT RAISE(ABORT, 'r25 card task unavailable'); END").Error; err != nil {
					t.Fatal(err)
				}
			}
			before := r25SemanticCounts(t, f)
			_, _, _, err := f.svc.ApplyChangeSet(context.Background(), f.owner, f.project.ID, c.ID, "r25-card-failure-apply")
			if err == nil {
				t.Fatal("failure applied a formal card")
			}
			if (failure == "audit" || failure == "task") && !strings.Contains(err.Error(), "r25 card "+failure+" unavailable") {
				t.Fatalf("did not reach intended %s failure: %v", failure, err)
			}
			if !reflect.DeepEqual(before, r25SemanticCounts(t, f)) {
				t.Fatal("failed apply left operation/history/task/audit writes")
			}
			if got := r25EvidenceCard(t, f, card.ID); got.Version != card.Version || got.Status != "CONFIRMED" {
				t.Fatal("failed apply changed current evidence")
			}
			var p Project
			e := f.svc.repository.Transaction(context.Background(), TransactionOptions{ReadOnly: true}, func(tx Transaction) error {
				var err error
				p, err = tx.Project(f.owner.TenantID, f.project.ID)
				return err
			})
			if e != nil || p.ProjectVersion != f.project.ProjectVersion || p.CurrentContextRevision != f.project.CurrentContextRevision {
				t.Fatalf("failed apply changed formal project: %v", e)
			}
			var current ChangeSet
			e = f.svc.repository.Transaction(context.Background(), TransactionOptions{ReadOnly: true}, func(tx Transaction) error {
				var err error
				current, err = tx.(changeSetTransaction).ChangeSet(f.project.ID, c.ID)
				return err
			})
			if e != nil || current.Status != "assessed" {
				t.Fatalf("failed apply changed proposal state: %+v %v", current, e)
			}
		})
	}
}

func TestEvidenceCardProposalRequiresConsent(t *testing.T) {
	f, _, card, replacement := r25ConfirmedEvidenceFixture(t)
	delta := r25CardDelta(card, replacement)
	delta.ProposedCard.Confirmed = false
	before := r25SemanticCounts(t, f)
	input := ExtendedChangeSetInput{ExpectedContextRevision: f.project.CurrentContextRevision, Reason: "合成关系修改", SemanticDeltas: []SemanticDelta{delta}}
	if _, _, _, err := f.svc.CreateChangeDraft(context.Background(), f.owner, f.project.ID, "r25-no-evidence-consent", input); !errors.Is(err, ErrG2ConsentRequired) {
		t.Fatalf("implicit evidence consent: %v", err)
	}
	delta.ProposedCard.Confirmed = true
	delta.ProposedClaim = &SaveResearchClaimInput{Text: "forged mixed payload", Scope: "scope", Status: "active", Confirmed: true}
	input.SemanticDeltas = []SemanticDelta{delta}
	if _, _, _, err := f.svc.CreateChangeDraft(context.Background(), f.owner, f.project.ID, "r25-mixed-evidence-payload", input); !errors.Is(err, ErrInvalidRequest) {
		t.Fatalf("mixed semantic payload accepted: %v", err)
	}
	if !reflect.DeepEqual(before, r25SemanticCounts(t, f)) {
		t.Fatal("rejected semantic draft wrote operation/audit/domain state")
	}
}

func TestEvidenceCardInverseRejectsLaterChange(t *testing.T) {
	f, _, card, replacement := r25ConfirmedEvidenceFixture(t)
	original := semanticDraft(t, f.svc, f.owner, f.project, "r25-original-before-confirm", r25CardDelta(card, replacement))
	semanticApply(t, f.svc, f.owner, f.project, original, "r25-original-before-confirm")
	p, err := f.svc.GetProject(context.Background(), f.owner, f.project.ID)
	if err != nil {
		t.Fatal(err)
	}
	f.project = p
	now := r25EvidenceCard(t, f, card.ID)
	raw, _, _, err := f.svc.ConfirmEvidenceCard(context.Background(), f.owner, p.ID, now.ID, "r25-confirm-before-other-change", G2ConfirmedInput{G2Expectation: f.expectation(now.Version), Confirmed: true, Reason: "独立核对当前合成原文"})
	now = g2Decode[EvidenceCard](t, raw, err)
	input := cardSemanticInput(now)
	input.Limitations = "此后实际改变过的语义，不得被旧逆向操作覆盖"
	next := semanticDraft(t, f.svc, f.owner, p, "r25-subsequent-card-change", SemanticDelta{Kind: "EvidenceCard", TargetID: now.ID, ExpectedVersion: now.Version, ProposedCard: &input})
	semanticApply(t, f.svc, f.owner, p, next, "r25-subsequent-card-change")
	before := r25SemanticCounts(t, f)
	_, _, _, err = f.svc.ReverseChangeSet(context.Background(), f.owner, p.ID, original.ID, "r25-reject-old-card-inverse")
	if !errors.Is(err, ErrVersionConflict) || !reflect.DeepEqual(before, r25SemanticCounts(t, f)) {
		t.Fatalf("inverse overwrote later meaning or wrote a failed operation: %v", err)
	}
	if got := r25EvidenceCard(t, f, card.ID); got.Version != now.Version+1 || got.Limitations != input.Limitations {
		t.Fatal("failed old inverse changed current card")
	}
}

func TestAssociatedClaimAndCardMustBeStaged(t *testing.T) {
	f, claim, card, replacement := r25ConfirmedEvidenceFixture(t)
	raw, _, _, err := f.svc.SaveResearchClaim(context.Background(), f.owner, f.project.ID, "r25-independent-new-claim", SaveResearchClaimInput{G2Expectation: f.expectation(0), Text: "另一个未确认的合成主张", Scope: "合成范围", Status: "active", Reason: "普通收集", Confirmed: true})
	other := g2Decode[ResearchClaim](t, raw, err)
	claimDelta := SemanticDelta{Kind: "ResearchClaim", TargetID: claim.ID, ExpectedVersion: claim.Version, ProposedClaim: &SaveResearchClaimInput{Text: "新主张含义", Scope: claim.Scope, Status: "active", Confirmed: true}}
	delta := r25CardDelta(card, replacement)
	for _, which := range []string{"proposed_claim", "previous_claim"} {
		t.Run(which, func(t *testing.T) {
			proposal := *delta.ProposedCard
			if which == "proposed_claim" {
				proposal.ClaimRef = &G2VersionRef{ID: claim.ID, Version: claim.Version + 1}
			} else {
				proposal.ClaimRef = &G2VersionRef{ID: other.ID, Version: other.Version}
			}
			cardDelta := delta
			cardDelta.ProposedCard = &proposal
			before := r25SemanticCounts(t, f)
			_, _, _, err := f.svc.CreateChangeDraft(context.Background(), f.owner, f.project.ID, "r25-reject-compound-"+which, ExtendedChangeSetInput{ExpectedContextRevision: f.project.CurrentContextRevision, Reason: "不支持同批关联对象的未来版本解析", SemanticDeltas: []SemanticDelta{claimDelta, cardDelta}})
			if !errors.Is(err, ErrSemanticBatchStagingRequired) || !reflect.DeepEqual(before, r25SemanticCounts(t, f)) {
				t.Fatalf("associated batch reached late apply or wrote a draft: %v", err)
			}
		})
	}
	// A previously persisted mixed plan must also be rejected before assessment.
	draft := semanticDraft(t, f.svc, f.owner, f.project, "r25-legacy-mixed-card-draft", delta)
	if err := f.svc.repository.Transaction(context.Background(), TransactionOptions{}, func(tx Transaction) error {
		return tx.(semanticTransaction).SaveSemanticPlan(f.project.ID, draft.ID, append(draft.SemanticDeltas, claimDelta))
	}); err != nil {
		t.Fatal(err)
	}
	before := r25SemanticCounts(t, f)
	if _, _, _, err := f.svc.AssessChangeSet(context.Background(), f.owner, f.project.ID, draft.ID, "r25-reject-mixed-assess"); !errors.Is(err, ErrSemanticBatchStagingRequired) || !reflect.DeepEqual(before, r25SemanticCounts(t, f)) {
		t.Fatalf("legacy mixed plan reached assessment: %v", err)
	}
}
