package workspace

import (
	"context"
	"encoding/json"
	"errors"
	"reflect"
	"strings"
	"testing"

	"github.com/Tencent/WeKnora/internal/evidence"
	"github.com/Tencent/WeKnora/internal/lingdoc/generation"
	"github.com/Tencent/WeKnora/internal/lingdoc/sourcepolicy"
	core "github.com/Tencent/WeKnora/internal/lingdoc/workspacecore"
	"github.com/Tencent/WeKnora/internal/models/chat"
	"github.com/Tencent/WeKnora/internal/types"
)

type scopedGenerationModels struct {
	*acceptanceModelServiceStub
	check func(context.Context)
}

func (s scopedGenerationModels) ListModels(ctx context.Context) ([]*types.Model, error) {
	s.check(ctx)
	return s.acceptanceModelServiceStub.ListModels(ctx)
}

func (s scopedGenerationModels) GetChatModel(ctx context.Context, id string) (chat.Chat, error) {
	s.check(ctx)
	return s.acceptanceModelServiceStub.GetChatModel(ctx, id)
}

type scopedGenerationChat struct {
	*acceptanceChatStub
	check func(context.Context)
}

func (s scopedGenerationChat) Chat(ctx context.Context, messages []chat.Message, options *chat.ChatOptions) (*types.ChatResponse, error) {
	s.check(ctx)
	return s.acceptanceChatStub.Chat(ctx, messages, options)
}

type forbiddenGenerationCatalog struct{ t *testing.T }

func (r forbiddenGenerationCatalog) Get(string, string) (core.Template, error) {
	r.t.Fatal("generation re-read the catalog instead of using the project-bound template")
	return core.Template{}, generation.ErrDependencyUnavailable
}

func TestGenerationHostKeepsBoundTemplateEvidenceAndAuthenticatedContext(t *testing.T) {
	for _, test := range []struct {
		name    string
		catalog core.TemplateReader
	}{
		{name: "no catalog dependency"},
		{name: "bound copy takes precedence", catalog: forbiddenGenerationCatalog{t: t}},
	} {
		t.Run(test.name, func(t *testing.T) {
			input := acceptanceGenerationInput()
			input.Actor.Role = types.TenantRoleOwner
			input.TemplateDefinition = &core.Template{ID: "project-copy", Version: "7", Sections: []core.Section{{ID: "project-only-section", Title: "项目副本专属章节"}}}
			input.Workspace.Chapter.SectionID = "project-only-section"
			ref := generation.EvidenceCardRef{ID: "selected-card", Version: 2}
			cards := []generation.EvidenceConstraint{{CardRef: ref, SupportType: "refutes", Scope: "合成范围", Limitations: "不能外推", SourceIDs: []string{"source-1"}}}
			input.EvidenceCardRefs, input.EvidenceConstraints = []generation.EvidenceCardRef{ref}, cards
			caller := types.Caller{TenantID: input.Actor.TenantID, UserID: input.Actor.UserID, Role: types.TenantRoleContributor}
			principal := types.Principal{Type: types.PrincipalAPITenant, ID: "key-44"}
			ctx := types.WithCaller(context.Background(), caller)
			ctx = types.WithPrincipal(ctx, principal)
			ctx = types.WithTenantAPIKeyScope(ctx, types.TenantAPIKeyScope{KeyID: 44, ScopeType: types.APIKeyScopeTenant, KnowledgeBaseIDs: types.StringArray{"kb-only"}, Capabilities: types.StringArray{string(types.APIKeyCapabilityRetrieve)}})
			scope, _ := types.TenantAPIKeyScopeFromContext(ctx)
			calls := 0
			check := func(seen context.Context) {
				t.Helper()
				calls++
				gotPrincipal, _ := types.PrincipalFromContext(seen)
				gotScope, _ := types.TenantAPIKeyScopeFromContext(seen)
				if types.CallerFromContext(seen) != caller || !reflect.DeepEqual(gotPrincipal, principal) || !reflect.DeepEqual(gotScope, scope) {
					t.Fatal("model dispatch changed authenticated caller, principal, or API key grants")
				}
			}
			model := &acceptanceChatStub{response: &types.ChatResponse{Content: `{"body_markdown":"合成正文 [[source:source-1]]","source_ids":["source-1"],"review_items":[]}`}}
			models := scopedGenerationModels{acceptanceModelServiceStub: &acceptanceModelServiceStub{models: []*types.Model{acceptanceDeepSeekModel()}, chatModel: scopedGenerationChat{acceptanceChatStub: model, check: check}}, check: check}
			_, err := (generationHostModel{models: models, templates: test.catalog}).Generate(ctx, input)
			if err != nil {
				t.Fatal(err)
			}
			if calls != 3 {
				t.Fatalf("context checks=%d want=3", calls)
			}
			if len(model.messages) != 2 || !strings.HasSuffix(model.messages[0].Content, g2EvidenceModelInstructions) {
				t.Fatal("generation omitted the evidence-card instructions")
			}
			var payload struct {
				SectionTitle  string                          `json:"section_title"`
				EvidenceCards []generation.EvidenceConstraint `json:"evidence_cards"`
			}
			if err := json.Unmarshal([]byte(model.messages[1].Content), &payload); err != nil {
				t.Fatal(err)
			}
			if payload.SectionTitle != "项目副本专属章节" || !reflect.DeepEqual(payload.EvidenceCards, cards) {
				t.Fatalf("project template or selected evidence was replaced: %+v", payload)
			}
		})
	}
}

func TestGenerationHostRejectsUnselectedEvidenceBeforeModelCall(t *testing.T) {
	input := acceptanceGenerationInput()
	input.EvidenceCardRefs = []generation.EvidenceCardRef{{ID: "selected-card", Version: 2}}
	input.EvidenceConstraints = []generation.EvidenceConstraint{{CardRef: generation.EvidenceCardRef{ID: "unselected-card", Version: 2}, SourceIDs: []string{"source-1"}}}
	model := &acceptanceChatStub{}
	models := &acceptanceModelServiceStub{models: []*types.Model{acceptanceDeepSeekModel()}, chatModel: model}
	_, err := (generationHostModel{models: models, templates: core.ContractDemoTemplate{}}).Generate(context.Background(), input)
	if !errors.Is(err, generation.ErrSourceAccessDenied) || model.messages != nil {
		t.Fatalf("unselected evidence reached model: err=%v messages=%v", err, model.messages)
	}
}

type fixedSourceReviewer []sourcepolicy.Verdict

func (r fixedSourceReviewer) Review(context.Context, string, evidence.Actor, []string) ([]sourcepolicy.Verdict, error) {
	return r, nil
}

func TestGenerationSharedReviewerPinsQueuedSourceSnapshot(t *testing.T) {
	source := evidence.Source{ID: "source-1", ProjectID: "project-1", AssetID: "asset-1", AssetRevision: 1, Locator: "chunk:0", QuotedText: "budget", Status: evidence.SourceAvailable, Anchor: evidence.Anchor{KnowledgeID: "knowledge-1", ChunkID: "source-1", StartAt: 0, EndAt: 6}}
	for _, test := range []struct {
		name   string
		mutate func(*sourcepolicy.Verdict)
		want   error
	}{
		{name: "same captured source"},
		{name: "asset advanced under same source ID", mutate: func(v *sourcepolicy.Verdict) { v.Source.AssetRevision++ }, want: generation.ErrSourceStale},
		{name: "quoted text changed", mutate: func(v *sourcepolicy.Verdict) { v.Source.QuotedText = "changed" }, want: generation.ErrSourceStale},
		{name: "anchor changed", mutate: func(v *sourcepolicy.Verdict) { v.Source.Anchor.EndAt++ }, want: generation.ErrSourceStale},
		{name: "different reviewed source ID", mutate: func(v *sourcepolicy.Verdict) { v.SourceID = "other-source" }, want: generation.ErrSourceAccessDenied},
		{name: "different source projection ID", mutate: func(v *sourcepolicy.Verdict) { v.Source.ID = "other-source" }, want: generation.ErrSourceStale},
	} {
		t.Run(test.name, func(t *testing.T) {
			verdict := sourcepolicy.Verdict{SourceID: source.ID, Source: source, Usable: true}
			if test.mutate != nil {
				test.mutate(&verdict)
			}
			err := (generationSourceValidator{reviewer: fixedSourceReviewer{verdict}}).ValidateGenerationSources(context.Background(), generation.Actor{TenantID: 7, UserID: "reader"}, "project-1", []generation.Source{generationSourceFromEvidence(source)})
			if !errors.Is(err, test.want) {
				t.Fatalf("err=%v want=%v", err, test.want)
			}
		})
	}
}

func TestGenerationMixedSourceFailuresKeepStalePrecedence(t *testing.T) {
	denied := sourcepolicy.Verdict{SourceID: "denied", AssetDeny: evidence.DenyNotAuthorized}
	stale := sourcepolicy.Verdict{SourceID: "stale", Status: evidence.SourceStale}
	for _, verdicts := range []fixedSourceReviewer{{denied, stale}, {stale, denied}} {
		err := (generationSourceValidator{reviewer: verdicts}).ValidateGenerationSources(context.Background(), generation.Actor{TenantID: 7, UserID: "reader"}, "project-1", []generation.Source{{ID: "denied"}, {ID: "stale"}})
		if !errors.Is(err, generation.ErrSourceStale) {
			t.Fatalf("mixed verdicts=%v err=%v want stale", verdicts, err)
		}
	}
}

func TestSourceServiceReturnsExactlyTheReviewedProjection(t *testing.T) {
	asset := evidence.Asset{ID: "asset-1", ProjectID: "project-1", KnowledgeID: "knowledge-1", AssetRevision: 1, ProcessingState: evidence.AssetStateReady}
	chunk := &SourceChunk{ID: "source-1", KnowledgeID: "knowledge-1", StartAt: 0, EndAt: 6, Content: "budget"}
	reviewed := evidence.Source{ID: "source-1", ProjectID: "project-1", AssetID: "asset-1", AssetRevision: 2, QuotedText: "updated reviewed excerpt", Status: evidence.SourceAvailable}
	for _, mismatched := range []bool{false, true} {
		verdict := sourcepolicy.Verdict{SourceID: "source-1", Source: reviewed, Usable: true}
		if mismatched {
			verdict.Source.ID = "different-source"
		}
		service := NewSourceServiceWithReviewer(&projectAuthorizerStub{}, &sourceBindingsStub{asset: asset}, &assetGatewayStub{result: &evidence.ResolveResult{Allowed: []evidence.Asset{asset}}}, sourceCatalogStub{chunk: chunk}, originTextStub("budget"), nil, nil, fixedSourceReviewer{verdict})
		got, err := service.GetSource(context.Background(), Actor{TenantID: 7, UserID: "reader"}, "project-1", "source-1")
		if mismatched {
			if !errors.Is(err, ErrSourceUnavailable) {
				t.Fatalf("mismatched reviewed source escaped: %+v err=%v", got, err)
			}
		} else if err != nil || got != reviewed {
			t.Fatalf("returned an unreviewed source snapshot: got=%+v reviewed=%+v err=%v", got, reviewed, err)
		}
	}
}
