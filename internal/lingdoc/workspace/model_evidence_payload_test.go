package workspace

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/Tencent/WeKnora/internal/evidence"
	"github.com/Tencent/WeKnora/internal/lingdoc/generation"
	core "github.com/Tencent/WeKnora/internal/lingdoc/workspacecore"
	"github.com/Tencent/WeKnora/internal/types"
	"github.com/stretchr/testify/require"
)

func TestModelEvidencePayloadRejectsUnselectedAndUnprovidedSource(t *testing.T) {
	ref := generation.EvidenceCardRef{ID: "selected-card", Version: 2}
	card := generation.EvidenceConstraint{CardRef: ref, SupportType: "refutes", Scope: "仅限合成场景", Limitations: "不能外推", SourceIDs: []string{"source-1"}}
	got, err := modelEvidencePayload([]generation.EvidenceCardRef{ref}, []generation.EvidenceConstraint{card}, []string{"source-1"})
	require.NoError(t, err)
	require.Equal(t, []generation.EvidenceConstraint{card}, got)
	got[0].SourceIDs[0] = "mutated"
	require.Equal(t, "source-1", card.SourceIDs[0])
	wrong := card
	wrong.CardRef.ID = "unselected-private-card"
	_, err = modelEvidencePayload([]generation.EvidenceCardRef{ref}, []generation.EvidenceConstraint{wrong}, []string{"source-1"})
	require.ErrorIs(t, err, generation.ErrSourceAccessDenied)
	wrong = card
	wrong.SourceIDs = []string{"unprovided-source"}
	_, err = modelEvidencePayload([]generation.EvidenceCardRef{ref}, []generation.EvidenceConstraint{wrong}, []string{"source-1"})
	require.ErrorIs(t, err, generation.ErrSourceAccessDenied)
	_, err = modelEvidencePayload(nil, []generation.EvidenceConstraint{card}, []string{"source-1"})
	require.ErrorIs(t, err, generation.ErrSourceAccessDenied)
}

func TestActualModelAdaptersTransmitOnlySelectedCardContract(t *testing.T) {
	ref := generation.EvidenceCardRef{ID: "selected-card", Version: 2}
	cards := []generation.EvidenceConstraint{{CardRef: ref, SupportType: "refutes", Scope: "仅限合成场景", Limitations: "不能外推", SourceIDs: []string{"source-1"}, Anchors: []generation.EvidenceAnchor{{ID: "anchor", Version: 1, SourceID: "source-1", Locator: "page 1", SelectionEnd: 1, QuotedTextHash: "hash"}}}}
	for _, kind := range []string{"generation", "rewrite"} {
		t.Run(kind, func(t *testing.T) {
			result := `{"body_markdown":"合成正文 [[source:source-1]]","source_ids":["source-1"],"review_items":[],"claims":[{"text":"合成正文 [[source:source-1]]","references":[{"card_id":"selected-card","card_version":2,"anchor_id":"anchor","anchor_version":1,"source_id":"source-1"}]}]}`
			if kind == "rewrite" {
				result = `{"replacement_markdown":"合成改写 [[source:source-1]]","source_ids":["source-1"],"review_items":[]}`
			}
			model := &acceptanceChatStub{response: &types.ChatResponse{Content: result, FinishReason: "stop", Usage: types.TokenUsage{PromptTokens: 100, CompletionTokens: 30}}}
			service := &acceptanceModelServiceStub{models: []*types.Model{acceptanceDeepSeekModel()}, chatModel: model}
			if kind == "generation" {
				input := acceptanceGenerationInput()
				input.EvidenceCardRefs = []generation.EvidenceCardRef{ref}
				input.EvidenceConstraints = cards
				draft, err := (generationHostModel{models: service, templates: core.ContractDemoTemplate{}}).Generate(context.Background(), input)
				require.NoError(t, err)
				require.Len(t, draft.Claims, 1)
				require.Equal(t, "anchor", draft.Claims[0].References[0].AnchorID)
			} else {
				prompt := SelectedRewritePrompt{Instruction: "只改选段", EvidenceCardRefs: []generation.EvidenceCardRef{ref}, EvidenceConstraints: cards, Sources: []evidence.Source{{ID: "source-1", QuotedText: "已许可的合成来源片段"}}}
				_, err := (selectedRewriteHostModel{models: service}).RewriteSelected(context.Background(), Actor{TenantID: 1, UserID: "synthetic"}, prompt)
				require.NoError(t, err)
			}
			require.Len(t, model.messages, 2)
			var payload map[string]json.RawMessage
			require.NoError(t, json.Unmarshal([]byte(model.messages[1].Content), &payload))
			var sent []generation.EvidenceConstraint
			require.NoError(t, json.Unmarshal(payload["evidence_cards"], &sent))
			require.Equal(t, cards, sent)
			require.NotContains(t, payload, "template_definition")
			require.NotContains(t, payload, "all_cards")
			require.NotContains(t, payload, "claims")
			var fields []map[string]json.RawMessage
			require.NoError(t, json.Unmarshal(payload["evidence_cards"], &fields))
			require.ElementsMatch(t, []string{"card_ref", "support_type", "scope", "limitations", "source_ids", "anchors"}, keysOfEvidencePayload(fields[0]))
		})
	}
}
func keysOfEvidencePayload(value map[string]json.RawMessage) []string {
	out := []string{}
	for key := range value {
		out = append(out, key)
	}
	return out
}
