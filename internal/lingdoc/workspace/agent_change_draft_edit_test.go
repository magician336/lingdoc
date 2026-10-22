package workspace

import (
	"encoding/json"
	"testing"

	"github.com/Tencent/WeKnora/internal/lingdoc/delivery"
	core "github.com/Tencent/WeKnora/internal/lingdoc/workspacecore"
	"github.com/stretchr/testify/require"
)

type agentDraftHistoryRead struct {
	Version     int64
	PayloadJSON string
}

func agentReadDraftHistory(t *testing.T, f deliveryGuardFixture, id string) (agentDraftHistoryRead, map[string]core.SpecProvenanceRef, map[string]string) {
	t.Helper()
	var row agentDraftHistoryRead
	require.NoError(t, f.db.Table("lingdoc_g5_records").Select("version,payload_json").Where("project_id=? AND kind='g1_change_provenance' AND id=?", f.project.ID, id).Take(&row).Error)
	var history struct {
		Fields  map[string]core.SpecProvenanceRef `json:"proposed_fields"`
		Origins map[string]string                 `json:"proposed_origins"`
	}
	require.NoError(t, json.Unmarshal([]byte(row.PayloadJSON), &history))
	return row, history.Fields, history.Origins
}
func agentDraftPinCount(t *testing.T, f deliveryGuardFixture) int64 {
	t.Helper()
	var count int64
	require.NoError(t, f.db.Table("lingdoc_g5_records").Where("project_id=? AND kind='g1_provenance'", f.project.ID).Count(&count).Error)
	return count
}
func agentConfirmPreparedDraft(t *testing.T, f deliveryGuardFixture, proposal core.ActionProposal) core.ChangeSet {
	t.Helper()
	p, err := f.projects.GetProject(f.ctx, f.actor, f.project.ID)
	require.NoError(t, err)
	raw, _, _, err := f.projects.ConfirmActionProposal(f.ctx, f.actor, p.ID, proposal.ID, "agent-edit-initial-confirm", core.ConfirmationInput{VersionExpectation: agentDraftExpectation(p, proposal.Version), Confirmed: true, Reason: "真人仅确认建草稿"})
	require.NoError(t, err)
	var completed core.ActionProposal
	require.NoError(t, json.Unmarshal(raw, &completed))
	require.Equal(t, "completed", completed.Status)
	var draft core.ChangeSet
	require.NoError(t, json.Unmarshal(completed.Result, &draft))
	require.Equal(t, "draft", draft.Status)
	return draft
}
func agentReopenDraftService(t *testing.T, f deliveryGuardFixture) *core.Service {
	t.Helper()
	// A new application service and repository read the same production DB;
	// no in-memory proposal object is used as the persisted readback source.
	runtime := NewGORMSourceRuntime(f.db, ProjectPorts{}, adapterSharesStub{}, nil, delivery.NewFixedTemplateReader())
	projects := core.NewServiceWithSources(core.NewGORMRepository(f.db), core.ContractDemoTemplate{}, runtime.WorkspaceSourcePolicy())
	runtime.ConnectProjects(ProjectPorts{Authorizer: projects, Projects: projects, Context: projects})
	connectAggregateCapabilities(runtime, projects)
	runtime.EnableFullWorkflowPolicy()
	projects.ConfigureAgentRuntime(core.AgentRuntime{Sources: NewResearchAdapter(f.db, projects, runtime, adapterSharesStub{}, nil, nil)})
	governance, err := NewTemplateGovernanceRuntime(f.db, projects, runtime, nil)
	require.NoError(t, err)
	projects.SetTemplateCatalogResolver(governance)
	projects.SetImpactRuleChecker(governance.ImpactRuleChecker())
	projects.SetRuntimeMode("real")
	NewFullWorkflowGuard(fullWorkflowGuardPortsFor(projects), runtime)
	return projects
}

func TestAgentChangeDraftServerCanonicalizesForgedOrigin(t *testing.T) {
	for _, edited := range []bool{false, true} {
		name := "exact_model_value"
		if edited {
			name = "human_edited_value"
		}
		t.Run(name, func(t *testing.T) {
			f := newDeliveryGuardFixture(t, false)
			_, input, _ := agentDraftModelTurn(t, f)
			metadata := input.FieldMetadata["research_goal"]
			expected := "ai_generated"
			metadata.Origin = "ai_assisted_human" // Forge the opposite label.
			if edited {
				delta := input.Fields["research_goal"]
				delta.NewValue += " 人工另加范围"
				input.Fields["research_goal"] = delta
				metadata.Origin = "ai_generated"
				expected = "ai_assisted_human"
			}
			input.FieldMetadata["research_goal"] = metadata
			proposal := agentDraftPropose(t, f, input)
			var stored core.AgentChangeDraftInput
			require.NoError(t, json.Unmarshal(proposal.Input, &stored))
			require.Equal(t, expected, stored.FieldMetadata["research_goal"].Origin, "client origin is never authoritative")
			draft := agentConfirmPreparedDraft(t, f, proposal)
			_, _, origins := agentReadDraftHistory(t, f, draft.ID)
			require.Equal(t, expected, origins["research_goal"])
			_, _, _, err := f.projects.AssessChangeSet(f.ctx, f.actor, f.project.ID, draft.ID, "agent-forged-origin-assess")
			require.NoError(t, err)
			_, _, _, err = f.projects.ApplyChangeSet(f.ctx, f.actor, f.project.ID, draft.ID, "agent-forged-origin-apply")
			require.NoError(t, err)
			p, err := f.projects.GetProject(f.ctx, f.actor, f.project.ID)
			require.NoError(t, err)
			require.Equal(t, expected, p.SpecFields["research_goal"].Origin)
		})
	}
}

func TestAgentChangeDraftEditReadReopenAssessApplyKeepsAIProvenance(t *testing.T) {
	f := newDeliveryGuardFixture(t, false)
	turn, input, _ := agentDraftModelTurn(t, f)
	draft := agentConfirmPreparedDraft(t, f, agentDraftPropose(t, f, input))
	_, oldPins, _ := agentReadDraftHistory(t, f, draft.ID)
	edit := input.ExtendedChangeSetInput
	field := edit.Fields["research_goal"]
	field.NewValue += "；草稿编辑补充适用范围"
	edit.Fields["research_goal"] = field
	edit.Reason = "人工复核候选后编辑新值"
	raw, _, _, err := f.projects.EditChangeDraft(f.ctx, f.actor, f.project.ID, draft.ID, "agent-draft-real-edit", edit)
	require.NoError(t, err)
	var edited core.ChangeSet
	require.NoError(t, json.Unmarshal(raw, &edited))
	require.Equal(t, "draft", edited.Status)
	require.Equal(t, field.NewValue, edited.Fields[0].NewValue)
	_, newPins, origins := agentReadDraftHistory(t, f, draft.ID)
	require.NotEqual(t, oldPins["research_goal"].PinID, newPins["research_goal"].PinID)
	require.NotEqual(t, oldPins["research_goal"].ValueHash, newPins["research_goal"].ValueHash)
	require.Equal(t, "ai_assisted_human", origins["research_goal"])
	pinsAfterEdit := agentDraftPinCount(t, f)
	_, _, replay, err := f.projects.EditChangeDraft(f.ctx, f.actor, f.project.ID, draft.ID, "agent-draft-real-edit", edit)
	require.NoError(t, err)
	require.True(t, replay)
	require.Equal(t, pinsAfterEdit, agentDraftPinCount(t, f), "edit replay must not mint another pin")
	listed, err := f.projects.ListChangeSets(f.ctx, f.actor, f.project.ID)
	require.NoError(t, err)
	require.Len(t, listed, 1)
	require.Equal(t, field.NewValue, listed[0].Fields[0].NewValue)
	reopened := agentReopenDraftService(t, f)
	listed, err = reopened.ListChangeSets(f.ctx, f.actor, f.project.ID)
	require.NoError(t, err)
	require.Len(t, listed, 1)
	require.Equal(t, field.NewValue, listed[0].Fields[0].NewValue)
	p, err := reopened.GetProject(f.ctx, f.actor, f.project.ID)
	require.NoError(t, err)
	require.Equal(t, f.project.Spec, p.Spec, "edit changes a draft, not the formal Spec")
	require.Equal(t, f.project.CurrentContextRevision, p.CurrentContextRevision)
	_, _, _, err = reopened.AssessChangeSet(f.ctx, f.actor, p.ID, draft.ID, "agent-edited-draft-assess")
	require.NoError(t, err)
	_, _, _, err = reopened.ApplyChangeSet(f.ctx, f.actor, p.ID, draft.ID, "agent-edited-draft-apply")
	require.NoError(t, err)
	applied, err := reopened.GetProject(f.ctx, f.actor, p.ID)
	require.NoError(t, err)
	require.Equal(t, field.NewValue, applied.Spec["research_goal"])
	require.Equal(t, "ai_assisted_human", applied.SpecFields["research_goal"].Origin)
	require.Equal(t, "pending_confirmation", applied.SpecFields["research_goal"].Status)
	require.NotNil(t, applied.SpecFields["research_goal"].Provenance)
	require.Equal(t, turn.ID, applied.SpecFields["research_goal"].Provenance.SourceID)
	require.Equal(t, "agent_turn", applied.SpecFields["research_goal"].Provenance.SourceType)
	require.Equal(t, f.project.SpecRevision+1, applied.SpecRevision)
}

func TestAgentChangeDraftReasonOnlyAndRemovedAIFieldDoNotCreatePins(t *testing.T) {
	f := newDeliveryGuardFixture(t, false)
	_, input, _ := agentDraftModelTurn(t, f)
	draft := agentConfirmPreparedDraft(t, f, agentDraftPropose(t, f, input))
	oldHistory, oldPins, _ := agentReadDraftHistory(t, f, draft.ID)
	before := agentDraftPinCount(t, f)
	edit := input.ExtendedChangeSetInput
	edit.Reason = "只编辑说明，不编辑新值"
	_, _, _, err := f.projects.EditChangeDraft(f.ctx, f.actor, f.project.ID, draft.ID, "agent-draft-reason-only", edit)
	require.NoError(t, err)
	history, pins, _ := agentReadDraftHistory(t, f, draft.ID)
	require.Equal(t, oldHistory, history, "reason-only edit must not rewrite provenance history")
	require.Equal(t, oldPins, pins)
	require.Equal(t, before, agentDraftPinCount(t, f))
	// Delete the proposed AI field while keeping a separate manual condition
	// change. The removed AI new value must never be applied or relabelled.
	edit.Fields = map[string]core.ChangeFieldInput{"research_subject": {OldValue: f.project.Spec["research_subject"], NewValue: "人工输入的另一研究对象"}}
	edit.Reason = "放弃 AI 条件，只保留人工条件修改"
	_, _, _, err = f.projects.EditChangeDraft(f.ctx, f.actor, f.project.ID, draft.ID, "agent-draft-remove-ai-field", edit)
	require.NoError(t, err)
	_, pins, origins := agentReadDraftHistory(t, f, draft.ID)
	require.Empty(t, pins)
	require.Empty(t, origins)
	require.Equal(t, before, agentDraftPinCount(t, f))
	reopened := agentReopenDraftService(t, f)
	values, err := reopened.ListChangeSets(f.ctx, f.actor, f.project.ID)
	require.NoError(t, err)
	require.Len(t, values, 1)
	require.Len(t, values[0].Fields, 1)
	require.Equal(t, "research_subject", values[0].Fields[0].Key)
	_, _, _, err = reopened.AssessChangeSet(f.ctx, f.actor, f.project.ID, draft.ID, "agent-removed-field-assess")
	require.NoError(t, err)
	_, _, _, err = reopened.ApplyChangeSet(f.ctx, f.actor, f.project.ID, draft.ID, "agent-removed-field-apply")
	require.NoError(t, err)
	p, err := reopened.GetProject(f.ctx, f.actor, f.project.ID)
	require.NoError(t, err)
	require.Equal(t, f.project.Spec["research_goal"], p.Spec["research_goal"])
}

func TestAgentChangeDraftEditPinFailureRollsBackFieldHistoryAndOperation(t *testing.T) {
	f := newDeliveryGuardFixture(t, false)
	_, input, _ := agentDraftModelTurn(t, f)
	draft := agentConfirmPreparedDraft(t, f, agentDraftPropose(t, f, input))
	oldHistory, _, _ := agentReadDraftHistory(t, f, draft.ID)
	before := agentDraftPinCount(t, f)
	edit := input.ExtendedChangeSetInput
	field := edit.Fields["research_goal"]
	field.NewValue += "失败编辑"
	edit.Fields["research_goal"] = field
	edit.Reason = "此事务必须全部回滚"
	require.NoError(t, f.db.Exec(`CREATE TRIGGER agent_edit_pin_fail BEFORE UPDATE ON lingdoc_g5_records WHEN NEW.kind='g1_change_provenance' BEGIN SELECT RAISE(ABORT,'synthetic edit pin failure'); END`).Error)
	key := "agent-draft-failing-edit"
	_, _, _, err := f.projects.EditChangeDraft(f.ctx, f.actor, f.project.ID, draft.ID, key, edit)
	require.Error(t, err)
	history, _, _ := agentReadDraftHistory(t, f, draft.ID)
	require.Equal(t, oldHistory, history)
	require.Equal(t, before, agentDraftPinCount(t, f))
	listed, err := f.projects.ListChangeSets(f.ctx, f.actor, f.project.ID)
	require.NoError(t, err)
	require.Len(t, listed, 1)
	require.Equal(t, draft.Fields, listed[0].Fields)
	require.Equal(t, draft.Reason, listed[0].Reason)
	var operations int64
	require.NoError(t, f.db.Table("lingdoc_operations").Where("operation=? AND target=? AND key=?", "editChangeDraft", f.project.ID+"/"+draft.ID, key).Count(&operations).Error)
	require.Zero(t, operations)
}

func TestAgentChangeDraftEditRechecksCurrentSourceAndActor(t *testing.T) {
	for _, name := range []string{"other_actor", "source_revoked", "turn_version_mutated"} {
		t.Run(name, func(t *testing.T) {
			f := newDeliveryGuardFixture(t, false, name == "source_revoked")
			turn, input, _ := agentDraftModelTurn(t, f)
			draft := agentConfirmPreparedDraft(t, f, agentDraftPropose(t, f, input))
			oldHistory, _, _ := agentReadDraftHistory(t, f, draft.ID)
			before := agentDraftPinCount(t, f)
			edit := input.ExtendedChangeSetInput
			field := edit.Fields["research_goal"]
			field.NewValue += "不应落账"
			edit.Fields["research_goal"] = field
			ctx, actor := f.ctx, f.actor
			switch name {
			case "source_revoked":
				require.NoError(t, f.db.Exec("UPDATE knowledge_bases SET deleted_at=CURRENT_TIMESTAMP WHERE id='kb-runtime'").Error)
			case "turn_version_mutated":
				require.NoError(t, f.db.Table("lingdoc_g2_objects").Where("project_id=? AND kind=? AND id=?", f.project.ID, core.AgentTurnKind, turn.ID).Update("version", turn.Version+1).Error)
			case "other_actor":
				actor.UserID = "unrelated-user"
				ctx = f.requestContext(actor.UserID, actor.Role, "agent-edit-other-actor")
			}
			_, _, _, err := f.projects.EditChangeDraft(ctx, actor, f.project.ID, draft.ID, "agent-draft-denied-edit", edit)
			require.Error(t, err)
			history, _, _ := agentReadDraftHistory(t, f, draft.ID)
			require.Equal(t, oldHistory, history)
			require.Equal(t, before, agentDraftPinCount(t, f))
		})
	}
}

func TestAgentChangeDraftEditReplayAfterApplyRequiresCurrentAccess(t *testing.T) {
	for _, scenario := range []string{"allowed", "source_revoked", "write_permission_revoked"} {
		t.Run(scenario, func(t *testing.T) {
			f := newDeliveryGuardFixture(t, false, scenario == "source_revoked")
			_, input, _ := agentDraftModelTurn(t, f)
			draft := agentConfirmPreparedDraft(t, f, agentDraftPropose(t, f, input))
			edit := input.ExtendedChangeSetInput
			field := edit.Fields["research_goal"]
			field.NewValue += "；正式应用后仍可核对原编辑回执"
			edit.Fields["research_goal"] = field
			edit.Reason = "合成 AI 草稿幂等重放回归"
			key := "agent-edit-applied-replay"
			original, originalStatus, replay, err := f.projects.EditChangeDraft(f.ctx, f.actor, f.project.ID, draft.ID, key, edit)
			require.NoError(t, err)
			require.False(t, replay)
			_, _, _, err = f.projects.AssessChangeSet(f.ctx, f.actor, f.project.ID, draft.ID, "agent-replay-assess")
			require.NoError(t, err)
			_, _, _, err = f.projects.ApplyChangeSet(f.ctx, f.actor, f.project.ID, draft.ID, "agent-replay-apply")
			require.NoError(t, err)
			applied, err := f.projects.GetProject(f.ctx, f.actor, f.project.ID)
			require.NoError(t, err)
			require.Equal(t, field.NewValue, applied.Spec["research_goal"])
			pins := agentDraftPinCount(t, f)
			history, _, _ := agentReadDraftHistory(t, f, draft.ID)
			var operations int64
			require.NoError(t, f.db.Table("lingdoc_operations").Count(&operations).Error)
			var audits int64
			require.NoError(t, f.db.Table("lingdoc_project_audits").Where("project_id=?", f.project.ID).Count(&audits).Error)
			assertNoWrites := func() {
				require.Equal(t, pins, agentDraftPinCount(t, f))
				currentHistory, _, _ := agentReadDraftHistory(t, f, draft.ID)
				require.Equal(t, history, currentHistory)
				var currentOperations int64
				require.NoError(t, f.db.Table("lingdoc_operations").Count(&currentOperations).Error)
				require.Equal(t, operations, currentOperations)
				var currentAudits int64
				require.NoError(t, f.db.Table("lingdoc_project_audits").Where("project_id=?", f.project.ID).Count(&currentAudits).Error)
				require.Equal(t, audits, currentAudits)
			}
			// Same identity and payload replay the original draft-edit response,
			// even though the live ChangeSet and formal revision now advanced.
			readback, status, replay, err := f.projects.EditChangeDraft(f.ctx, f.actor, f.project.ID, draft.ID, key, edit)
			require.NoError(t, err)
			require.True(t, replay)
			require.Equal(t, originalStatus, status)
			require.JSONEq(t, string(original), string(readback))
			assertNoWrites()
			// Cache-miss validation remains inside the unchanged native callback.
			_, _, replay, err = f.projects.EditChangeDraft(f.ctx, f.actor, f.project.ID, draft.ID, "agent-edit-new-key-old-payload", edit)
			require.Error(t, err)
			require.False(t, replay)
			assertNoWrites()
			current, err := f.projects.GetProject(f.ctx, f.actor, f.project.ID)
			require.NoError(t, err)
			require.Equal(t, applied.Spec, current.Spec)
			require.Equal(t, applied.SpecRevision, current.SpecRevision)
			require.Equal(t, applied.CurrentContextRevision, current.CurrentContextRevision)
			switch scenario {
			case "allowed":
				return
			case "source_revoked":
				require.NoError(t, f.db.Exec("UPDATE knowledge_bases SET deleted_at=CURRENT_TIMESTAMP WHERE id='kb-runtime'").Error)
			case "write_permission_revoked":
				require.NoError(t, f.db.Exec("DELETE FROM lingdoc_member_permissions WHERE project_id=? AND user_id=?", f.project.ID, f.actor.UserID).Error)
				require.NoError(t, f.db.Exec("DELETE FROM lingdoc_members WHERE project_id=? AND user_id=?", f.project.ID, f.actor.UserID).Error)
			}
			denied, _, replay, err := f.projects.EditChangeDraft(f.ctx, f.actor, f.project.ID, draft.ID, key, edit)
			require.Error(t, err, "cached editing response must not bypass current source/write authorization")
			require.False(t, replay)
			require.Empty(t, denied)
			assertNoWrites()
		})
	}
}
