package workspacecore

import (
	"context"
	"encoding/json"
	"errors"
	"testing"
	"time"
)

func academicAssociationFixture(t *testing.T) (agentFixture, ImportCandidate) {
	t.Helper()
	f, run, _ := setupAcademicResearchRun(t)
	raw, _, _, err := f.svc.CreateImportCandidate(context.Background(), f.owner, f.project.ID, "assoc-metadata-create", academicCandidateInput(f, run))
	candidate := decodeAgentJSON[ImportCandidate](t, raw, err)
	raw, _, _, err = f.svc.ConfirmImportCandidate(context.Background(), f.owner, f.project.ID, candidate.ID, "assoc-metadata-confirm", ConfirmationInput{VersionExpectation: f.expectation(candidate.Version), Confirmed: true, Reason: "metadata accepted"})
	candidate = decodeAgentJSON[ImportCandidate](t, raw, err)
	seedNativeEvidence(t, f)
	seedCandidateProofFoundation(t, f)
	if !f.svc.testDB().Migrator().HasColumn("chunks", "content_revision") {
		if e := f.svc.testDB().Exec("ALTER TABLE chunks ADD COLUMN content_revision INTEGER NOT NULL DEFAULT 0").Error; e != nil {
			t.Fatal(e)
		}
	}
	return f, candidate
}

func TestAcademicAssociationRecordsHumanAssertion(t *testing.T) {
	f, candidate := academicAssociationFixture(t)
	input := ConfirmAcademicSourceAssociationInput{VersionExpectation: f.expectation(candidate.Version), CandidateID: candidate.ID, SourceID: "source-1", ExpectedAssetRevision: f.source.sources["source-1"].AssetRevision, ExpectedQuotedTextHash: f.source.sources["source-1"].QuotedTextHash, ExpectedLocator: f.source.sources["source-1"].Locator, Confirmed: true, Reason: "checked bibliographic details in uploaded paper"}
	raw, status, _, err := f.svc.ConfirmAcademicSourceAssociation(context.Background(), f.owner, f.project.ID, "assoc-confirm", input)
	got := decodeAgentJSON[AcademicSourceAssociation](t, raw, err)
	source := f.source.sources[input.SourceID]
	if status != 201 || got.CandidateRef.ID != candidate.ID || got.CandidateRef.Version != candidate.Version || got.DOI != candidate.DOI || got.CatalogVersion != candidate.CatalogVersion || got.PolicySnapshotVersion != candidate.PolicySnapshotVersion || got.SourceID != source.SourceID || got.AssetID != source.AssetID || got.AssetRevision != source.AssetRevision || got.QuotedTextHash != source.QuotedTextHash || got.ConfirmedBy != f.owner.UserID || got.Reason != input.Reason {
		t.Fatalf("incomplete association provenance: %+v", got)
	}
	raw2, _, replay, err := f.svc.ConfirmAcademicSourceAssociation(context.Background(), f.owner, f.project.ID, "assoc-confirm", input)
	got2 := decodeAgentJSON[AcademicSourceAssociation](t, raw2, err)
	if !replay || got2.ID != got.ID {
		t.Fatal("idempotent replay changed association")
	}
	var audits []auditEventRow
	if err := f.svc.testDB().Where("project_id = ? AND action = ? AND target = ?", f.project.ID, "g2.academic_source_association.confirm", f.project.ID+"/"+got.ID).Find(&audits).Error; err != nil {
		t.Fatal(err)
	}
	if len(audits) != 1 || audits[0].ActorID != f.owner.UserID {
		t.Fatalf("missing, duplicated or wrong association audit: %+v", audits)
	}
	var details map[string]any
	if err := json.Unmarshal([]byte(audits[0].DetailsJSON), &details); err != nil {
		t.Fatal(err)
	}
	if details["reason"] != input.Reason || details["source_id"] != source.SourceID || details["candidate_id"] != candidate.ID {
		t.Fatalf("incomplete association audit details: %+v", details)
	}
}

func TestAcademicAssociationRejectsMissingHumanDecisionOrUnusableSource(t *testing.T) {
	for _, tc := range []struct {
		name   string
		mutate func(*agentFixture, *ConfirmAcademicSourceAssociationInput)
		want   error
	}{
		{"no confirmation", func(_ *agentFixture, i *ConfirmAcademicSourceAssociationInput) { i.Confirmed = false }, ErrConsentRequired},
		{"no reason", func(_ *agentFixture, i *ConfirmAcademicSourceAssociationInput) { i.Reason = " " }, ErrInvalidRequest},
		{"denied", func(f *agentFixture, _ *ConfirmAcademicSourceAssociationInput) { f.source.denied["source-1"] = true }, ErrSourceUnavailable},
		{"unstable", func(f *agentFixture, _ *ConfirmAcademicSourceAssociationInput) {
			s := f.source.sources["source-1"]
			s.StableOriginal = false
			f.source.sources["source-1"] = s
		}, ErrEvidenceIncomplete},
		{"bad hash", func(f *agentFixture, _ *ConfirmAcademicSourceAssociationInput) {
			s := f.source.sources["source-1"]
			s.QuotedTextHash = "invalid"
			f.source.sources["source-1"] = s
		}, ErrEvidenceIncomplete},
		{"wrong candidate version", func(_ *agentFixture, i *ConfirmAcademicSourceAssociationInput) { i.ExpectedVersion++ }, ErrVersionConflict},
		{"source changed after review", func(_ *agentFixture, i *ConfirmAcademicSourceAssociationInput) {
			i.ExpectedQuotedTextHash = "old-reviewed-hash"
		}, ErrVersionConflict},
		{"source revision changed after review", func(_ *agentFixture, i *ConfirmAcademicSourceAssociationInput) { i.ExpectedAssetRevision++ }, ErrVersionConflict},
		{"missing reviewed hash", func(_ *agentFixture, i *ConfirmAcademicSourceAssociationInput) { i.ExpectedQuotedTextHash = "" }, ErrInvalidRequest},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f, candidate := academicAssociationFixture(t)
			input := ConfirmAcademicSourceAssociationInput{VersionExpectation: f.expectation(candidate.Version), CandidateID: candidate.ID, SourceID: "source-1", ExpectedAssetRevision: f.source.sources["source-1"].AssetRevision, ExpectedQuotedTextHash: f.source.sources["source-1"].QuotedTextHash, ExpectedLocator: f.source.sources["source-1"].Locator, Confirmed: true, Reason: "checked manually"}
			tc.mutate(&f, &input)
			if _, _, _, err := f.svc.ConfirmAcademicSourceAssociation(context.Background(), f.owner, f.project.ID, "assoc-reject", input); !errors.Is(err, tc.want) {
				t.Fatalf("got %v, want %v", err, tc.want)
			}
			var count int64
			if err := f.svc.testDB().Table("lingdoc_g2_objects").Where("project_id = ? AND kind = ?", f.project.ID, AcademicSourceAssociationKind).Count(&count).Error; err != nil || count != 0 {
				t.Fatalf("rejected assertion persisted: %d, %v", count, err)
			}
		})
	}
}

func TestAcademicAssociationReplayRejectsChangedNativeSource(t *testing.T) {
	f, candidate := academicAssociationFixture(t)
	input := ConfirmAcademicSourceAssociationInput{VersionExpectation: f.expectation(candidate.Version), CandidateID: candidate.ID, SourceID: "source-1", ExpectedAssetRevision: f.source.sources["source-1"].AssetRevision, ExpectedQuotedTextHash: f.source.sources["source-1"].QuotedTextHash, ExpectedLocator: f.source.sources["source-1"].Locator, Confirmed: true, Reason: "reviewed metadata and original"}
	if _, _, _, err := f.svc.ConfirmAcademicSourceAssociation(context.Background(), f.owner, f.project.ID, "assoc-stale-replay", input); err != nil {
		t.Fatal(err)
	}
	if err := f.svc.testDB().Exec("UPDATE chunks SET content = ? WHERE id = ?", "changed original", "source-1").Error; err != nil {
		t.Fatal(err)
	}
	if _, _, _, err := f.svc.ConfirmAcademicSourceAssociation(context.Background(), f.owner, f.project.ID, "assoc-stale-replay", input); !errors.Is(err, ErrVersionConflict) {
		t.Fatalf("changed native chunk replay: %v", err)
	}
}

func TestAcademicAssociationReplayRejectsRevokedTenantMembership(t *testing.T) {
	f, candidate := academicAssociationFixture(t)
	input := ConfirmAcademicSourceAssociationInput{VersionExpectation: f.expectation(candidate.Version), CandidateID: candidate.ID, SourceID: "source-1", ExpectedAssetRevision: f.source.sources["source-1"].AssetRevision, ExpectedQuotedTextHash: f.source.sources["source-1"].QuotedTextHash, ExpectedLocator: f.source.sources["source-1"].Locator, Confirmed: true, Reason: "reviewed metadata and original"}
	if _, _, _, err := f.svc.ConfirmAcademicSourceAssociation(context.Background(), f.owner, f.project.ID, "assoc-revoked-replay", input); err != nil {
		t.Fatal(err)
	}
	if err := f.svc.testDB().Exec("UPDATE tenant_members SET status = 'disabled' WHERE tenant_id = ? AND user_id = ?", f.owner.TenantID, f.owner.UserID).Error; err != nil {
		t.Fatal(err)
	}
	if _, _, _, err := f.svc.ConfirmAcademicSourceAssociation(context.Background(), f.owner, f.project.ID, "assoc-revoked-replay", input); err == nil {
		t.Fatal("replay disclosed result after membership revocation")
	}
}

// A revocation racing the write must not commit while the association
// transaction is holding its SQLite writer lock. This is SQLite evidence only;
// PostgreSQL row-lock ordering remains a required separate integration test.
func TestAcademicAssociationSQLiteRevocationIsSerialized(t *testing.T) {
	f, candidate := academicAssociationFixture(t)
	source := f.source.sources["source-1"]
	ready, release, done := make(chan struct{}), make(chan struct{}), make(chan error, 1)
	go func() {
		done <- f.svc.repository.Transaction(context.Background(), TransactionOptions{}, func(tx Transaction) error {
			p, e := safelyLockAssociationTest(f.svc, tx, f.owner, f.project.ID, source)
			if e != nil {
				return e
			}
			_ = p
			close(ready)
			<-release
			return nil
		})
	}()
	select {
	case <-ready:
	case e := <-done:
		t.Fatalf("lock fixture: %v", e)
	case <-time.After(5 * time.Second):
		t.Fatal("timed out locking source")
	}
	revoke := make(chan error, 1)
	go func() {
		revoke <- f.svc.testDB().Exec("UPDATE tenant_members SET status = 'disabled' WHERE tenant_id = ? AND user_id = ?", f.owner.TenantID, f.owner.UserID).Error
	}()
	select {
	case e := <-revoke:
		if e == nil {
			close(release)
			t.Fatal("revocation committed during association lock")
		}
	case <-time.After(100 * time.Millisecond):
	}
	close(release)
	if e := <-done; e != nil {
		t.Fatal(e)
	}
	if e := f.svc.testDB().Exec("UPDATE tenant_members SET status = 'disabled' WHERE tenant_id = ? AND user_id = ?", f.owner.TenantID, f.owner.UserID).Error; e != nil {
		t.Fatal(e)
	}
	input := ConfirmAcademicSourceAssociationInput{VersionExpectation: f.expectation(candidate.Version), CandidateID: candidate.ID, SourceID: "source-1", ExpectedAssetRevision: f.source.sources["source-1"].AssetRevision, ExpectedQuotedTextHash: f.source.sources["source-1"].QuotedTextHash, ExpectedLocator: f.source.sources["source-1"].Locator, Confirmed: true, Reason: "checked manually"}
	if _, _, _, e := f.svc.ConfirmAcademicSourceAssociation(context.Background(), f.owner, f.project.ID, "revocation-then-association", input); e == nil {
		t.Fatal("revoked actor created association")
	}
}

func safelyLockAssociationTest(s *Service, tx Transaction, actor Actor, projectID string, source ResearchSource) (Project, error) {
	p, e := s.lockAssociationDecisionRows(context.Background(), tx, actor, projectID)
	if e != nil {
		return Project{}, e
	}
	if e = validateAssociationSourceInTransaction(context.Background(), tx, actor, p, source); e != nil {
		return Project{}, e
	}
	return s.lockAssociationDecisionRows(context.Background(), tx, actor, projectID)
}

func TestAcademicAssociationDoesNotRequireOrGrantModelUse(t *testing.T) {
	f, candidate := academicAssociationFixture(t)
	var policy ProjectAssetPolicy
	var row objectRow
	if err := f.svc.testDB().Where("project_id = ? AND kind = ? AND id = ?", f.project.ID, AssetPolicyKind, "asset-1").Take(&row).Error; err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal([]byte(row.PayloadJSON), &policy); err != nil {
		t.Fatal(err)
	}
	policy.ModelUseAllowed = false
	payload, err := json.Marshal(policy)
	if err != nil {
		t.Fatal(err)
	}
	if err = f.svc.testDB().Model(&objectRow{}).Where("project_id = ? AND kind = ? AND id = ?", f.project.ID, AssetPolicyKind, "asset-1").Update("payload_json", string(payload)).Error; err != nil {
		t.Fatal(err)
	}
	source := f.source.sources["source-1"]
	source.ModelUseAllowed = false
	f.source.sources["source-1"] = source
	input := ConfirmAcademicSourceAssociationInput{VersionExpectation: f.expectation(candidate.Version), CandidateID: candidate.ID, SourceID: "source-1", ExpectedAssetRevision: f.source.sources["source-1"].AssetRevision, ExpectedQuotedTextHash: f.source.sources["source-1"].QuotedTextHash, ExpectedLocator: f.source.sources["source-1"].Locator, Confirmed: true, Reason: "human reviewed the record and source"}
	if _, _, _, err = f.svc.ConfirmAcademicSourceAssociation(context.Background(), f.owner, f.project.ID, "assoc-no-model-grant", input); err != nil {
		t.Fatal(err)
	}
	if err = f.svc.testDB().Where("project_id = ? AND kind = ? AND id = ?", f.project.ID, AssetPolicyKind, "asset-1").Take(&row).Error; err != nil {
		t.Fatal(err)
	}
	if err = json.Unmarshal([]byte(row.PayloadJSON), &policy); err != nil {
		t.Fatal(err)
	}
	if policy.ModelUseAllowed {
		t.Fatal("association granted model use")
	}
}

func TestAcademicAssociationAuditFailureRollsBack(t *testing.T) {
	f, candidate := academicAssociationFixture(t)
	if err := f.svc.testDB().Exec(`CREATE TRIGGER reject_association_audit BEFORE INSERT ON lingdoc_project_audits WHEN NEW.action = 'g2.academic_source_association.confirm' BEGIN SELECT RAISE(ABORT, 'synthetic audit failure'); END`).Error; err != nil {
		t.Fatal(err)
	}
	input := ConfirmAcademicSourceAssociationInput{VersionExpectation: f.expectation(candidate.Version), CandidateID: candidate.ID, SourceID: "source-1", ExpectedAssetRevision: f.source.sources["source-1"].AssetRevision, ExpectedQuotedTextHash: f.source.sources["source-1"].QuotedTextHash, ExpectedLocator: f.source.sources["source-1"].Locator, Confirmed: true, Reason: "human reviewed"}
	if _, _, _, err := f.svc.ConfirmAcademicSourceAssociation(context.Background(), f.owner, f.project.ID, "assoc-audit-fails", input); err == nil {
		t.Fatal("audit failure accepted")
	}
	var count int64
	if err := f.svc.testDB().Table("lingdoc_g2_objects").Where("project_id = ? AND kind = ?", f.project.ID, AcademicSourceAssociationKind).Count(&count).Error; err != nil || count != 0 {
		t.Fatalf("association persisted without audit: %d, %v", count, err)
	}
}

func TestAcademicAssociationRejectsRetractionNotice(t *testing.T) {
	f, candidate := academicAssociationFixture(t)
	candidate.RetractionStatus = "retraction_notice"
	payload, err := json.Marshal(candidate)
	if err != nil {
		t.Fatal(err)
	}
	if err = f.svc.testDB().Exec("UPDATE lingdoc_g2_objects SET payload_json = ? WHERE project_id = ? AND kind = ? AND id = ?", string(payload), f.project.ID, ImportCandidateKind, candidate.ID).Error; err != nil {
		t.Fatal(err)
	}
	source := f.source.sources["source-1"]
	input := ConfirmAcademicSourceAssociationInput{VersionExpectation: f.expectation(candidate.Version), CandidateID: candidate.ID, SourceID: source.SourceID, ExpectedAssetRevision: source.AssetRevision, ExpectedQuotedTextHash: source.QuotedTextHash, ExpectedLocator: source.Locator, Confirmed: true, Reason: "human review"}
	if _, _, _, err = f.svc.ConfirmAcademicSourceAssociation(context.Background(), f.owner, f.project.ID, "assoc-retraction-notice", input); !errors.Is(err, ErrEvidenceIncomplete) {
		t.Fatalf("retraction notice accepted: %v", err)
	}
}

func TestAcademicAssociationHistoryRevocationAndPermission(t *testing.T) {
	f, candidate := academicAssociationFixture(t)
	source := f.source.sources["source-1"]
	input := ConfirmAcademicSourceAssociationInput{VersionExpectation: f.expectation(candidate.Version), CandidateID: candidate.ID, SourceID: source.SourceID, ExpectedAssetRevision: source.AssetRevision, ExpectedQuotedTextHash: source.QuotedTextHash, ExpectedLocator: source.Locator, Confirmed: true, Reason: "human review"}
	raw, _, _, err := f.svc.ConfirmAcademicSourceAssociation(context.Background(), f.owner, f.project.ID, "history-create", input)
	a := decodeAgentJSON[AcademicSourceAssociation](t, raw, err)
	history, err := f.svc.ListAcademicSourceAssociations(context.Background(), f.owner, f.project.ID)
	if err != nil || len(history) != 1 || history[0].ReviewState != "historical" {
		t.Fatalf("history=%+v err=%v", history, err)
	}
	f.source.denied[source.SourceID] = true
	history, err = f.svc.ListAcademicSourceAssociations(context.Background(), f.owner, f.project.ID)
	if err != nil || len(history) != 0 {
		t.Fatalf("restricted history leaked: %+v %v", history, err)
	}
	decision := ConfirmationInput{VersionExpectation: f.expectation(a.Version), Confirmed: true, Reason: "withdraw earlier review"}
	raw, _, _, err = f.svc.RevokeAcademicSourceAssociation(context.Background(), f.owner, f.project.ID, a.ID, "history-revoke", decision)
	receipt := decodeAgentJSON[AssociationRevocationReceipt](t, raw, err)
	if receipt.Status != "revoked" || receipt.Version != a.Version+1 {
		t.Fatalf("receipt=%+v", receipt)
	}
	if _, _, replay, e := f.svc.RevokeAcademicSourceAssociation(context.Background(), f.owner, f.project.ID, a.ID, "history-revoke", decision); e != nil || !replay {
		t.Fatalf("revoke replay: %v %v", replay, e)
	}
	f.source.denied[source.SourceID] = false
	history, err = f.svc.ListAcademicSourceAssociations(context.Background(), f.owner, f.project.ID)
	if err != nil || len(history) != 1 || history[0].ReviewState != "revoked" || history[0].RevocationReason != decision.Reason {
		t.Fatalf("revoked history=%+v err=%v", history, err)
	}
	if _, _, _, err = f.svc.ConfirmAcademicSourceAssociation(context.Background(), f.owner, f.project.ID, "history-create", input); !errors.Is(err, ErrVersionConflict) {
		t.Fatalf("old confirmation resurrected after revoke: %v", err)
	}
}

func TestAcademicAssociationHistoryDetectsChangedSource(t *testing.T) {
	f, candidate := academicAssociationFixture(t)
	source := f.source.sources["source-1"]
	input := ConfirmAcademicSourceAssociationInput{VersionExpectation: f.expectation(candidate.Version), CandidateID: candidate.ID, SourceID: source.SourceID, ExpectedAssetRevision: source.AssetRevision, ExpectedQuotedTextHash: source.QuotedTextHash, ExpectedLocator: source.Locator, Confirmed: true, Reason: "review"}
	if _, _, _, err := f.svc.ConfirmAcademicSourceAssociation(context.Background(), f.owner, f.project.ID, "history-stale", input); err != nil {
		t.Fatal(err)
	}
	source.AssetRevision++
	f.source.sources[source.SourceID] = source
	history, err := f.svc.ListAcademicSourceAssociations(context.Background(), f.owner, f.project.ID)
	if err != nil || len(history) != 1 || history[0].ReviewState != "needs_review" {
		t.Fatalf("changed history=%+v err=%v", history, err)
	}
}
