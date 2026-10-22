package candidateadoption

import (
	"context"
	"encoding/json"
	"errors"
	"testing"

	core "github.com/Tencent/WeKnora/internal/lingdoc/workspacecore"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

type candidateEvidenceTestPolicy struct {
	denied   bool
	txDenied bool
	calls    int
	txCalls  int
	selected core.EvidenceSelection
}

func (p *candidateEvidenceTestPolicy) PrepareCandidateEvidence(ctx context.Context, pid, ch, user string, refs []core.VersionRef, ids []string) (context.Context, core.EvidenceSelection, error) {
	if len(refs) == 0 {
		return ctx, p.selected, nil
	}
	selected, err := p.ValidateCandidateEvidence(ctx, pid, ch, user, refs, ids)
	return ctx, selected, err
}
func (p *candidateEvidenceTestPolicy) Validate(context.Context, string, string, []string) error {
	return nil
}
func (p *candidateEvidenceTestPolicy) ValidateCandidateEvidence(context.Context, string, string, string, []core.VersionRef, []string) (core.EvidenceSelection, error) {
	p.calls++
	if p.denied {
		return p.selected, ErrSourceAccessDenied
	}
	return p.selected, nil
}
func (p *candidateEvidenceTestPolicy) ValidateCandidateEvidenceInTransaction(context.Context, *gorm.DB, string, string, string, []core.VersionRef, []string) (core.EvidenceSelection, error) {
	p.txCalls++
	if p.denied || p.txDenied {
		return p.selected, ErrSourceAccessDenied
	}
	return p.selected, nil
}

type candidateScopeAuthorizer struct{ capabilities []string }

func (a *candidateScopeAuthorizer) Authorize(_ context.Context, _, _, capability string) error {
	a.capabilities = append(a.capabilities, capability)
	if capability != "write:chapter" {
		return ErrForbidden
	}
	return nil
}

func newBoundCandidateFixture(t *testing.T, refs bool) (*SQLiteCandidateAdoptionStore, Candidate, AcceptCandidateInput, *candidateEvidenceTestPolicy) {
	t.Helper()
	store := newCandidateAdoptionStore(t)
	require.NoError(t, store.DB().AutoMigrate(&projectTemplateCopyRow{}))
	c := Candidate{ID: "candidate", ProjectID: "project", ChapterID: "chapter", BodyMarkdown: "正文 [[source:source]]", SourceIDs: []string{"source"}, Validity: "fresh", Basis: Basis{SpecRevision: 1, TemplateID: "template-demo", TemplateVersion: "1", ContextRevision: 4, TemplateCopyID: "copy", TemplateCopyVersion: 2, TemplateCopyContentHash: "content-hash", RulesetHash: "effective-rules-hash", AssetVersions: []AssetVersion{{AssetID: "asset", AssetRevision: 1}}}}
	if refs {
		c.Basis.EvidenceCardRefs = []core.VersionRef{{ID: "card", Version: 3, Kind: "EvidenceCard"}}
	}
	seedCandidateAdoptionWorkspace(t, store, Chapter{ID: "chapter", ProjectID: "project"}, c, 1)
	require.NoError(t, store.DB().Create(&projectAssetRow{ID: "asset", ProjectID: "project", AssetRevision: 1}).Error)
	require.NoError(t, store.DB().Model(&projectRow{}).Where("id = ?", "project").Updates(map[string]any{"current_context_revision": 4, "template_copy_version": 2, "status": "active"}).Error)
	require.NoError(t, store.DB().Create(&projectTemplateCopyRow{ID: "copy", ProjectID: "project", Version: 2, ContentHash: "content-hash", RulesetHash: "effective-rules-hash", Status: core.TemplateCopyBound, DefinitionJSON: "{}"}).Error)
	policy := &candidateEvidenceTestPolicy{selected: core.EvidenceSelection{ContextRevision: 4, Cards: []core.EvidenceCard{{ID: "card", ProjectID: "project", Version: 3, Status: "CONFIRMED", SourceIDs: []string{"source"}, Confirmation: &core.EvidenceConfirmation{CardVersion: 3, ConfirmedBy: "reviewer"}}}, Sources: []core.AgentSourceRef{{SourceID: "source", AssetID: "asset", AssetRevision: 1, ModelUseAllowed: true}}}}
	return store, c, AcceptCandidateInput{ProjectID: "project", ChapterID: "chapter", CandidateID: "candidate", ActorID: "actor", ExpectedSpecRevision: 1, IdempotencyKey: "bound-adoption-key"}, policy
}

func TestCandidateAdoptionPinsCurrentCopyContextAndEffectiveRulesInFinalWrite(t *testing.T) {
	for _, field := range []string{"context", "copy_id", "copy_version", "copy_content", "effective_rules"} {
		t.Run(field, func(t *testing.T) {
			store, c, in, _ := newBoundCandidateFixture(t, false)
			current, err := store.GenerationContext(context.Background(), "project", "chapter")
			require.NoError(t, err)
			require.Equal(t, c.Basis.TemplateCopyID, current.Basis.TemplateCopyID)
			require.Equal(t, c.Basis.ContextRevision, current.Basis.ContextRevision)
			require.Equal(t, c.Basis.RulesetHash, current.Basis.RulesetHash)
			require.NoError(t, validateCandidate(in, current, c))
			switch field {
			case "context":
				require.NoError(t, store.DB().Model(&projectRow{}).Where("id = ?", "project").Update("current_context_revision", 5).Error)
			case "copy_id":
				require.NoError(t, store.DB().Model(&projectTemplateCopyRow{}).Where("id = ?", "copy").Update("id", "new-copy").Error)
			case "copy_version":
				require.NoError(t, store.DB().Create(&projectTemplateCopyRow{ID: "copy3", ProjectID: "project", Version: 3, ContentHash: "content-hash", RulesetHash: "effective-rules-hash", Status: core.TemplateCopyBound, DefinitionJSON: "{}"}).Error)
				require.NoError(t, store.DB().Model(&projectRow{}).Where("id = ?", "project").Update("template_copy_version", 3).Error)
			case "copy_content":
				require.NoError(t, store.DB().Model(&projectTemplateCopyRow{}).Where("id = ?", "copy").Update("content_hash", "changed").Error)
			case "effective_rules":
				require.NoError(t, store.DB().Model(&projectTemplateCopyRow{}).Where("id = ?", "copy").Update("ruleset_hash", "changed-effective").Error)
			}
			_, err = store.AcceptCandidate(context.Background(), in, c)
			require.ErrorIs(t, err, ErrStaleInput)
			var count int64
			require.NoError(t, store.DB().Model(&chapterVersionRow{}).Count(&count).Error)
			require.Zero(t, count)
			require.NoError(t, store.DB().Model(&adoptionIdempotencyRow{}).Count(&count).Error)
			require.Zero(t, count)
		})
	}
}

func TestCandidateExplicitEvidenceRequiresAdapterAndCurrentTransactionResult(t *testing.T) {
	store, c, in, policy := newBoundCandidateFixture(t, true)
	service := NewCandidateAdoptionService(store, sourcePolicyFunc(func(context.Context, string, string, []string) error { return nil }), allowAuthorizer{})
	_, err := service.AcceptCandidate(context.Background(), in)
	require.ErrorIs(t, err, ErrDependencyUnavailable)
	policy.txDenied = true
	service = NewCandidateAdoptionService(store, policy, allowAuthorizer{})
	_, err = service.AcceptCandidate(context.Background(), in)
	require.ErrorIs(t, err, ErrSourceAccessDenied)
	require.Positive(t, policy.calls)
	require.Positive(t, policy.txCalls)
	var count int64
	require.NoError(t, store.DB().Model(&chapterVersionRow{}).Count(&count).Error)
	require.Zero(t, count)
	// Direct persistence entry cannot bypass the explicit current-card port.
	bare := NewSQLiteCandidateAdoptionStore(store.DB())
	_, err = bare.AcceptCandidate(context.Background(), in, c)
	require.ErrorIs(t, err, ErrDependencyUnavailable)
}

func TestCandidateAdoptionStoresRealVersionDependencyAndRechecksReplay(t *testing.T) {
	store, _, in, policy := newBoundCandidateFixture(t, true)
	require.NoError(t, store.DB().Exec(`CREATE TABLE lingdoc_g3_records(id TEXT PRIMARY KEY,project_id TEXT NOT NULL,kind TEXT NOT NULL,record_key TEXT NOT NULL,version INTEGER NOT NULL,body_json TEXT NOT NULL,created_at DATETIME,updated_at DATETIME,UNIQUE(project_id,kind,record_key))`).Error)
	authorizer := &candidateScopeAuthorizer{}
	service := NewCandidateAdoptionService(store, policy, authorizer)
	result, err := service.AcceptCandidate(context.Background(), in)
	require.NoError(t, err)
	require.NotNil(t, result.Chapter.CurrentVersionID)
	require.False(t, result.Chapter.ConfirmationValid)
	var row struct{ BodyJSON string }
	require.NoError(t, store.DB().Table("lingdoc_g3_records").Where("kind = ?", "edge").Take(&row).Error)
	var edge core.DependencyEdge
	require.NoError(t, json.Unmarshal([]byte(row.BodyJSON), &edge))
	require.Equal(t, core.ObjectVersionRef{Kind: "EvidenceCard", ID: "card", Version: "3"}, edge.Source)
	require.Equal(t, core.ObjectVersionRef{Kind: "ChapterVersion", ID: "chapter", Version: *result.Chapter.CurrentVersionID}, edge.Target)
	require.Equal(t, "confirmed", edge.Status)
	replay, err := service.AcceptCandidate(context.Background(), in)
	require.NoError(t, err)
	require.True(t, replay.Replayed)
	before := policy.txCalls
	policy.denied = true
	_, err = service.AcceptCandidate(context.Background(), in)
	require.ErrorIs(t, err, ErrSourceAccessDenied)
	require.Equal(t, before, policy.txCalls, "revocation is rejected by real preflight before replay takes a connection")
	var count int64
	require.NoError(t, store.DB().Model(&chapterVersionRow{}).Count(&count).Error)
	require.Equal(t, int64(1), count)
	for _, capability := range authorizer.capabilities {
		require.Equal(t, "write:chapter", capability)
	}
}

func TestCandidateDependencyFailureRollsBackAllFormalWrites(t *testing.T) {
	store, _, in, policy := newBoundCandidateFixture(t, true)
	// No impact records table: the real edge write must fail rather than silently skip it.
	service := NewCandidateAdoptionService(store, policy, allowAuthorizer{})
	_, err := service.AcceptCandidate(context.Background(), in)
	require.Error(t, err)
	require.Contains(t, err.Error(), "lingdoc_g3_records")
	var count int64
	require.NoError(t, store.DB().Model(&chapterVersionRow{}).Count(&count).Error)
	require.Zero(t, count)
	require.NoError(t, store.DB().Model(&confirmationRow{}).Count(&count).Error)
	require.Zero(t, count)
	require.NoError(t, store.DB().Model(&adoptionIdempotencyRow{}).Count(&count).Error)
	require.Zero(t, count)
	var p projectRow
	require.NoError(t, store.DB().First(&p).Error)
	require.Zero(t, p.ProjectVersion)
	var chapter chapterRow
	require.NoError(t, store.DB().First(&chapter).Error)
	require.Nil(t, chapter.CurrentVersionID)
}

func TestCandidateNoCardReferencesCreateNoInventedDependency(t *testing.T) {
	store, _, in, policy := newBoundCandidateFixture(t, false)
	result, err := NewCandidateAdoptionService(store, policy, allowAuthorizer{}).AcceptCandidate(context.Background(), in)
	require.NoError(t, err)
	require.NotNil(t, result.Chapter.CurrentVersionID)
	require.Zero(t, policy.calls)
	require.Positive(t, policy.txCalls, "native tx proof is checked even without explicit card refs")
}

func TestCandidateSelectionRejectsUnconfirmedUnrelatedAndUnconsentedCards(t *testing.T) {
	_, c, _, p := newBoundCandidateFixture(t, true)
	require.NoError(t, validateEvidenceSelection(c.Basis.EvidenceCardRefs, c.SourceIDs, p.selected))
	p.selected.Cards[0].Confirmation = nil
	require.ErrorIs(t, validateEvidenceSelection(c.Basis.EvidenceCardRefs, c.SourceIDs, p.selected), ErrSourceAccessDenied)
	p.selected.Cards[0].Confirmation = &core.EvidenceConfirmation{CardVersion: 3, ConfirmedBy: "reviewer"}
	p.selected.Cards[0].SourceIDs = []string{"unrelated"}
	require.ErrorIs(t, validateEvidenceSelection(c.Basis.EvidenceCardRefs, c.SourceIDs, p.selected), ErrSourceAccessDenied)
	p.selected.Cards[0].SourceIDs = c.SourceIDs
	p.selected.Sources[0].ModelUseAllowed = false
	require.True(t, errors.Is(validateEvidenceSelection(c.Basis.EvidenceCardRefs, c.SourceIDs, p.selected), ErrSourceAccessDenied))
}

func TestCandidateUsedAssetsAreAnExactCurrentSubset(t *testing.T) {
	current := []AssetVersion{{AssetID: "used", AssetRevision: 3}, {AssetID: "unused", AssetRevision: 8}}
	require.True(t, currentAssetSubset(current, []AssetVersion{{AssetID: "used", AssetRevision: 3}}))
	for _, bad := range [][]AssetVersion{{{AssetID: "missing", AssetRevision: 3}}, {{AssetID: "used", AssetRevision: 2}}, {{AssetID: "used", AssetRevision: 3}, {AssetID: "used", AssetRevision: 3}}} {
		require.False(t, currentAssetSubset(current, bad))
	}
}
