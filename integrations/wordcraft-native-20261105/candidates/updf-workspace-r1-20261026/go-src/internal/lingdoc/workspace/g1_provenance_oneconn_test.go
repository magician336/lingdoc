package workspace

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/Tencent/WeKnora/internal/evidence"
	"github.com/Tencent/WeKnora/internal/lingdoc/delivery"
	core "github.com/Tencent/WeKnora/internal/lingdoc/workspacecore"
	"github.com/Tencent/WeKnora/internal/types"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

type g1NativeFixture struct {
	db         *gorm.DB
	projects   *core.Service
	governance *TemplateGovernanceRuntime
	actor      core.Actor
	project    core.Project
	asset      evidence.Asset
	ctx        context.Context
}

func g1NativeProvenanceFixture(t *testing.T) g1NativeFixture {
	t.Helper()
	_, db, actor, p, _ := g4AdapterFixture(t)
	ctx := g4CallerContext(context.Background(), actor)
	ctx = types.WithPrincipal(ctx, types.Principal{Type: types.PrincipalWebUser, ID: actor.UserID})
	ctx, cancel := context.WithTimeout(ctx, 20*time.Second)
	t.Cleanup(cancel)
	runtime := NewGORMSourceRuntime(db, nil, g4AdapterShares{}, nil, delivery.NewFixedTemplateReader())
	projects := core.NewServiceWithSources(core.NewGORMRepository(db), core.ContractDemoTemplate{}, runtime.WorkspaceSourcePolicy())
	runtime.ConnectProjects(projects)
	runtime.EnableFullWorkflowPolicy()
	projects.ConfigureG2(core.G2Runtime{Sources: NewG2ResearchAdapter(db, projects, runtime, g4AdapterShares{}, nil, nil), Actions: NewG2ActionExecutor(projects, runtime, nil)})
	governance, err := NewTemplateGovernanceRuntime(db, projects, runtime, nil)
	require.NoError(t, err)
	projects.SetTemplateCatalogResolver(governance)
	projects.SetImpactRuleChecker(governance.ImpactRuleChecker())
	asset, _ := g4AdapterSource(t, governance, db, p, actor)
	pool, err := db.DB()
	require.NoError(t, err)
	require.Equal(t, 1, pool.Stats().MaxOpenConnections)
	return g1NativeFixture{db: db, projects: projects, governance: governance, actor: actor, project: p, asset: asset, ctx: ctx}
}
func (f g1NativeFixture) specInput(value string) core.SaveSpecInput {
	return core.SaveSpecInput{ExpectedSpecRevision: f.project.SpecRevision, Fields: map[string]string{"research_subject": value, "research_goal": "人类输入的软件目标"}, FieldMetadata: map[string]core.SpecFieldInput{"research_subject": {Origin: "human", Status: "draft", Provenance: &core.ProvenanceRecord{SourceType: "source", SourceID: "source-runtime", SourceVersion: "1", CreatedBy: "forged-owner", Accessible: false, Locator: "private source locator", Purpose: "private source purpose", AuthorizationScope: "client-made-grant"}}}}
}

func TestG1ActualOneConnectionCurrentSourceProjectionReplayAndNoDataLoss(t *testing.T) {
	f := g1NativeProvenanceFixture(t)
	input := f.specInput("合成来源派生条件")
	raw, _, _, err := f.projects.SaveSpec(f.ctx, f.actor, f.project.ID, "g1-native-spec-01", input)
	require.NoError(t, err)
	var p core.Project
	require.NoError(t, json.Unmarshal(raw, &p))
	require.Equal(t, input.Fields["research_subject"], p.Spec["research_subject"])
	require.Equal(t, f.actor.UserID, p.SpecFields["research_subject"].Provenance.CreatedBy)
	require.True(t, p.SpecFields["research_subject"].Provenance.Accessible)
	candidateInput := core.DraftCandidateInput{Kind: "retrieval", Title: "来源摘录合成标题", Content: "来源摘录合成正文", Provenance: input.FieldMetadata["research_subject"].Provenance}
	_, _, _, err = f.projects.CreateDraftCandidate(f.ctx, f.actor, p.ID, "g1-native-candidate", candidateInput)
	require.NoError(t, err)
	// Revoke the actual native resource, rather than flip a client Accessible flag.
	require.NoError(t, f.db.Exec("UPDATE knowledge_bases SET deleted_at=CURRENT_TIMESTAMP WHERE id=?", "kb-runtime").Error)
	assertHidden := func(p core.Project) {
		t.Helper()
		require.Empty(t, p.Spec["research_subject"])
		field := p.SpecFields["research_subject"]
		require.Empty(t, field.Value)
		require.False(t, field.Provenance.Accessible)
		require.True(t, field.Provenance.NeedsReview)
		require.Empty(t, field.Provenance.Locator)
		require.Empty(t, field.Provenance.Purpose)
		require.Empty(t, field.Provenance.AuthorizationScope)
	}
	current, err := f.projects.GetProject(f.ctx, f.actor, p.ID)
	require.NoError(t, err)
	assertHidden(current)
	listed, _, err := f.projects.ListProjects(f.ctx, f.actor)
	require.NoError(t, err)
	require.Len(t, listed, 1)
	assertHidden(listed[0])
	diff, err := f.projects.ActivationDiff(f.ctx, f.actor, p.ID, 0)
	require.NoError(t, err)
	for _, field := range diff.ChangedFields {
		if field.Key == "research_subject" {
			require.Empty(t, field.Value)
		}
	}
	replay, _, replayed, err := f.projects.SaveSpec(f.ctx, f.actor, p.ID, "g1-native-spec-01", input)
	require.NoError(t, err)
	require.True(t, replayed)
	require.NoError(t, json.Unmarshal(replay, &current))
	assertHidden(current)
	candidates, err := f.projects.ListDraftCandidates(f.ctx, f.actor, p.ID)
	require.NoError(t, err)
	require.Len(t, candidates, 1)
	require.Empty(t, candidates[0].Content)
	require.Equal(t, "来源待复核", candidates[0].Title)
	require.Empty(t, candidates[0].Provenance.Purpose)
	replay, _, replayed, err = f.projects.CreateDraftCandidate(f.ctx, f.actor, p.ID, "g1-native-candidate", candidateInput)
	require.NoError(t, err)
	require.True(t, replayed)
	require.NotContains(t, string(replay), candidateInput.Content)
	require.NotContains(t, string(replay), candidateInput.Title)
	preview, err := f.projects.PreviewTemplateMigration(f.ctx, f.actor, p.ID, core.TemplateMigrationInput{ExpectedProjectVersion: p.ProjectVersion, TemplateID: p.TemplateID, TemplateVersion: p.TemplateVersion})
	require.NoError(t, err)
	for _, field := range preview.Fields {
		if field.FieldID == "research_subject" {
			require.Empty(t, field.Value)
			require.Empty(t, field.Before)
			require.Empty(t, field.After)
		}
	}
	_, _, _, err = f.projects.ActivateProject(f.ctx, f.actor, p.ID, "g1-native-denied-activate", core.ActivateProjectInput{ExpectedSpecRevision: p.SpecRevision, ExpectedProjectVersion: p.ProjectVersion, ReviewedProjectVersion: p.ProjectVersion})
	require.ErrorIs(t, err, core.ErrSourceUnavailable)
	var stored struct {
		SpecJSON         string
		SpecMetadataJSON string
	}
	require.NoError(t, f.db.Table("lingdoc_projects").Select("spec_json,spec_metadata_json").Where("id=?", p.ID).Take(&stored).Error)
	require.Contains(t, stored.SpecJSON, input.Fields["research_subject"])
	require.Contains(t, stored.SpecMetadataJSON, input.Fields["research_subject"])
	hiddenMerge := core.SaveSpecInput{ExpectedSpecRevision: p.SpecRevision, Fields: map[string]string{"research_subject": "", "research_goal": "edited human goal"}}
	_, _, _, err = f.projects.SaveSpec(f.ctx, f.actor, p.ID, "g1-native-hidden-merge", hiddenMerge)
	require.ErrorIs(t, err, core.ErrSourceUnavailable)
	require.NoError(t, f.db.Exec("UPDATE knowledge_bases SET deleted_at=NULL WHERE id=?", "kb-runtime").Error)
	current, err = f.projects.GetProject(f.ctx, f.actor, p.ID)
	require.NoError(t, err)
	require.Equal(t, input.Fields["research_subject"], current.Spec["research_subject"])
	require.NotEqual(t, context.DeadlineExceeded, f.ctx.Err())
}

func TestG1ActualSourceHistoryRemainsProtectedAfterHumanFieldChange(t *testing.T) {
	f := g1NativeProvenanceFixture(t)
	input := f.specInput("合成来源旧条件")
	raw, _, _, err := f.projects.SaveSpec(f.ctx, f.actor, f.project.ID, "g1-history-spec", input)
	require.NoError(t, err)
	var p core.Project
	require.NoError(t, json.Unmarshal(raw, &p))
	raw, _, _, err = f.projects.ActivateProject(f.ctx, f.actor, p.ID, "g1-history-activate", core.ActivateProjectInput{ExpectedSpecRevision: p.SpecRevision, ExpectedProjectVersion: p.ProjectVersion, ReviewedProjectVersion: p.ProjectVersion})
	require.NoError(t, err)
	require.NoError(t, json.Unmarshal(raw, &p))
	chapters, err := f.projects.ListChapters(f.ctx, f.actor, p.ID)
	require.NoError(t, err)
	require.NotEmpty(t, chapters)
	raw, _, _, err = f.projects.CreateChangeSet(f.ctx, f.actor, p.ID, "g1-history-change", core.CreateChangeSetInput{ExpectedContextRevision: p.CurrentContextRevision, Reason: "人工更新合成条件", Fields: map[string]core.ChangeFieldInput{"research_subject": {OldValue: input.Fields["research_subject"], NewValue: "明确人类新条件"}}, AffectedChapterIDs: []string{chapters[0].ID}})
	require.NoError(t, err)
	var change core.ChangeSet
	require.NoError(t, json.Unmarshal(raw, &change))
	_, _, _, err = f.projects.ApplyChangeSet(f.ctx, f.actor, p.ID, change.ID, "g1-history-apply")
	require.NoError(t, err)
	current, err := f.projects.GetProject(f.ctx, f.actor, p.ID)
	require.NoError(t, err)
	require.Equal(t, "明确人类新条件", current.Spec["research_subject"])
	require.Equal(t, "human", current.SpecFields["research_subject"].Origin)
	require.Nil(t, current.SpecFields["research_subject"].Provenance)
	_, _, _, err = f.projects.ReverseChangeSet(f.ctx, f.actor, p.ID, change.ID, "g1-history-inverse")
	require.NoError(t, err)
	require.NoError(t, f.db.Exec("UPDATE knowledge_bases SET deleted_at=CURRENT_TIMESTAMP WHERE id=?", "kb-runtime").Error)
	current, err = f.projects.GetProject(f.ctx, f.actor, p.ID)
	require.NoError(t, err)
	require.Equal(t, "明确人类新条件", current.Spec["research_subject"])
	read, err := f.projects.GetChangeSet(f.ctx, f.actor, p.ID, change.ID)
	require.NoError(t, err)
	require.Empty(t, read.Fields[0].OldValue)
	require.Empty(t, read.Fields[0].NewValue)
	changes, err := f.projects.ListChangeSets(f.ctx, f.actor, p.ID)
	require.NoError(t, err)
	for _, item := range changes {
		for _, field := range item.Fields {
			require.Empty(t, field.OldValue)
			require.Empty(t, field.NewValue)
		}
	}
	history, err := f.projects.ListContextRevisions(f.ctx, f.actor, p.ID)
	require.NoError(t, err)
	require.NotEmpty(t, history)
	for _, record := range history {
		if record.Revision == p.CurrentContextRevision {
			require.Empty(t, record.Spec)
		}
	}
	_, _, _, err = f.projects.ReverseChangeSet(f.ctx, f.actor, p.ID, change.ID, "g1-history-denied-inverse")
	require.ErrorIs(t, err, core.ErrSourceUnavailable)
	// Read projections above deliberately redact historical fields. A mutation
	// replay must instead reauthorize its complete immutable receipt before
	// returning it, even though the live formal field is now independent human text.
	retry := core.CreateChangeSetInput{ExpectedContextRevision: p.CurrentContextRevision, Reason: "人工更新合成条件", Fields: map[string]core.ChangeFieldInput{"research_subject": {OldValue: input.Fields["research_subject"], NewValue: "明确人类新条件"}}, AffectedChapterIDs: []string{chapters[0].ID}}
	var operationsBefore, auditsBefore int64
	require.NoError(t, f.db.Table("lingdoc_operations").Count(&operationsBefore).Error)
	require.NoError(t, f.db.Table("lingdoc_project_audits").Count(&auditsBefore).Error)
	denied, _, replayed, err := f.projects.CreateChangeSet(f.ctx, f.actor, p.ID, "g1-history-change", retry)
	require.ErrorIs(t, err, core.ErrSourceUnavailable)
	require.Empty(t, denied)
	require.False(t, replayed)
	// Revocation preserves the operation. Current source access permits the
	// same old request to recover its original receipt without rerunning it.
	require.NoError(t, f.db.Exec("UPDATE knowledge_bases SET deleted_at=NULL WHERE id=?", "kb-runtime").Error)
	restored, status, replayed, err := f.projects.CreateChangeSet(f.ctx, f.actor, p.ID, "g1-history-change", retry)
	require.NoError(t, err)
	require.True(t, replayed)
	require.Equal(t, 201, status)
	require.JSONEq(t, string(raw), string(restored))
	after, err := f.projects.GetProject(f.ctx, f.actor, p.ID)
	require.NoError(t, err)
	require.Equal(t, current.Spec, after.Spec)
	require.Equal(t, current.ProjectVersion, after.ProjectVersion)
	require.Equal(t, current.SpecRevision, after.SpecRevision)
	require.Equal(t, current.CurrentContextRevision, after.CurrentContextRevision)
	var operationsAfter, auditsAfter int64
	require.NoError(t, f.db.Table("lingdoc_operations").Count(&operationsAfter).Error)
	require.NoError(t, f.db.Table("lingdoc_project_audits").Count(&auditsAfter).Error)
	require.Equal(t, operationsBefore, operationsAfter)
	require.Equal(t, auditsBefore, auditsAfter)
	require.NotEqual(t, context.DeadlineExceeded, f.ctx.Err())
}

func TestG1ActualSourceIdentityVersionAndModelScopeFailClosed(t *testing.T) {
	f := g1NativeProvenanceFixture(t)
	raw, _, _, err := f.projects.SaveSpec(f.ctx, f.actor, f.project.ID, "g1-scope-spec", f.specInput("合成源条件"))
	require.NoError(t, err)
	var p core.Project
	require.NoError(t, json.Unmarshal(raw, &p))
	// A public snapshot hash cannot turn source-dependent fields into human ones.
	prepared, snapshot, err := f.projects.PrepareProjectProvenance(f.ctx, f.actor, p.ID, false)
	require.NoError(t, err)
	_, err = f.projects.PrepareFrozenProjectProvenance(prepared, f.actor, p.ID, p.Spec, snapshot, false)
	require.NoError(t, err)
	for i := range snapshot.Fields {
		if snapshot.Fields[i].PinID != "" {
			snapshot.Fields[i].PinID = ""
			break
		}
	}
	snapshot.Digest = ""
	forged, _ := json.Marshal(snapshot)
	hash := sha256.Sum256(forged)
	snapshot.Digest = hex.EncodeToString(hash[:])
	_, err = f.projects.PrepareFrozenProjectProvenance(f.ctx, f.actor, p.ID, p.Spec, snapshot, false)
	require.ErrorIs(t, err, core.ErrSourceUnavailable)
	mismatch := types.WithCaller(f.ctx, types.Caller{TenantID: f.actor.TenantID, UserID: "other-user", Role: f.actor.Role})
	_, err = f.projects.GetProject(mismatch, f.actor, p.ID)
	require.ErrorIs(t, err, core.ErrNotFound)
	// Only the persisted model-use decision changes; human read stays permitted.
	require.NoError(t, f.db.Exec(`UPDATE lingdoc_g2_objects SET payload_json=json_set(payload_json,'$.model_use_allowed',json('false')) WHERE project_id=? AND kind=? AND id=?`, p.ID, core.G2ImportKind, "consent-runtime").Error)
	current, err := f.projects.GetProject(f.ctx, f.actor, p.ID)
	require.NoError(t, err)
	require.NotEmpty(t, current.Spec["research_subject"])
	_, _, err = f.projects.PrepareProjectProvenance(f.ctx, f.actor, p.ID, true)
	require.ErrorIs(t, err, core.ErrSourceUnavailable)
	require.NoError(t, f.db.Exec(`UPDATE chunks SET content=content||'版本变更',content_revision=content_revision+1 WHERE id=?`, "source-runtime").Error)
	current, err = f.projects.GetProject(f.ctx, f.actor, p.ID)
	require.NoError(t, err)
	require.Empty(t, current.Spec["research_subject"])
	require.False(t, errors.Is(f.ctx.Err(), context.DeadlineExceeded))
	require.False(t, strings.Contains(current.Spec["research_subject"], "版本变更"))
}

func TestG1ActualInverseApplyRetainsSourceAuthority(t *testing.T) {
	f := g1NativeProvenanceFixture(t)
	input := f.specInput("逆向来源条件 A")
	raw, _, _, err := f.projects.SaveSpec(f.ctx, f.actor, f.project.ID, "g1-inverse-source-save", input)
	require.NoError(t, err)
	var p core.Project
	require.NoError(t, json.Unmarshal(raw, &p))
	raw, _, _, err = f.projects.ActivateProject(f.ctx, f.actor, p.ID, "g1-inverse-source-activate", core.ActivateProjectInput{ExpectedSpecRevision: p.SpecRevision, ExpectedProjectVersion: p.ProjectVersion, ReviewedProjectVersion: p.ProjectVersion})
	require.NoError(t, err)
	require.NoError(t, json.Unmarshal(raw, &p))
	chapters, err := f.projects.ListChapters(f.ctx, f.actor, p.ID)
	require.NoError(t, err)
	raw, _, _, err = f.projects.CreateChangeSet(f.ctx, f.actor, p.ID, "g1-inverse-source-create", core.CreateChangeSetInput{ExpectedContextRevision: p.CurrentContextRevision, Reason: "合成人工改为 B", Fields: map[string]core.ChangeFieldInput{"research_subject": {OldValue: input.Fields["research_subject"], NewValue: "人类条件 B"}}, AffectedChapterIDs: []string{chapters[0].ID}})
	require.NoError(t, err)
	var change core.ChangeSet
	require.NoError(t, json.Unmarshal(raw, &change))
	_, _, _, err = f.projects.ApplyChangeSet(f.ctx, f.actor, p.ID, change.ID, "g1-inverse-source-apply")
	require.NoError(t, err)
	raw, _, _, err = f.projects.ReverseChangeSet(f.ctx, f.actor, p.ID, change.ID, "g1-inverse-source-reverse")
	require.NoError(t, err)
	var inverse core.ChangeSet
	require.NoError(t, json.Unmarshal(raw, &inverse))
	_, _, _, err = f.projects.ApplyChangeSet(f.ctx, f.actor, p.ID, inverse.ID, "g1-inverse-source-restore")
	require.NoError(t, err)
	current, err := f.projects.GetProject(f.ctx, f.actor, p.ID)
	require.NoError(t, err)
	require.Equal(t, input.Fields["research_subject"], current.Spec["research_subject"])
	require.NotNil(t, current.SpecFields["research_subject"].Provenance)
	require.True(t, current.SpecFields["research_subject"].Provenance.Accessible)
	require.NoError(t, f.db.Exec("UPDATE knowledge_bases SET deleted_at=CURRENT_TIMESTAMP WHERE id=?", "kb-runtime").Error)
	current, err = f.projects.GetProject(f.ctx, f.actor, p.ID)
	require.NoError(t, err)
	require.Empty(t, current.Spec["research_subject"])
	require.False(t, current.SpecFields["research_subject"].Provenance.Accessible)
	_, err = f.projects.GenerationContext(f.ctx, f.actor, p.ID, chapters[0].ID)
	require.ErrorIs(t, err, core.ErrSourceUnavailable)
}

func TestG1ActualActionResultReplayRechecksHistoricalSource(t *testing.T) {
	f := g1NativeProvenanceFixture(t)
	input := f.specInput("动作缓存来源条件 A")
	raw, _, _, err := f.projects.SaveSpec(f.ctx, f.actor, f.project.ID, "g1-action-source-save", input)
	require.NoError(t, err)
	var p core.Project
	require.NoError(t, json.Unmarshal(raw, &p))
	raw, _, _, err = f.projects.ActivateProject(f.ctx, f.actor, p.ID, "g1-action-source-activate", core.ActivateProjectInput{ExpectedSpecRevision: p.SpecRevision, ExpectedProjectVersion: p.ProjectVersion, ReviewedProjectVersion: p.ProjectVersion})
	require.NoError(t, err)
	require.NoError(t, json.Unmarshal(raw, &p))
	chapters, err := f.projects.ListChapters(f.ctx, f.actor, p.ID)
	require.NoError(t, err)
	raw, _, _, err = f.projects.CreateChangeSet(f.ctx, f.actor, p.ID, "g1-action-source-change", core.CreateChangeSetInput{ExpectedContextRevision: p.CurrentContextRevision, Reason: "合成缓存变更", Fields: map[string]core.ChangeFieldInput{"research_subject": {OldValue: input.Fields["research_subject"], NewValue: "动作后人类条件 B"}}, AffectedChapterIDs: []string{chapters[0].ID}})
	require.NoError(t, err)
	var change core.ChangeSet
	require.NoError(t, json.Unmarshal(raw, &change))
	expectation := core.G2Expectation{ExpectedProjectVersion: p.ProjectVersion, ExpectedContextRevision: p.CurrentContextRevision}
	raw, _, _, err = f.projects.CreateActionProposal(f.ctx, f.actor, p.ID, "g1-action-source-proposal", core.CreateActionProposalInput{G2Expectation: expectation, Action: "apply_change_set", TargetID: change.ID, Input: json.RawMessage(`{}`), ExpectedEffect: "合成实际领域变更"})
	require.NoError(t, err)
	var proposal core.ActionProposal
	require.NoError(t, json.Unmarshal(raw, &proposal))
	expectation.ExpectedVersion = proposal.Version
	confirm := core.G2ConfirmedInput{G2Expectation: expectation, Confirmed: true, Reason: "合成账号确认实际 apply"}
	raw, _, _, err = f.projects.ConfirmActionProposal(f.ctx, f.actor, p.ID, proposal.ID, "g1-action-source-confirm", confirm)
	require.NoError(t, err)
	require.Contains(t, string(raw), input.Fields["research_subject"])
	require.NoError(t, f.db.Exec("UPDATE knowledge_bases SET deleted_at=CURRENT_TIMESTAMP WHERE id=?", "kb-runtime").Error)
	actions, err := f.projects.ListActionProposals(f.ctx, f.actor, p.ID)
	require.NoError(t, err)
	require.Len(t, actions, 1)
	require.Equal(t, "restricted", actions[0].Status)
	require.Empty(t, actions[0].Result)
	replay, _, replayed, err := f.projects.ConfirmActionProposal(f.ctx, f.actor, p.ID, proposal.ID, "g1-action-source-confirm", confirm)
	require.NoError(t, err)
	require.True(t, replayed)
	require.NotContains(t, string(replay), input.Fields["research_subject"])
	var row struct{ PayloadJSON string }
	require.NoError(t, f.db.Table("lingdoc_g2_objects").Select("payload_json").Where("project_id=? AND kind=? AND id=?", p.ID, core.G2ActionKind, proposal.ID).Take(&row).Error)
	require.Contains(t, row.PayloadJSON, input.Fields["research_subject"])
}

func TestG1ActualExplicitHumanSameValueCannotEraseSourcePin(t *testing.T) {
	f := g1NativeProvenanceFixture(t)
	input := f.specInput("仍受来源授权约束的条件")
	raw, _, _, err := f.projects.SaveSpec(f.ctx, f.actor, f.project.ID, "g1-no-launder-source", input)
	require.NoError(t, err)
	var p core.Project
	require.NoError(t, json.Unmarshal(raw, &p))
	require.NoError(t, f.db.Exec("UPDATE knowledge_bases SET deleted_at=CURRENT_TIMESTAMP WHERE id=?", "kb-runtime").Error)
	raw, _, _, err = f.projects.SaveSpec(f.ctx, f.actor, p.ID, "g1-no-launder-human", core.SaveSpecInput{ExpectedSpecRevision: p.SpecRevision, Fields: input.Fields, FieldMetadata: map[string]core.SpecFieldInput{"research_subject": {Origin: "human", Status: "draft"}}})
	require.NoError(t, err)
	require.NoError(t, json.Unmarshal(raw, &p))
	require.Empty(t, p.Spec["research_subject"])
	require.NotNil(t, p.SpecFields["research_subject"].Provenance)
	current, err := f.projects.GetProject(f.ctx, f.actor, p.ID)
	require.NoError(t, err)
	require.Empty(t, current.Spec["research_subject"])
}
