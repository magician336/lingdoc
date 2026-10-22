package generation

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"
)

// 提纲的撤权切片（#347）。
//
// 撤权有四个时机：模型调用之前、运行之中、候选读取之前、采纳之前。四个时机都
// 在**与动作无关**的代码上——它们查的是来源授权，不是动作——所以这一组用例
// 盯的是「提纲走的是同一条路」这件事，而不是重新证明一遍机制。
//
// 夹具自带：它用提纲自己的夹具，不复用别的动作的夹具（开发计划 D20）。

// outlineRevocationValidator 让用例在指定的一次来源复核上撤掉授权。
type outlineRevocationValidator struct {
	revoked bool
}

func (v *outlineRevocationValidator) ValidateGenerationSources(context.Context, Actor, string, []Source) error {
	if v.revoked {
		return ErrSourceAccessDenied
	}
	return nil
}

// outlineRevocationModel 在模型返回之前撤掉授权。
type outlineRevocationModel struct {
	onCall func()
}

func (m *outlineRevocationModel) RunMode() string { return "mock" }

func (m *outlineRevocationModel) Generate(context.Context, Input) (Draft, error) {
	if m.onCall != nil {
		m.onCall()
	}
	return Draft{BodyMarkdown: outlineSkeleton, Sources: []Source{{ID: "source-asset-1"}}}, nil
}

// TestGenerationOutlineRevocationBeforeModelCallBlocksTheRun 覆盖撤权的第一个
// 时机：模型调用之前。
//
// 授权没了 ⇒ 拒绝、模型不被调用、一条候选都不留。提纲与其它动作走同一条路。
func TestGenerationOutlineRevocationBeforeModelCallBlocksTheRun(t *testing.T) {
	svc, repo := outlineFixture(t)
	validator := &outlineRevocationValidator{revoked: true}
	svc.Sources = validator
	model := &countingModel{draft: Draft{BodyMarkdown: outlineSkeleton, Sources: []Source{{ID: "source-asset-1"}}}}
	svc.Model = model

	run, err := svc.Execute(context.Background(), repo.run.ID)
	require.NoError(t, err, "撤权在模型调用前 ⇒ 运行失败，失败原因在运行上")
	require.Equal(t, StatusFailed, run.Status)
	require.NotNil(t, run.Error)
	require.Equal(t, "source_access_denied", run.Error.Code)
	require.Zero(t, model.calls, "撤权后模型不得被调用")
	require.Empty(t, repo.candidate.ID, "撤权后不得留下候选")
}

// TestGenerationOutlineRevocationDuringRunLeavesNoUsableCandidate 覆盖撤权的
// 第二个时机：模型已经返回、写入之前。
func TestGenerationOutlineRevocationDuringRunLeavesNoUsableCandidate(t *testing.T) {
	svc, repo := outlineFixture(t)
	validator := &outlineRevocationValidator{}
	svc.Sources = validator
	svc.Model = &outlineRevocationModel{onCall: func() { validator.revoked = true }}

	run, err := svc.Execute(context.Background(), repo.run.ID)
	require.NoError(t, err)
	require.Equal(t, StatusFailed, run.Status)
	require.NotNil(t, repo.failure)
	require.Equal(t, "source_access_denied", repo.failure.Code)
	require.Empty(t, repo.candidate.ID, "撤权后不得发布候选")
}

// TestGenerationOutlineRevocationBeforeCandidateReadIsDenied 覆盖撤权的第三个
// 时机：候选读取之前。
//
// 候选还在，但它依据的授权没了 ⇒ 读取被拒。
func TestGenerationOutlineRevocationBeforeCandidateReadIsDenied(t *testing.T) {
	svc, repo := outlineFixture(t)
	validator := &outlineRevocationValidator{}
	svc.Sources = validator

	run, err := svc.Execute(context.Background(), repo.run.ID)
	require.NoError(t, err)
	require.Equal(t, StatusSucceeded, run.Status)

	validator.revoked = true
	_, err = svc.GetCandidate(context.Background(), Actor{TenantID: 1, UserID: "user-1"}, "project-1", repo.run.ID)
	require.ErrorIs(t, err, ErrSourceAccessDenied)
}
