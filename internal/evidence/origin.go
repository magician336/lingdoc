package evidence

import (
	"context"
	"os"
	"strings"
	"unicode/utf8"

	"github.com/Tencent/WeKnora/internal/infrastructure/chunker"
	"github.com/Tencent/WeKnora/internal/types"
)

// maxOriginBytes 是直读上限。原文是给逐字比对用的，不是给灌进内存用的：
// 超过这个大小就降弱档，而不是把一次请求变成一次大文件读取。
const maxOriginBytes = 8 << 20 // 8 MiB

// MaxOriginBytes 是直读上限的公开形式：读取原文的一方（存储层）需要同一条
// 上限，否则它会把一份多大的文件读进内存就由它决定了。
const MaxOriginBytes = maxOriginBytes

// directReadExtensions 是走 Go 侧 SimpleFormatReader 的扩展名——它把这几种
// 文件**原样**返回成 MarkdownContent（`docparser/builtin_converter.go:57-60`），
// 不经过 Python 解析服务，也不做格式转换。
//
// 判据不是「看起来像文本」，而是「解析链路取的就是这些字节」：csv/json 同样是
// 文本，但那里有 csvToMarkdown/jsonToMarkdown 明确改写（同文件 61-72 行），
// rune 偏移整体错位，所以不在这一档。
var directReadExtensions = map[string]struct{}{
	"txt":      {},
	"text":     {}, // 与 txt 走同一分支
	"md":       {},
	"markdown": {},
}

// KnowledgeReader 取一份资料的底座事实（类型、文件位置、当前解析状态）。
//
// 契约：资料不存在返回 (nil, nil)，**不是错误**。取不到原文对调用方就是
// 弱档，而底座真的故障（连接断了）必须如实返回错误——把故障吞成弱档，
// 等于把一次事故说成「这份资料只能弱档」。
//
// 方法名刻意与仓储的 GetKnowledgeByIDOnly 不同：仓储对缺失返回
// ErrKnowledgeNotFound，需要有意识地翻成 (nil, nil)。名字不一样，
// 接线的人就不会以为仓储直接满足这个端口。
type KnowledgeReader interface {
	KnowledgeForOrigin(ctx context.Context, knowledgeID string) (*types.Knowledge, error)

	// UsesBuiltinConverter 回答「这份资料的正文在切分前是否由内置 Go 转换器直出」。
	//
	// 只有明确为 true 才成立强档。KB 级的 parser_engine_rules 能把 txt/md 改派给
	// 别的引擎（`knowledge_process.go:3771` 的 ResolveParserEngine），那时取回来的是
	// 引擎产出的 markdown、不是文件字节，偏移整体错位，强档会产出**假的 stale**。
	//
	// 实现方（接线处）在 KB 与过程覆盖上取 `eff.ChunkingConfig.ResolveParserEngine(fileType)`：
	// 空串与显式 simple 均可由 Go 直读文本；其他引擎须降档。
	// 判不定时返回 (false, nil)——保守一侧是降档，不是失败，别把它做成错误。
	UsesBuiltinConverter(ctx context.Context, knowledgeID string) (bool, error)
}

// OriginPresenceReader lets callers distinguish a missing knowledge row from a
// present row whose original text cannot be reconstructed. The latter is a
// legitimate weak-evidence case; the former must never be treated as one.
type OriginPresenceReader interface {
	OriginKnowledgePresent(ctx context.Context, knowledgeID string) (bool, error)
}

// OriginBytesReader is an optional capability of the knowledge reader: it
// returns the source bytes of a material.
//
// The file path of an **uploaded** material is a storage reference
// ("resource://…"), not a filesystem path, so reading the bytes needs the
// storage layer.
//
// The two negative answers mean different things, and the difference matters:
//
//   - ok=false, err=nil — "I could not read it" (the file is gone, the
//     storage backend cannot serve it, the material is not file-backed).
//     The caller degrades to the self-consistency tier instead of claiming
//     that a quote was proven against the original.
//   - err!=nil — "the base failed" (the storage connection is broken). This
//     is deliberately **not** degraded: swallowing it would report an
//     incident as "this material can only be weak evidence", the same
//     distinction the KnowledgeReader port draws.
//
// The reader is optional on purpose: a reader that only knows a local path
// (the pre-storage case) keeps working through the local-file fallback.
type OriginBytesReader interface {
	OriginBytes(ctx context.Context, knowledgeID string) (data []byte, ok bool, err error)
}

// OriginSnapshotReader returns the parser-aligned origin text and the exact
// raw bytes from the same read. Consumers that bind a digest to quote offsets
// must use one snapshot to avoid a rewrite between two independent reads.
type OriginSnapshotReader interface {
	OriginSnapshot(ctx context.Context, knowledgeID string) (text string, raw []byte, ok bool, err error)
}

// NewOriginReader 组装固定实现：手工资料的正文取自 metadata；直接读取的
// 上传文本返回与分块索引一致的 LF 规范化文本。其余一律 ok=false。
func NewOriginReader(knowledge KnowledgeReader) OriginReader {
	return &fileOriginReader{knowledge: knowledge}
}

type fileOriginReader struct {
	knowledge KnowledgeReader
}

func (r *fileOriginReader) OriginKnowledgePresent(ctx context.Context, knowledgeID string) (bool, error) {
	if knowledgeID == "" {
		return false, nil
	}
	row, err := r.knowledge.KnowledgeForOrigin(ctx, knowledgeID)
	if err != nil {
		return false, err
	}
	return row != nil, nil
}

func (r *fileOriginReader) OriginText(ctx context.Context, knowledgeID string) (string, bool, error) {
	if knowledgeID == "" {
		return "", false, nil
	}
	row, err := r.knowledge.KnowledgeForOrigin(ctx, knowledgeID)
	if err != nil {
		return "", false, err
	}
	if row == nil {
		return "", false, nil
	}

	// 手工资料优先：它的正文在 metadata 里，FilePath 通常为空。
	if row.IsManual() {
		return manualOrigin(row)
	}
	return r.fileOrigin(ctx, row)
}

// OriginSnapshot returns parser-aligned text and the exact raw bytes that
// produced it, from a single storage read. Manual records and transformed
// formats deliberately do not provide a verifiable original-byte snapshot.
func (r *fileOriginReader) OriginSnapshot(ctx context.Context, knowledgeID string) (string, []byte, bool, error) {
	if knowledgeID == "" {
		return "", nil, false, nil
	}
	row, err := r.knowledge.KnowledgeForOrigin(ctx, knowledgeID)
	if err != nil || row == nil {
		return "", nil, false, err
	}
	if row.IsManual() {
		return "", nil, false, nil
	}
	if _, ok := directReadExtensions[normalizeExtension(row.FileType)]; !ok {
		return "", nil, false, nil
	}
	builtin, err := r.knowledge.UsesBuiltinConverter(ctx, row.ID)
	if err != nil || !builtin {
		return "", nil, false, err
	}
	raw, ok, err := r.originBytes(ctx, row)
	if err != nil || !ok {
		return "", nil, false, err
	}
	text, ok := UploadedOriginForIndex(raw)
	if !ok {
		return "", nil, false, nil
	}
	return text, raw, true, nil
}

// manualOrigin 取手工资料的正文。判据与文件那条同构，但**要多镜像一步**。
//
// 手工正文并不因为「是手工写的」就天然可直读：进切分前它有四步改写
// （`knowledge_create.go:1237-1274`），而分块器切的是改写之后的文本，
// 所以 chunk 的 rune 偏移是相对改写结果、不是相对 metadata 里的原文：
//
//  1. `clean := strings.TrimSpace(content)`（:1237）——**纯函数，这里照样做一遍**，
//     取回的字串就与切分输入对齐了；
//  2. data-URI 图片被解析成存储地址（:1241）；
//  3. 远程 http(s) 图片被解析成存储地址（:1249）；
//  4. `chunker.NormalizeLineEndings(clean)`（:1274，该处注释自认手工输入的 CRLF 是常态）。
//
// 后三步要读文件系统、要联网、重建不出同样结果，所以只能**排除**：带图片引用
// 或 CRLF 的正文一律降档。不这么做就会判出假的 stale——告诉用户「这句话不再
// 出自原文」，而事实只是换行被归一了。
func manualOrigin(row *types.Knowledge) (string, bool, error) {
	meta, err := row.ManualMetadata()
	if err != nil || meta == nil {
		// 元数据坏了就该降档，而不是让整次检索失败：正文取不回来是
		// 定位能力的问题，不是这次请求的问题。
		return "", false, nil
	}
	clean := strings.TrimSpace(meta.Content)
	if clean == "" {
		return "", false, nil
	}
	text, ok := VerbatimOrigin([]byte(clean))
	return text, ok, nil
}

func (r *fileOriginReader) fileOrigin(ctx context.Context, row *types.Knowledge) (string, bool, error) {
	if row.FilePath == "" {
		return "", false, nil
	}
	if _, ok := directReadExtensions[normalizeExtension(row.FileType)]; !ok {
		return "", false, nil
	}

	// 「扩展名走直读分支」只说明默认路由下如此：KB 规则可以把这类文件改派给
	// 别的引擎，那时切分输入是引擎产出的文本。问不到就降档。
	builtin, err := r.knowledge.UsesBuiltinConverter(ctx, row.ID)
	if err != nil {
		return "", false, err
	}
	if !builtin {
		return "", false, nil
	}

	data, ok, err := r.originBytes(ctx, row)
	if err != nil {
		return "", false, err
	}
	if !ok {
		return "", false, nil
	}
	text, provable := UploadedOriginForIndex(data)
	if !provable {
		return "", false, nil
	}
	return text, true, nil
}

// UploadedOriginForIndex reconstructs the exact rune-coordinate space used by
// the built-in uploaded-text ingestion path: line endings are canonicalized
// to LF before chunking. Callers must still prove the uploaded bytes were read
// through a direct text parser; this function alone cannot establish that.
// Quotes returned against this representation use LF, not original file bytes.
// All other changes performed by ingestion remain fail-closed.
func UploadedOriginForIndex(data []byte) (string, bool) {
	if !utf8.Valid(data) {
		return "", false
	}
	text := string(data)
	if strings.ContainsRune(text, 0) || hasImageRewrite(text) {
		return "", false
	}
	return chunker.NormalizeLineEndings(text), true
}

// originBytes 取这份资料的源文件字节。
//
// 优先问读取方（OriginBytesReader）：上传进来的资料，其 file_path 是存储
// 引用（resource://…）而不是文件系统路径，只有存储层能把它读成字节。
//
// 读取方**答不出**时退回本地文件路径——那是存储化之前的老路径，仍然要能用。
// 这一步不能省：答不出不等于「这份资料没有原文」，只等于「这一条路读不到」。
// 省掉它，存储读取方一旦答不出，存储化之前那些本地路径的资料就会全部
// 降成「取不到原文」，而它们的原文明明就在磁盘上。
func (r *fileOriginReader) originBytes(ctx context.Context, row *types.Knowledge) ([]byte, bool, error) {
	if reader, ok := r.knowledge.(OriginBytesReader); ok {
		data, readable, err := reader.OriginBytes(ctx, row.ID)
		if err != nil {
			return nil, false, err
		}
		if readable {
			return data, true, nil
		}
	}
	info, err := os.Stat(row.FilePath)
	if err != nil || info.IsDir() || info.Size() == 0 || info.Size() > maxOriginBytes {
		return nil, false, nil
	}
	data, err := os.ReadFile(row.FilePath)
	if err != nil {
		return nil, false, nil
	}
	if len(data) == 0 || len(data) > maxOriginBytes {
		// 落到这一步说明文件在 Stat 与 Read 之间变了。按降档处理。
		return nil, false, nil
	}
	return data, true, nil
}

// VerbatimOrigin 判定文本是否未经换行或其他改写而进入切分链路。
//
// 读到文本还不够：切分用的是**改写后**的文本。改写只有三处，都查得到
// （`knowledge_process.go:3637-3639` 及其上游）：
//
//  1. `common.CleanInvalidUTF8`（`common/tools.go:189`）丢掉非法 UTF-8 字节与 NUL；
//  2. `chunker.NormalizeLineEndings`（`infra/chunker/strategy.go:207`）把 \r\n 与 \r 归一成 \n；
//  3. 图片解析（`docparser/image_resolver.go`）把图片引用改写成对象存储地址。
//
// 任何一条会动到这串字节，rune 偏移就整体错位，此时声称强档会产出**假的 stale**
// ——它告诉用户「这句话不再出自原文」，而事实上只是因为换行被归一了。
// 所以这四条逐条排除，而不是赌一把。
//
// BOM 不在排除之列：U+FEFF 是合法 rune、不是 NUL，上面两步都不会动它，
// 于是它留在两侧的文本里，偏移一致。
//
// 手工内容仍沿用此保守判据。上传文本使用 UploadedOriginForIndex 将
// 可证明的换行变化映射到切分坐标空间，仍需传入原始上传字节。
func VerbatimOrigin(data []byte) (string, bool) {
	if !utf8.Valid(data) {
		return "", false
	}
	text := string(data)
	if strings.ContainsRune(text, 0) || strings.Contains(text, "\r") {
		return "", false
	}
	if hasImageRewrite(text) {
		return "", false
	}
	return text, true
}

// hasImageRewrite 判图片解析会不会改写这段文本。触发面取自 image_resolver.go
// 的几个模式：Markdown 图片、HTML <img>、data URI。
func hasImageRewrite(text string) bool {
	return strings.Contains(text, "![") ||
		strings.Contains(strings.ToLower(text), "<img") ||
		strings.Contains(text, "data:image/")
}

// normalizeExtension 与底座 getFileType 的输出对齐：扩展名可能带点、大小写不定。
func normalizeExtension(fileType string) string {
	return strings.ToLower(strings.TrimPrefix(strings.TrimSpace(fileType), "."))
}
