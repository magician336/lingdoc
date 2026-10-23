package materiallingest

import (
	"context"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/Tencent/WeKnora/internal/evidence"
	"github.com/Tencent/WeKnora/internal/types"
	"github.com/stretchr/testify/require"
)

// fakeBottom 是底座的本地替身：不发网络请求、不碰真实服务。
//
// 它同时实现 BottomLayer（接入）与 evidence.KnowledgeSignalReader（取处理状态），
// 因为在真实底座里这两件事是同一份数据：接入建出知识行，解析推进那行的
// parse_status。替身把它们放在一起，测试才能看到「接入 → 解析 → 就绪」的完整链条，
// 而且处理状态取数走的仍是 evidence.KnowledgeSignalReader → evidence.ProcessingStateOf
// 这条真实链路。
type fakeBottom struct {
	mu        sync.Mutex
	items     map[string]MaterialKnowledge
	statuses  map[string]string
	uploadCnt int
	calls     int
	// flipAfter 是一次观测到第几次时把状态翻成 flipTo（0 表示不翻）。
	flipAfter int
	flipTo    string
}

func newFakeBottom() *fakeBottom {
	return &fakeBottom{items: map[string]MaterialKnowledge{}, statuses: map[string]string{}}
}

func (f *fakeBottom) FindByMaterial(
	_ context.Context, kbID string, material Material,
) (*MaterialKnowledge, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	knowledge, ok := f.items[f.key(kbID, material)]
	if !ok {
		return nil, nil
	}
	return &knowledge, nil
}

func (f *fakeBottom) UploadMaterial(
	_ context.Context, kbID string, material Material,
) (UploadResult, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	key := f.key(kbID, material)
	if existing, ok := f.items[key]; ok {
		return UploadResult{Knowledge: existing, Reused: true}, nil
	}
	f.uploadCnt++
	knowledge := MaterialKnowledge{
		ID:       "k-" + material.ID.shortDigest(),
		Title:    material.ID.Title(),
		FileName: material.FileName,
		FileHash: material.ID.SHA256,
		FileSize: material.FileSize,
	}
	f.items[key] = knowledge
	// 新建的知识行此刻是 pending：底座的解析是异步的，接入完成不等于解析完成。
	f.statuses[knowledge.ID] = types.ParseStatusPending
	return UploadResult{Knowledge: knowledge}, nil
}

func (f *fakeBottom) CurrentKnowledgeSignal(
	_ context.Context, knowledgeID string,
) (evidence.KnowledgeSignal, bool, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.calls++
	status, ok := f.statuses[knowledgeID]
	if !ok {
		return evidence.KnowledgeSignal{}, false, nil
	}
	if f.flipAfter > 0 && f.calls >= f.flipAfter {
		status = f.flipTo
	}
	return evidence.KnowledgeSignal{
		KnowledgeID: knowledgeID,
		ParseStatus: status,
		FileHash:    "file-hash",
		FileSize:    12,
	}, true, nil
}

// setStatus 推进这条材料的解析状态。
func (f *fakeBottom) setStatus(knowledgeID, status string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.statuses[knowledgeID] = status
}

// forget 让这条材料在底座上消失（模拟被删）。
func (f *fakeBottom) forget(knowledgeID string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	delete(f.statuses, knowledgeID)
}

func (f *fakeBottom) uploads() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.uploadCnt
}

func (f *fakeBottom) key(kbID string, material Material) string {
	return kbID + "|" + material.ID.String()
}

func writeMaterial(t *testing.T, name, content string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), name)
	require.NoError(t, os.WriteFile(path, []byte(content), 0o600))
	return path
}

// TestIngestReportsProcessingReadyAfterMaterialIsProcessed 是本包最要紧的一条：
// 材料接入之后，处理状态要能走到「就绪」，而且是经 evidence.ProcessingStateOf
// 从底座的 parse_status 映射出来的那一档。
func TestIngestReportsProcessingReadyAfterMaterialIsProcessed(t *testing.T) {
	t.Parallel()
	path := writeMaterial(t, "synthetic.md", "# 合成材料\n")
	bottom := newFakeBottom()
	service := New(bottom, bottom)
	request := Request{KnowledgeBaseID: "kb-1", FilePath: path, TitlePrefix: "f01-synth-q"}

	// 刚接入时底座还没解析完：此刻不能算就绪。
	first, err := service.Ingest(context.Background(), request)
	require.NoError(t, err)
	require.Equal(t, evidence.AssetStatePending, first.ProcessingState)
	require.False(t, first.Ready)
	require.NotEmpty(t, first.KnowledgeID)

	// 解析完成后，同一条材料的处理状态变成就绪。
	bottom.setStatus(first.KnowledgeID, types.ParseStatusCompleted)
	second, err := service.Ingest(context.Background(), request)
	require.NoError(t, err)
	require.Equal(t, evidence.AssetStateReady, second.ProcessingState)
	require.True(t, second.Ready)
	require.Equal(t, types.ParseStatusCompleted, second.ParseStatus)
	require.Equal(t, first.KnowledgeID, second.KnowledgeID)
}

// TestIngestIsIdempotentForTheSameMaterial 盯住「同一份材料重复接入不新增知识」。
func TestIngestIsIdempotentForTheSameMaterial(t *testing.T) {
	t.Parallel()
	path := writeMaterial(t, "synthetic.md", "# 合成材料\n")
	bottom := newFakeBottom()
	service := New(bottom, bottom)
	request := Request{KnowledgeBaseID: "kb-1", FilePath: path, TitlePrefix: "f01-synth-q"}

	first, err := service.Ingest(context.Background(), request)
	require.NoError(t, err)
	require.False(t, first.Reused)

	second, err := service.Ingest(context.Background(), request)
	require.NoError(t, err)
	require.True(t, second.Reused)
	require.Equal(t, 1, bottom.uploads(), "重复接入不得再建一条知识")
	require.Equal(t, first.KnowledgeID, second.KnowledgeID)
	require.Equal(t, first.MaterialID, second.MaterialID)
	require.Equal(t, first.SHA256, second.SHA256)
}

// TestIngestDoesNotClaimReadyWhileProcessingIsIncomplete 盯住「未就绪不能被当作就绪」。
func TestIngestDoesNotClaimReadyWhileProcessingIsIncomplete(t *testing.T) {
	t.Parallel()
	path := writeMaterial(t, "synthetic.md", "# 合成材料\n")
	bottom := newFakeBottom()
	service := New(bottom, bottom)
	request := Request{KnowledgeBaseID: "kb-1", FilePath: path, TitlePrefix: "f01-synth-q"}

	result, err := service.Ingest(context.Background(), request)
	require.NoError(t, err)
	bottom.setStatus(result.KnowledgeID, types.ParseStatusProcessing)

	result, err = service.Ingest(context.Background(), request)
	require.NoError(t, err)
	require.Equal(t, evidence.AssetStateProcessing, result.ProcessingState)
	require.False(t, result.Ready)

	// 等到超时也不能把「还没好」说成「好了」。
	state, err := service.WaitReady(context.Background(), result.KnowledgeID, 0)
	require.ErrorIs(t, err, ErrNotReady)
	require.Equal(t, evidence.AssetStateProcessing, state)
}

// TestIngestTreatsFailedParsingAsNotReady 盯住失败档：解析失败不是就绪。
func TestIngestTreatsFailedParsingAsNotReady(t *testing.T) {
	t.Parallel()
	path := writeMaterial(t, "synthetic.md", "# 合成材料\n")
	bottom := newFakeBottom()
	service := New(bottom, bottom)
	request := Request{KnowledgeBaseID: "kb-1", FilePath: path, TitlePrefix: "f01-synth-q"}

	result, err := service.Ingest(context.Background(), request)
	require.NoError(t, err)
	bottom.setStatus(result.KnowledgeID, types.ParseStatusFailed)

	result, err = service.Ingest(context.Background(), request)
	require.NoError(t, err)
	require.Equal(t, evidence.AssetStateFailed, result.ProcessingState)
	require.False(t, result.Ready)

	state, err := service.WaitReady(context.Background(), result.KnowledgeID, time.Minute)
	require.ErrorIs(t, err, ErrNotReady)
	require.Equal(t, evidence.AssetStateFailed, state)
}

// TestIngestTreatsVanishedMaterialAsNotReady 盯住材料不在底座的情形。
func TestIngestTreatsVanishedMaterialAsNotReady(t *testing.T) {
	t.Parallel()
	path := writeMaterial(t, "synthetic.md", "# 合成材料\n")
	bottom := newFakeBottom()
	service := New(bottom, bottom)

	result, err := service.Ingest(context.Background(), Request{
		KnowledgeBaseID: "kb-1", FilePath: path, TitlePrefix: "f01-synth-q",
	})
	require.NoError(t, err)
	bottom.forget(result.KnowledgeID)

	result, err = service.Ingest(context.Background(), Request{
		KnowledgeBaseID: "kb-1", FilePath: path, TitlePrefix: "f01-synth-q",
	})
	require.NoError(t, err)
	require.Equal(t, evidence.AssetStateReplaced, result.ProcessingState)
	require.False(t, result.Ready)
}

// TestIngestRejectsMaterialWhoseHashDiffersFromTheRecordedOne 盯住两处哈希对账：
// 运行前记录的哈希与运行时算出的不一致时，不许接入。
func TestIngestRejectsMaterialWhoseHashDiffersFromTheRecordedOne(t *testing.T) {
	t.Parallel()
	path := writeMaterial(t, "synthetic.md", "# 合成材料\n")
	bottom := newFakeBottom()
	service := New(bottom, bottom)

	_, err := service.Ingest(context.Background(), Request{
		KnowledgeBaseID: "kb-1", FilePath: path, TitlePrefix: "f01-synth-q",
		ExpectedSHA256: "0000000000000000000000000000000000000000000000000000000000000000",
	})
	require.ErrorIs(t, err, ErrMaterialHashMismatch)
	require.Equal(t, 0, bottom.uploads(), "哈希对不上时不得接入")
}

// TestIngestAcceptsMaterialWhoseHashMatchesTheRecordedOne 是上一条的正面对照。
func TestIngestAcceptsMaterialWhoseHashMatchesTheRecordedOne(t *testing.T) {
	t.Parallel()
	path := writeMaterial(t, "synthetic.md", "# 合成材料\n")
	bottom := newFakeBottom()
	service := New(bottom, bottom)
	sum, err := FileSHA256(path)
	require.NoError(t, err)

	result, err := service.Ingest(context.Background(), Request{
		KnowledgeBaseID: "kb-1", FilePath: path, TitlePrefix: "f01-synth-q",
		ExpectedSHA256: sum,
	})
	require.NoError(t, err)
	require.Equal(t, sum, result.SHA256)
	require.Equal(t, 1, bottom.uploads())
}

// TestWaitReadyReturnsReadyOnceParsingCompletes 盯住等待这一档：解析完成后等得到
// 「就绪」，而不是一直卡在 pending。
func TestWaitReadyReturnsReadyOnceParsingCompletes(t *testing.T) {
	t.Parallel()
	path := writeMaterial(t, "synthetic.md", "# 合成材料\n")
	bottom := newFakeBottom()
	service := New(bottom, bottom).WithPollInterval(time.Millisecond)

	result, err := service.Ingest(context.Background(), Request{
		KnowledgeBaseID: "kb-1", FilePath: path, TitlePrefix: "f01-synth-q",
	})
	require.NoError(t, err)
	bottom.setStatus(result.KnowledgeID, types.ParseStatusProcessing)
	bottom.flipAfter, bottom.flipTo = 3, types.ParseStatusCompleted

	state, err := service.WaitReady(context.Background(), result.KnowledgeID, 5*time.Second)
	require.NoError(t, err)
	require.Equal(t, evidence.AssetStateReady, state)
}

// mismatchedBottom 是把别的材料的知识当成本次材料交回来的底座。
type mismatchedBottom struct{}

func (mismatchedBottom) FindByMaterial(context.Context, string, Material) (*MaterialKnowledge, error) {
	return nil, nil
}

func (mismatchedBottom) UploadMaterial(context.Context, string, Material) (UploadResult, error) {
	return UploadResult{
		Knowledge: MaterialKnowledge{ID: "k-other", FileName: "other-prefix-0123456789ab.md"},
		Reused:    true,
	}, nil
}

// TestIngestRejectsKnowledgeThatCarriesAnotherMaterial 盯住「接回来的不是这份材料」：
// 底座按内容去重，可能把字节相同、标题前缀不同的另一份材料当成这份交回来。
func TestIngestRejectsKnowledgeThatCarriesAnotherMaterial(t *testing.T) {
	t.Parallel()
	path := writeMaterial(t, "synthetic.md", "# 合成材料\n")
	service := New(mismatchedBottom{}, newFakeBottom())

	_, err := service.Ingest(context.Background(), Request{
		KnowledgeBaseID: "kb-1", FilePath: path, TitlePrefix: "f01-synth-q",
	})
	require.ErrorIs(t, err, ErrMaterialMismatch)
}

// TestIngestRejectsMissingFile 盯住材料文件读不了的情形：不返回半个身份。
func TestIngestRejectsMissingFile(t *testing.T) {
	t.Parallel()
	bottom := newFakeBottom()
	service := New(bottom, bottom)

	_, err := service.Ingest(context.Background(), Request{
		KnowledgeBaseID: "kb-1", FilePath: filepath.Join(t.TempDir(), "missing.md"),
		TitlePrefix: "f01-synth-q",
	})
	require.ErrorIs(t, err, ErrInvalidMaterial)
	require.Equal(t, 0, bottom.uploads())
}

// TestIngestRejectsMissingKnowledgeBase 盯住知识库缺失。
func TestIngestRejectsMissingKnowledgeBase(t *testing.T) {
	t.Parallel()
	path := writeMaterial(t, "synthetic.md", "# 合成材料\n")
	bottom := newFakeBottom()
	service := New(bottom, bottom)

	_, err := service.Ingest(context.Background(), Request{FilePath: path, TitlePrefix: "f01-synth-q"})
	require.ErrorIs(t, err, ErrInvalidMaterial)
	require.Equal(t, 0, bottom.uploads())
}
