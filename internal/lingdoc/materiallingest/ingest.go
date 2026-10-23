package materiallingest

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/Tencent/WeKnora/internal/evidence"
	"github.com/Tencent/WeKnora/internal/types"
)

// ErrNotReady 表示材料还没到「处理就绪」。
//
// 调用方据此记 BLOCKED 并写明缺什么，而不是宣称通过。
var ErrNotReady = errors.New("material is not processing-ready")

// ErrMaterialMismatch 表示底座交回来的那条知识不是这份材料。
//
// 底座按内容去重：两份字节相同、标题前缀不同的材料会被它认成同一份，于是第二份
// 拿到的是第一份的知识 ID。这种错配必须当场失败——把材料 A 的知识 ID 当成材料 B
// 的报出去，后面的绑定与断言就都指错了材料。
var ErrMaterialMismatch = errors.New("material does not match the ingested knowledge")

// defaultPollInterval 是 WaitReady 的默认轮询间隔。
const defaultPollInterval = 2 * time.Second

// Request 是一次材料接入的输入。
type Request struct {
	// KnowledgeBaseID 是材料要接入的知识库。
	KnowledgeBaseID string
	// FilePath 是本地材料文件。
	FilePath string
	// TitlePrefix 是固定的标题前缀。
	TitlePrefix string
	// ExpectedSHA256 是运行前记录下来的材料哈希。
	//
	// 给了就核对：与文件实际哈希不一致时直接失败（ErrMaterialHashMismatch），
	// 不接入。这是「输入侧记录」与「运行时核对」两处哈希的对账口。
	ExpectedSHA256 string
}

// Material 是一份待接入的材料：身份 + 落点。
type Material struct {
	// ID 是稳定身份（与时间无关）。
	ID MaterialID
	// FilePath 是本地材料文件。
	FilePath string
	// FileName 是材料在底座上使用的文件名；它决定底座用哪个解析引擎。
	FileName string
	// FileSize 是文件字节数。
	FileSize int64
}

// MaterialKnowledge 是底座上一条承载材料的知识。
type MaterialKnowledge struct {
	ID       string
	Title    string
	FileName string
	FileHash string
	FileSize int64
}

// UploadResult 是一次接入的结果。
type UploadResult struct {
	// Knowledge 是承载这份材料的那条知识。
	Knowledge MaterialKnowledge
	// Reused 表示底座复用了已存在的同内容材料，没有新建。
	Reused bool
}

// BottomLayer 是材料接入需要的底座能力。
type BottomLayer interface {
	// FindByMaterial 返回知识库里已经承载这份材料的那条知识；没有则返回 nil。
	//
	// 「同一份材料」在这里有唯一定义：材料身份（Material.ID）与它在底座上的
	// 确定性文件名（Material.FileName）。
	FindByMaterial(ctx context.Context, kbID string, material Material) (*MaterialKnowledge, error)
	// UploadMaterial 把材料接入知识库。
	//
	// 实现必须按 (kbID, Material.ID) 幂等：重复接入返回同一条知识，不新建第二条。
	UploadMaterial(ctx context.Context, kbID string, material Material) (UploadResult, error)
}

// Result 是一次接入的结果。
type Result struct {
	// MaterialID 是材料的稳定身份：多轮验收之间复用它。
	MaterialID MaterialID
	// KnowledgeID 是底座上承载这份材料的知识标识；绑定（F01-06）用它。
	KnowledgeID string
	// KnowledgeBaseID 是材料所在的知识库。
	KnowledgeBaseID string
	// Title 是底座上的标题。
	Title string
	// SHA256 是文件内容哈希（十六进制）。
	SHA256 string
	// FileSize 是文件字节数。
	FileSize int64
	// ParseStatus 是底座的原始解析状态（knowledges.parse_status）。
	ParseStatus string
	// ProcessingState 是处理状态，取值就是 evidence.AssetState 枚举。
	ProcessingState evidence.AssetState
	// Ready 表示处理就绪：ProcessingState == evidence.AssetStateReady。
	Ready bool
	// Reused 表示这次没有新建知识，复用了同一份材料已有的那条。
	Reused bool
}

// Service 是材料接入能力。
type Service struct {
	bottom       BottomLayer
	signals      evidence.KnowledgeSignalReader
	pollInterval time.Duration
}

// New 组装材料接入能力。
//
// signals 是底座当前观测的取数口（生产实现见 workspace.dbKnowledgeReader）；处理
// 状态一律经它取，再由 evidence.ProcessingStateOf 映射成契约枚举。
func New(bottom BottomLayer, signals evidence.KnowledgeSignalReader) *Service {
	return &Service{bottom: bottom, signals: signals, pollInterval: defaultPollInterval}
}

// WithPollInterval 设置 WaitReady 的轮询间隔。
func (s *Service) WithPollInterval(interval time.Duration) *Service {
	if interval > 0 {
		s.pollInterval = interval
	}
	return s
}

// Ingest 把一份材料接入底座，并回报它的处理状态。
//
// 幂等：材料身份是「固定前缀 + 文件内容 SHA-256」，与时间无关，所以同一份材料
// 重复接入不会产生第二条知识，也不会换掉知识 ID。
//
// 注意「接入成功」不等于「处理就绪」：底座的解析是异步的，刚建好的那条知识此刻
// 多半还在 pending。要判定就绪，用 WaitReady 等它走完。
func (s *Service) Ingest(ctx context.Context, req Request) (Result, error) {
	kbID := strings.TrimSpace(req.KnowledgeBaseID)
	if kbID == "" {
		return Result{}, fmt.Errorf("%w: 知识库不能为空", ErrInvalidMaterial)
	}
	material, err := s.material(req)
	if err != nil {
		return Result{}, err
	}
	knowledge, reused, err := s.connect(ctx, kbID, material)
	if err != nil {
		return Result{}, err
	}
	observed, err := s.observe(ctx, knowledge.ID)
	if err != nil {
		return Result{}, err
	}
	return Result{
		MaterialID:      material.ID,
		KnowledgeID:     knowledge.ID,
		KnowledgeBaseID: kbID,
		Title:           knowledge.Title,
		SHA256:          material.ID.SHA256,
		FileSize:        material.FileSize,
		ParseStatus:     observed.ParseStatus,
		ProcessingState: observed.State,
		Ready:           observed.State == evidence.AssetStateReady,
		Reused:          reused,
	}, nil
}

// WaitReady 轮询材料，直到它处理就绪。
//
// 底座的解析是异步的：接入成功只说明「材料已经进去了」，处理有没有完成要另问。
// 这个函数把那个「另问」变成一次有超时的等待；超时时它返回最后一次观测到的状态
// 与错误，调用方据此记 BLOCKED（缺什么）而不是宣称通过。
func (s *Service) WaitReady(ctx context.Context, knowledgeID string, timeout time.Duration) (evidence.AssetState, error) {
	deadline := time.Now().Add(timeout)
	for {
		observed, err := s.observe(ctx, knowledgeID)
		if err != nil {
			return "", err
		}
		switch observed.State {
		case evidence.AssetStateReady:
			return observed.State, nil
		case evidence.AssetStateFailed, evidence.AssetStateReplaced:
			return observed.State, fmt.Errorf("%w: 处理停在 %s", ErrNotReady, observed.State)
		}
		if !time.Now().Before(deadline) {
			return observed.State, fmt.Errorf("%w: 等待处理就绪超时，当前状态 %s", ErrNotReady, observed.State)
		}
		select {
		case <-ctx.Done():
			return observed.State, ctx.Err()
		case <-time.After(s.pollInterval):
		}
	}
}

// observation 是一次处理状态观测。
type observation struct {
	// ParseStatus 是底座的原始解析状态。
	ParseStatus string
	// State 是映射后的契约处理状态。
	State evidence.AssetState
}

// material 把请求变成一份可接入的材料：算身份、核哈希、定文件名。
func (s *Service) material(req Request) (Material, error) {
	path := strings.TrimSpace(req.FilePath)
	if path == "" {
		return Material{}, fmt.Errorf("%w: 材料文件不能为空", ErrInvalidMaterial)
	}
	id, err := Identify(req.TitlePrefix, path)
	if err != nil {
		return Material{}, err
	}
	expected := strings.TrimSpace(req.ExpectedSHA256)
	if expected != "" && !id.MatchesSHA256(expected) {
		return Material{}, fmt.Errorf(
			"%w: 记录的是 %s，文件实际是 %s", ErrMaterialHashMismatch, expected, id.SHA256,
		)
	}
	info, err := os.Stat(path)
	if err != nil {
		return Material{}, fmt.Errorf("%w: 读取材料文件 %s: %v", ErrInvalidMaterial, path, err)
	}
	return Material{
		ID:       id,
		FilePath: path,
		FileName: id.FileName(filepath.Ext(path)),
		FileSize: info.Size(),
	}, nil
}

// connect 把材料接到底座上；底座上已经有同一份材料就复用那一条。
func (s *Service) connect(ctx context.Context, kbID string, material Material) (MaterialKnowledge, bool, error) {
	if s.bottom == nil {
		return MaterialKnowledge{}, false, fmt.Errorf("%w: 缺少底座接入能力", ErrInvalidMaterial)
	}
	existing, err := s.bottom.FindByMaterial(ctx, kbID, material)
	if err != nil {
		return MaterialKnowledge{}, false, fmt.Errorf("查找材料的既有接入: %w", err)
	}
	if existing != nil {
		return *existing, true, nil
	}
	uploaded, err := s.bottom.UploadMaterial(ctx, kbID, material)
	if err != nil {
		return MaterialKnowledge{}, false, fmt.Errorf("接入材料: %w", err)
	}
	// 底座按内容去重，可能把字节相同、标题前缀不同的另一份材料当成这份交回来。
	// 那不是「接好了」，是接错了：当场失败比把错的 ID 报出去好。
	if uploaded.Knowledge.FileName != "" && uploaded.Knowledge.FileName != material.FileName {
		return MaterialKnowledge{}, false, fmt.Errorf(
			"%w: 底座交回的是 %q，要接的是 %q",
			ErrMaterialMismatch, uploaded.Knowledge.FileName, material.FileName,
		)
	}
	return uploaded.Knowledge, uploaded.Reused, nil
}

// observe 取这条材料当前的处理状态。
//
// 状态经 evidence.ProcessingStateOf 从底座的 parse_status 映射而来——与绑定层
// （evidence.Bindings.RefreshAsset）走同一条链，所以「就绪」在这里和在 F01-06 的
// 响应里是同一个判定，不会出现两套口径。
func (s *Service) observe(ctx context.Context, knowledgeID string) (observation, error) {
	if s.signals == nil {
		return observation{}, fmt.Errorf("%w: 缺少处理状态取数口", ErrInvalidMaterial)
	}
	signal, present, err := s.signals.CurrentKnowledgeSignal(ctx, knowledgeID)
	if err != nil {
		return observation{}, fmt.Errorf("读取材料处理状态: %w", err)
	}
	if !present {
		// 材料不在底座了：按绑定层同一口径记成 replaced（删除中），它当然不是就绪。
		return observation{
			ParseStatus: types.ParseStatusDeleting,
			State:       evidence.ProcessingStateOf(types.ParseStatusDeleting),
		}, nil
	}
	return observation{
		ParseStatus: signal.ParseStatus,
		State:       evidence.ProcessingStateOf(signal.ParseStatus),
	}, nil
}
