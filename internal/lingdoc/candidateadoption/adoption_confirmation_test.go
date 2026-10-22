package candidateadoption

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/Tencent/WeKnora/internal/clienterror"
	core "github.com/Tencent/WeKnora/internal/lingdoc/workspacecore"
	"github.com/Tencent/WeKnora/internal/lingdoc/writingaction"
	"github.com/Tencent/WeKnora/internal/types"
)

// 显式采纳确认的用例。
//
// 采纳不是「点一下就写进去」：确认载荷缺任何一项，服务层拒——不是把按钮变灰。
// 这组用例盯的是**服务层**的拒绝，不是前端的禁用状态。

// confirmationFixture 造一份可用的确认载荷。
//
// 取值抄自词汇包：动作与采纳目标的词表归 writingaction 所有，测试不另抄一份。
func confirmationFixture(action writingaction.Action, target writingaction.AdoptionTarget, actor string) *AdoptionConfirmation {
	return &AdoptionConfirmation{
		WritingAction: action,
		Target:        target,
		ConfirmedAt:   time.Date(2026, 10, 22, 1, 0, 0, 0, time.UTC),
		ConfirmedBy:   actor,
		Origin:        writingaction.OriginGenerated,
	}
}

// adoptionFixture 造一个「候选带动作与来源」的采纳场景。
//
// 夹具带上 writing_action 与 origin 是**新契约**：采纳必须确认「采纳的是哪个
// 动作的结果、候选是什么来源」，候选不说自己是怎么来的就无从确认。
func adoptionFixture(t *testing.T, action writingaction.Action, origin writingaction.Origin) (*SQLiteCandidateAdoptionStore, AcceptCandidateInput) {
	t.Helper()
	store := newCandidateAdoptionStore(t)
	seedCandidateAdoptionWorkspace(t, store,
		Chapter{ID: "chapter-1", ProjectID: "project-1", SectionID: "question", Title: "问题"},
		Candidate{
			ID: "candidate-1", ProjectID: "project-1", ChapterID: "chapter-1", RunID: "run-1",
			BodyMarkdown: "候选正文 [[source:source-1]]", SourceIDs: []string{"source-1"},
			Basis:    Basis{SpecRevision: 1, TemplateID: "template-demo", TemplateVersion: "1"},
			Validity: "fresh", WritingAction: action, Origin: origin,
		}, 1)
	allowSources := sourcePolicyFunc(func(context.Context, string, string, []string) error { return nil })
	service := NewCandidateAdoptionService(store, allowSources, allowAuthorizer{})
	input := AcceptCandidateInput{
		ProjectID: "project-1", ChapterID: "chapter-1", CandidateID: "candidate-1", ActorID: "user-1",
		IdempotencyKey: "candidate-adoption-confirm-0001", ExpectedSpecRevision: 1,
		// 这条夹具的现场里没有工作副本。
		ExpectedWorkingCopyRevision: 0,
		ExpectedBodyHash:            workingCopyBodyHash(""),
		Confirmation:                confirmationFixture(action, writingaction.TargetFormalVersionWhole, "user-1"),
	}
	_ = service
	return store, input
}

func TestAcceptCandidateRefusesAMissingConfirmation(t *testing.T) {
	store, input := adoptionFixture(t, writingaction.ActionGenerate, writingaction.OriginGenerated)
	input.Confirmation = nil
	service := NewCandidateAdoptionService(store,
		sourcePolicyFunc(func(context.Context, string, string, []string) error { return nil }), allowAuthorizer{})
	_, err := service.AcceptCandidate(context.Background(), input)
	require.ErrorIs(t, err, ErrInvalidRequest)

	var versions int64
	require.NoError(t, store.DB().Model(&chapterVersionRow{}).Count(&versions).Error)
	require.Equal(t, int64(0), versions, "缺确认载荷的采纳不得留下任何版本")
}

func TestAcceptCandidateRefusesAnIncompleteConfirmation(t *testing.T) {
	complete := confirmationFixture(writingaction.ActionGenerate, writingaction.TargetFormalVersionWhole, "user-1")
	cases := map[string]func(*AdoptionConfirmation){
		"缺动作":     func(c *AdoptionConfirmation) { c.WritingAction = "" },
		"缺采纳目标":   func(c *AdoptionConfirmation) { c.Target = "" },
		"缺确认时刻":   func(c *AdoptionConfirmation) { c.ConfirmedAt = time.Time{} },
		"缺确认人":    func(c *AdoptionConfirmation) { c.ConfirmedBy = "" },
		"缺来源":     func(c *AdoptionConfirmation) { c.Origin = "" },
		"部分采纳缺范围": func(c *AdoptionConfirmation) { c.Target = writingaction.TargetWorkingCopyPartial },
		"部分采纳范围倒置": func(c *AdoptionConfirmation) {
			c.Target = writingaction.TargetWorkingCopyPartial
			c.Scope = &AdoptionScope{StartUTF16: 5, EndUTF16: 5}
		},
		"部分采纳范围为负": func(c *AdoptionConfirmation) {
			c.Target = writingaction.TargetWorkingCopyPartial
			c.Scope = &AdoptionScope{StartUTF16: -1, EndUTF16: 3}
		},
		"整段采纳带了范围": func(c *AdoptionConfirmation) { c.Scope = &AdoptionScope{StartUTF16: 0, EndUTF16: 2} },
	}
	for name, mutate := range cases {
		t.Run(name, func(t *testing.T) {
			store, input := adoptionFixture(t, writingaction.ActionGenerate, writingaction.OriginGenerated)
			confirmation := *complete
			mutate(&confirmation)
			input.Confirmation = &confirmation
			input.IdempotencyKey = "candidate-adoption-incomplete-0001"
			service := NewCandidateAdoptionService(store,
				sourcePolicyFunc(func(context.Context, string, string, []string) error { return nil }), allowAuthorizer{})
			_, err := service.AcceptCandidate(context.Background(), input)
			require.ErrorIs(t, err, ErrInvalidRequest)

			var versions int64
			require.NoError(t, store.DB().Model(&chapterVersionRow{}).Count(&versions).Error)
			require.Equal(t, int64(0), versions)
		})
	}
}

func TestAcceptCandidateRefusesAConfirmationMadeForSomebodyElse(t *testing.T) {
	store, input := adoptionFixture(t, writingaction.ActionGenerate, writingaction.OriginGenerated)
	input.Confirmation.ConfirmedBy = "somebody-else"
	service := NewCandidateAdoptionService(store,
		sourcePolicyFunc(func(context.Context, string, string, []string) error { return nil }), allowAuthorizer{})
	_, err := service.AcceptCandidate(context.Background(), input)
	require.ErrorIs(t, err, ErrForbidden)

	var versions int64
	require.NoError(t, store.DB().Model(&chapterVersionRow{}).Count(&versions).Error)
	require.Equal(t, int64(0), versions)
}

func TestAcceptCandidateRefusesAConfirmationThatDoesNotMatchTheCandidate(t *testing.T) {
	t.Run("动作对不上", func(t *testing.T) {
		store, input := adoptionFixture(t, writingaction.ActionGenerate, writingaction.OriginGenerated)
		input.Confirmation.WritingAction = writingaction.ActionRewrite
		service := NewCandidateAdoptionService(store,
			sourcePolicyFunc(func(context.Context, string, string, []string) error { return nil }), allowAuthorizer{})
		_, err := service.AcceptCandidate(context.Background(), input)
		require.ErrorIs(t, err, ErrInvalidRequest)
	})
	t.Run("来源对不上", func(t *testing.T) {
		store, input := adoptionFixture(t, writingaction.ActionGenerate, writingaction.OriginGenerated)
		input.Confirmation.Origin = writingaction.OriginHuman
		service := NewCandidateAdoptionService(store,
			sourcePolicyFunc(func(context.Context, string, string, []string) error { return nil }), allowAuthorizer{})
		_, err := service.AcceptCandidate(context.Background(), input)
		require.ErrorIs(t, err, ErrInvalidRequest)
	})
}

func TestAcceptCandidateRefusesACandidateThatDoesNotRecordItsAction(t *testing.T) {
	store, input := adoptionFixture(t, writingaction.ActionGenerate, writingaction.OriginGenerated)
	// 候选没记动作：它没法被确认，必须响亮地失败，而不是被猜一个动作放行。
	require.NoError(t, store.DB().Model(&candidateRow{}).Where("id = ?", "candidate-1").
		Updates(map[string]any{"writing_action": "", "origin": ""}).Error)
	service := NewCandidateAdoptionService(store,
		sourcePolicyFunc(func(context.Context, string, string, []string) error { return nil }), allowAuthorizer{})
	_, err := service.AcceptCandidate(context.Background(), input)
	require.ErrorIs(t, err, ErrCandidateActionUnknown)

	form, ok := clienterror.FormOf(err)
	require.True(t, ok)
	require.Equal(t, 409, form.Status)
	require.Equal(t, "candidate_action_unknown", form.Code)
}

func TestAcceptCandidateRefusesATargetTheActionDoesNotAllow(t *testing.T) {
	t.Run("提纲不许建正式版本", func(t *testing.T) {
		store, input := adoptionFixture(t, writingaction.ActionOutline, writingaction.OriginGenerated)
		input.Confirmation.WritingAction = writingaction.ActionOutline
		input.Confirmation.Target = writingaction.TargetFormalVersionWhole
		service := NewCandidateAdoptionService(store,
			sourcePolicyFunc(func(context.Context, string, string, []string) error { return nil }), allowAuthorizer{})
		_, err := service.AcceptCandidate(context.Background(), input)
		require.ErrorIs(t, err, ErrInvalidRequest)
	})
	t.Run("语言评审没有可采纳物", func(t *testing.T) {
		store, input := adoptionFixture(t, writingaction.ActionReview, writingaction.OriginGenerated)
		input.Confirmation.WritingAction = writingaction.ActionReview
		input.Confirmation.Target = writingaction.TargetWorkingCopyWhole
		service := NewCandidateAdoptionService(store,
			sourcePolicyFunc(func(context.Context, string, string, []string) error { return nil }), allowAuthorizer{})
		_, err := service.AcceptCandidate(context.Background(), input)
		require.ErrorIs(t, err, ErrInvalidRequest)
	})
}

func TestAcceptCandidateRefusesAMachineIdentity(t *testing.T) {
	store, input := adoptionFixture(t, writingaction.ActionGenerate, writingaction.OriginGenerated)
	service := NewCandidateAdoptionService(store,
		sourcePolicyFunc(func(context.Context, string, string, []string) error { return nil }), allowAuthorizer{})
	machine := types.WithTenantAPIKeyScope(context.Background(), types.TenantAPIKeyScope{
		KeyID: 7, ScopeType: types.APIKeyScopeTenant, FullAccess: true,
	})
	_, err := service.AcceptCandidate(machine, input)
	require.ErrorIs(t, err, core.ErrHumanDecisionRequired)

	var versions int64
	require.NoError(t, store.DB().Model(&chapterVersionRow{}).Count(&versions).Error)
	require.Equal(t, int64(0), versions)
}

// TestAcceptCandidateRefusesAnInventedSourceMarker 断言采纳端不接受正文里
// 出现、却不在候选授权来源集合里的来源标记（A14）。
//
// 改写侧已经有同一条保证：validateRewriteOutput(output, input.SourceIDs) 拒绝
// 模型编造的来源（workspace/selected_rewrite_inherited_sources_test.go 的
// TestSelectedRewriteInheritanceDoesNotAuthorizeModelInventedSources）。采纳
// 端是同一条边界的另一半：候选落库之后，正文里冒出来一个没被授权的来源，
// 采纳必须把它拒掉，而不是让它进正式版本。
func TestAcceptCandidateRefusesAnInventedSourceMarker(t *testing.T) {
	store, input := adoptionFixture(t, writingaction.ActionGenerate, writingaction.OriginGenerated)
	// 正文里多了一个不在候选授权来源集合里的来源标记。
	require.NoError(t, store.DB().Model(&candidateRow{}).Where("id = ?", "candidate-1").
		Update("body_markdown", "候选正文 [[source:source-1]] [[source:invented]]").Error)
	service := NewCandidateAdoptionService(store,
		sourcePolicyFunc(func(context.Context, string, string, []string) error { return nil }), allowAuthorizer{})
	_, err := service.AcceptCandidate(context.Background(), input)
	require.ErrorIs(t, err, ErrInvalidRequest)

	var versions int64
	require.NoError(t, store.DB().Model(&chapterVersionRow{}).Count(&versions).Error)
	require.Equal(t, int64(0), versions, "编造的来源不得进入正式版本")
}

func TestAcceptCandidateAcceptsAWholeConfirmationAndRecordsItInTheAudit(t *testing.T) {
	store, input := adoptionFixture(t, writingaction.ActionGenerate, writingaction.OriginGenerated)
	require.NoError(t, store.DB().Exec(`CREATE TABLE lingdoc_project_audits (
		id TEXT PRIMARY KEY,
		project_id TEXT NOT NULL,
		actor_id TEXT NOT NULL,
		action TEXT NOT NULL,
		target TEXT NOT NULL,
		details_json TEXT NOT NULL DEFAULT '{}',
		created_at DATETIME
	)`).Error)
	service := NewCandidateAdoptionService(store,
		sourcePolicyFunc(func(context.Context, string, string, []string) error { return nil }), allowAuthorizer{})
	result, err := service.AcceptCandidate(context.Background(), input)
	require.NoError(t, err)
	require.NotNil(t, result.Chapter.CurrentVersionID)

	var audit projectAuditRow
	require.NoError(t, store.DB().Where("project_id = ? AND action = ?", "project-1", "project.candidate.adopt").First(&audit).Error)
	require.Equal(t, "user-1", audit.ActorID)
	require.Contains(t, audit.DetailsJSON, string(writingaction.ActionGenerate))
	require.Contains(t, audit.DetailsJSON, string(writingaction.TargetFormalVersionWhole))
	require.Contains(t, audit.DetailsJSON, string(writingaction.OriginGenerated))
	require.Contains(t, audit.DetailsJSON, "user-1")
}
