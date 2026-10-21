package generation

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/Tencent/WeKnora/internal/lingdoc/candidateadoption"
	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"github.com/stretchr/testify/require"
	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
)

// runTraceFixture 造一个用真实存储的生成服务。
//
// 留痕这件事发生在存储里：状态、原因与输入都在 run 行上。用一个假的
// Repository 断言「留痕了」没有意义——它只会证明夹具自己记住了。
func runTraceFixture(t *testing.T, model ModelAdapter) (*Service, *SQLiteRepository, *gorm.DB, Actor, Request) {
	t.Helper()
	ctx := context.Background()
	db, err := gorm.Open(sqlite.Open("file:"+uuid.NewString()+"?mode=memory&cache=shared"), &gorm.Config{})
	require.NoError(t, err)
	sqlDB, err := db.DB()
	require.NoError(t, err)
	t.Cleanup(func() { _ = sqlDB.Close() })

	repo := NewSQLiteRepository(db)
	require.NoError(t, candidateadoption.NewSQLiteCandidateAdoptionStore(db).AutoMigrate(ctx))
	require.NoError(t, repo.AutoMigrate(ctx))

	actor := Actor{TenantID: 7, UserID: "member-1"}
	chapterVersion := "chapter-v1"
	basis := candidateadoption.Basis{SpecRevision: 3, ChapterVersionID: &chapterVersion,
		AssetVersions: []candidateadoption.AssetVersion{{AssetID: "asset-1", AssetRevision: 2}}}
	input := Input{Actor: actor, Basis: basis,
		Workspace: candidateadoption.GenerationContext{ProjectID: "project-1", ChapterID: "chapter-1", SpecRevision: 3,
			ChapterVersionID: &chapterVersion, Basis: basis},
		Sources: []Source{{ID: "source-1", ProjectID: "project-1", AssetID: "asset-1", AssetRevision: 2, Status: "available"}}}
	service := NewService(testAuthorizer{}, testInputs{input: input}, testSources{}, testCurrentness{current: true},
		model, repo, &testEnqueuer{})
	service.NewID = func() string { return "candidate-1" }
	request := Request{ChapterID: "chapter-1", AssetIDs: []string{"asset-1"}, Instruction: "Draft this chapter",
		ExpectedSpecRevision: 3, ExpectedChapterVersionID: &chapterVersion}
	return service, repo, db, actor, request
}

// storedRunInputJSON 读回 run 行上存的输入原文。
func storedRunInputJSON(t *testing.T, db *gorm.DB, runID string) string {
	t.Helper()
	var row generationRow
	require.NoError(t, db.Where("id = ?", runID).First(&row).Error)
	return row.InputJSON
}

// TestFailedRunKeepsItsInputSummaryStatusAndReason 覆盖 A21：失败的 run 要能
// 读回输入摘要、状态与原因。
func TestFailedRunKeepsItsInputSummaryStatusAndReason(t *testing.T) {
	service, _, db, actor, request := runTraceFixture(t, testModel{err: errors.New("provider refused")})
	run, err := service.Start(context.Background(), actor, "project-1", "generation-fail-1", request)
	require.NoError(t, err)

	failed, err := service.Execute(context.Background(), run.ID)
	require.NoError(t, err)
	require.Equal(t, StatusFailed, failed.Status)
	require.NotNil(t, failed.Error)
	require.Equal(t, "generation_failed", failed.Error.Code)
	require.NotEmpty(t, failed.Error.Message)
	require.True(t, failed.Error.Retryable)

	stored, err := service.Get(context.Background(), actor, "project-1", run.ID)
	require.NoError(t, err)
	require.Equal(t, StatusFailed, stored.Status)
	require.NotNil(t, stored.Error)
	require.Equal(t, "generation_failed", stored.Error.Code)
	require.NotEmpty(t, stored.Error.Message)
	require.True(t, stored.Error.Retryable)

	require.NotNil(t, stored.InputSummary, "失败的 run 必须能读回输入摘要")
	require.Equal(t, "chapter-1", stored.InputSummary.ChapterID)
	require.Equal(t, []string{"asset-1"}, stored.InputSummary.AssetIDs)
	require.Equal(t, 3, stored.InputSummary.SpecRevision)
	require.Equal(t, "chapter-v1", *stored.InputSummary.ChapterVersionID)
	require.Equal(t, len([]rune(request.Instruction)), stored.InputSummary.InstructionRunes)
	require.NotEmpty(t, stored.InputSummary.InstructionHash)
	require.NotContains(t, string(mustJSON(t, stored.InputSummary)), request.Instruction,
		"输入摘要不得把说明原文吐出来")

	// 输入没丢：行里仍然留着可重跑的完整输入。
	raw := storedRunInputJSON(t, db, run.ID)
	require.NotEmpty(t, raw)
	var storedInput Input
	require.NoError(t, json.Unmarshal([]byte(raw), &storedInput))
	require.Equal(t, request.Instruction, storedInput.Request.Instruction)
}

// TestCancelledRunKeepsItsInputSummaryAndInput 覆盖 A21 的另一半：取消之后
// 状态是 interrupted，输入既不丢也能读回。
func TestCancelledRunKeepsItsInputSummaryAndInput(t *testing.T) {
	service, _, db, actor, request := runTraceFixture(t, testModel{})
	run, err := service.Start(context.Background(), actor, "project-1", "generation-cancel-1", request)
	require.NoError(t, err)
	before := storedRunInputJSON(t, db, run.ID)
	require.NotEmpty(t, before)

	cancelled, err := service.Cancel(context.Background(), actor, "project-1", run.ID)
	require.NoError(t, err)
	require.Equal(t, StatusInterrupted, cancelled.Status)
	require.NotNil(t, cancelled.Error)
	require.Equal(t, "generation_cancelled", cancelled.Error.Code)

	stored, err := service.Get(context.Background(), actor, "project-1", run.ID)
	require.NoError(t, err)
	require.Equal(t, StatusInterrupted, stored.Status)
	require.NotNil(t, stored.Error)
	require.Equal(t, "generation_cancelled", stored.Error.Code)
	require.NotNil(t, stored.InputSummary)
	require.Equal(t, "chapter-1", stored.InputSummary.ChapterID)
	require.Equal(t, before, storedRunInputJSON(t, db, run.ID), "取消不得清空输入")
}

// TestTimedOutRunKeepsItsReasonAndInput 覆盖 A25 的超时分支：超时的 run 是
// interrupted，原因与输入都留着。
func TestTimedOutRunKeepsItsReasonAndInput(t *testing.T) {
	service, _, _, actor, request := runTraceFixture(t, deadlineTestModel{})
	run, err := service.Start(context.Background(), actor, "project-1", "generation-timeout-1", request)
	require.NoError(t, err)

	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()
	interrupted, err := service.Execute(ctx, run.ID)
	require.NoError(t, err)
	require.Equal(t, StatusInterrupted, interrupted.Status)
	require.NotNil(t, interrupted.Error)
	require.Equal(t, "generation_interrupted", interrupted.Error.Code)
	require.True(t, interrupted.Error.Retryable)
	require.NotNil(t, interrupted.InputSummary)
	require.Equal(t, "chapter-1", interrupted.InputSummary.ChapterID)
}

// TestMalformedModelOutputFailsTheRunWithoutACandidate 覆盖 A25 的格式错误
// 分支：状态与原因留着，而且**不产生候选**。
func TestMalformedModelOutputFailsTheRunWithoutACandidate(t *testing.T) {
	// 模型引用了本次授权之外的来源：正文与来源清单对不上。
	draft := Draft{BodyMarkdown: "Invented [[source:source-9]]", Sources: []Source{{ID: "source-9"}}}
	service, _, db, actor, request := runTraceFixture(t, testModel{draft: draft})
	run, err := service.Start(context.Background(), actor, "project-1", "generation-malformed-1", request)
	require.NoError(t, err)

	failed, err := service.Execute(context.Background(), run.ID)
	require.NoError(t, err)
	require.Equal(t, StatusFailed, failed.Status)
	require.NotNil(t, failed.Error)
	require.Equal(t, "invalid_source_reference", failed.Error.Code)
	require.False(t, failed.Error.Retryable)
	require.Nil(t, failed.CandidateID)
	require.NotNil(t, failed.InputSummary)

	var candidates int64
	require.NoError(t, db.Model(&candidateRowForTrace{}).Count(&candidates).Error)
	require.Zero(t, candidates, "失败的 run 不得留下候选")
}

// TestRetryingWithANewIdempotencyKeyStartsANewRunAndKeepsTheOldOneReadable
// 覆盖 A21 的重试语义：新键开一个新 run，旧键仍然读得到那条失败留痕，
// 而且不会重复写工作副本（这里没有第二次候选被写出来）。
func TestRetryingWithANewIdempotencyKeyStartsANewRunAndKeepsTheOldOneReadable(t *testing.T) {
	service, _, db, actor, request := runTraceFixture(t, testModel{err: errors.New("provider refused")})
	first, err := service.Start(context.Background(), actor, "project-1", "generation-retry-1", request)
	require.NoError(t, err)
	_, err = service.Execute(context.Background(), first.ID)
	require.NoError(t, err)

	second, err := service.Start(context.Background(), actor, "project-1", "generation-retry-2", request)
	require.NoError(t, err)
	require.NotEqual(t, first.ID, second.ID, "新幂等键必须开一个新 run")
	require.Equal(t, StatusQueued, second.Status)

	old, err := service.Get(context.Background(), actor, "project-1", first.ID)
	require.NoError(t, err)
	require.Equal(t, StatusFailed, old.Status)
	require.NotNil(t, old.Error)
	require.Equal(t, "generation_failed", old.Error.Code)
	require.NotNil(t, old.InputSummary, "旧 run 的留痕不能被重试抹掉")

	replayed, err := service.Start(context.Background(), actor, "project-1", "generation-retry-1", request)
	require.NoError(t, err)
	require.Equal(t, first.ID, replayed.ID, "旧键必须仍然指向原来那条 run")
	require.True(t, replayed.Replayed)

	var runs int64
	require.NoError(t, db.Model(&generationRow{}).Count(&runs).Error)
	require.Equal(t, int64(2), runs, "重试不得产生第三条 run")
	var candidates int64
	require.NoError(t, db.Model(&candidateRowForTrace{}).Count(&candidates).Error)
	require.Zero(t, candidates, "失败的 run 不得留下候选")
}

// candidateRowForTrace 让失败用例能数候选行。
//
// 候选表住在 candidateadoption 包里，这里用一张同名的影子表名去数，避免为
// 一个断言把候选存储的内部结构暴露出去。
type candidateRowForTrace struct {
	ID string `gorm:"primaryKey;size:36"`
}

func (candidateRowForTrace) TableName() string { return "lingdoc_candidates" }

func mustJSON(t *testing.T, value any) []byte {
	t.Helper()
	encoded, err := json.Marshal(value)
	require.NoError(t, err)
	return encoded
}

// TestRunTraceIsReadableOverHTTP 覆盖 HTTP 接缝。
//
// 留痕是给人看的：失败的 run 在响应里就要能读回输入摘要、状态与原因，而不是
// 只在服务层的结构体里存在。
func TestRunTraceIsReadableOverHTTP(t *testing.T) {
	service, _, _, actor, request := runTraceFixture(t, testModel{err: errors.New("provider refused")})
	gin.SetMode(gin.TestMode)
	router := gin.New()
	RegisterRoutes(router.Group("/api/v1/lingdoc"), NewHandler(service, func(*gin.Context) (Actor, bool) {
		return actor, true
	}))

	body, err := json.Marshal(request)
	require.NoError(t, err)
	start := httptest.NewRequest(http.MethodPost, "/api/v1/lingdoc/projects/project-1/generations", bytes.NewReader(body))
	start.Header.Set("Idempotency-Key", "generation-http-trace-1")
	started := httptest.NewRecorder()
	router.ServeHTTP(started, start)
	require.Equal(t, http.StatusAccepted, started.Code, started.Body.String())

	var envelope struct {
		Data Run `json:"data"`
	}
	require.NoError(t, json.Unmarshal(started.Body.Bytes(), &envelope))
	require.NotNil(t, envelope.Data.InputSummary, "HTTP 响应里必须带输入摘要")
	require.Equal(t, "chapter-1", envelope.Data.InputSummary.ChapterID)
	require.NotContains(t, started.Body.String(), request.Instruction, "输入摘要不得把说明原文吐出来")

	_, err = service.Execute(context.Background(), envelope.Data.ID)
	require.NoError(t, err)

	read := httptest.NewRequest(http.MethodGet, "/api/v1/lingdoc/projects/project-1/generations/"+envelope.Data.ID, nil)
	readResponse := httptest.NewRecorder()
	router.ServeHTTP(readResponse, read)
	require.Equal(t, http.StatusOK, readResponse.Code, readResponse.Body.String())
	require.Contains(t, readResponse.Body.String(), `"status":"failed"`)
	require.Contains(t, readResponse.Body.String(), `"code":"generation_failed"`)
	require.Contains(t, readResponse.Body.String(), `"input_summary"`)
}
