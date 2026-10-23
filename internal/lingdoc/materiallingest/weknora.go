package materiallingest

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"mime/multipart"
	"net/textproto"
	"os"

	"github.com/Tencent/WeKnora/internal/types"
)

// WeKnoraService 是材料接入在真实 WeKnora 上需要的最小能力面。
//
// 只声明本包真正用到的方法：生产里传 knowledgeService，测试里传桩。这样本包的
// 逻辑不必拖着一整个服务面（几十个方法）才能被测。
type WeKnoraService interface {
	CreateKnowledgeFromFile(
		ctx context.Context,
		kbID string,
		file *multipart.FileHeader,
		metadata map[string]string,
		enableMultimodel *bool,
		customFileName string,
		tagIDs []string,
		channel string,
		processOverrides *types.KnowledgeProcessOverrides,
	) (*types.Knowledge, error)
	ListKnowledgeByKnowledgeBaseID(ctx context.Context, kbID string) ([]*types.Knowledge, error)
}

// 材料在底座元数据里留下的两个键。
//
// 有了它们，人在底座上看到一条知识时能直接回指「这是哪份材料」，不必再猜哪个
// 文件是它。
const (
	MetadataMaterialID  = "lingdoc_material_id"
	MetadataMaterialSHA = "lingdoc_material_sha256"
)

// WeKnoraBottomLayer 把材料接入落到真实的 WeKnora 知识服务上。
type WeKnoraBottomLayer struct {
	service  WeKnoraService
	tenantID uint64
	channel  string
}

// NewWeKnoraBottomLayer 组装真实底座适配器。
func NewWeKnoraBottomLayer(service WeKnoraService, tenantID uint64) *WeKnoraBottomLayer {
	return &WeKnoraBottomLayer{service: service, tenantID: tenantID, channel: types.ChannelAPI}
}

// FindByMaterial 按材料在底座上的确定性文件名找已有的那条。
//
// 文件名里带着材料身份的哈希，所以「同名」就等于「同一份材料」：不需要另找一处
// 索引，也不会把同名不同内容的材料认成同一份。
func (b *WeKnoraBottomLayer) FindByMaterial(ctx context.Context, kbID string, material Material) (*MaterialKnowledge, error) {
	list, err := b.service.ListKnowledgeByKnowledgeBaseID(b.scope(ctx), kbID)
	if err != nil {
		return nil, err
	}
	for _, knowledge := range list {
		if knowledge == nil || knowledge.FileName != material.FileName {
			continue
		}
		found := toMaterialKnowledge(knowledge)
		return &found, nil
	}
	return nil, nil
}

// UploadMaterial 把材料送进底座。
//
// 底座按内容哈希去重：同一份材料重复接入会返回既有那条（重复错误），这里把它
// 翻译成「复用了已有那条」，不是失败——材料接进去了，就是接进去了。
func (b *WeKnoraBottomLayer) UploadMaterial(ctx context.Context, kbID string, material Material) (UploadResult, error) {
	file, err := fileHeader(material.FilePath, material.FileName)
	if err != nil {
		return UploadResult{}, err
	}
	knowledge, err := b.service.CreateKnowledgeFromFile(
		b.scope(ctx), kbID, file, b.metadata(material), nil, material.FileName, nil, b.channel, nil,
	)
	if err != nil {
		var duplicate *types.DuplicateKnowledgeError
		if errors.As(err, &duplicate) && duplicate.Knowledge != nil {
			return UploadResult{Knowledge: toMaterialKnowledge(duplicate.Knowledge), Reused: true}, nil
		}
		return UploadResult{}, err
	}
	if knowledge == nil {
		return UploadResult{}, fmt.Errorf("%w: 底座没有返回材料", ErrInvalidMaterial)
	}
	return UploadResult{Knowledge: toMaterialKnowledge(knowledge)}, nil
}

// scope 把租户放进上下文。
//
// 底座的写入按上下文里的租户分桶（types.TenantIDContextKey）；调用方没放就补上，
// 免得底座取租户时炸掉。
func (b *WeKnoraBottomLayer) scope(ctx context.Context) context.Context {
	if _, ok := types.TenantIDFromContext(ctx); ok {
		return ctx
	}
	return types.WithExecutionTenant(ctx, b.tenantID)
}

// metadata 是这条材料在底座上留下的溯源信息。
func (b *WeKnoraBottomLayer) metadata(material Material) map[string]string {
	return map[string]string{
		MetadataMaterialID:  material.ID.String(),
		MetadataMaterialSHA: material.ID.SHA256,
	}
}

// toMaterialKnowledge 把底座的knowledge收成本包用的形状。
func toMaterialKnowledge(knowledge *types.Knowledge) MaterialKnowledge {
	if knowledge == nil {
		return MaterialKnowledge{}
	}
	return MaterialKnowledge{
		ID:       knowledge.ID,
		Title:    knowledge.Title,
		FileName: knowledge.FileName,
		FileHash: knowledge.FileHash,
		FileSize: knowledge.FileSize,
	}
}

// fileHeader 把本地文件包成底座要的 multipart 文件头。
//
// 底座的接入口吃的是 HTTP 上传的那份东西，这里在进程内造一份同样的：文件名是
// 材料的确定性文件名（底座靠扩展名选解析引擎）。
func fileHeader(path, fileName string) (*multipart.FileHeader, error) {
	source, err := os.Open(path)
	if err != nil {
		return nil, fmt.Errorf("%w: 打开材料文件 %s: %v", ErrInvalidMaterial, path, err)
	}
	defer func() { _ = source.Close() }()

	var body bytes.Buffer
	writer := multipart.NewWriter(&body)
	header := make(textproto.MIMEHeader)
	header.Set("Content-Disposition", fmt.Sprintf(`form-data; name="file"; filename="%s"`, fileName))
	header.Set("Content-Type", "application/octet-stream")
	part, err := writer.CreatePart(header)
	if err != nil {
		return nil, fmt.Errorf("构造上传体: %w", err)
	}
	if _, err := io.Copy(part, source); err != nil {
		return nil, fmt.Errorf("%w: 读取材料文件 %s: %v", ErrInvalidMaterial, path, err)
	}
	if err := writer.Close(); err != nil {
		return nil, fmt.Errorf("收尾上传体: %w", err)
	}

	form, err := multipart.NewReader(&body, writer.Boundary()).ReadForm(int64(body.Len()) + 1024)
	if err != nil {
		return nil, fmt.Errorf("解析上传体: %w", err)
	}
	files := form.File["file"]
	if len(files) == 0 {
		return nil, fmt.Errorf("%w: 上传体里没有文件", ErrInvalidMaterial)
	}
	return files[0], nil
}
