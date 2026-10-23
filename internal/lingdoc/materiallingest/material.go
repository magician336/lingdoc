// Package materiallingest 把一份材料接入底座（WeKnora），并给出「处理就绪」的可断言事实。
//
// 它服务的场景是 Beta 放行链的 F01 节点：契约要绑定的不是一个人手写的
// knowledge_id，而是一份**真的经过接入与处理**的合成材料。为了让「同一套材料」
// 在多轮验收之间可复用，材料的身份必须与时间无关——这里用「固定标题前缀 +
// 文件内容 SHA-256」构造它。
//
// 判定「处理就绪」不在本包另起炉灶：状态一律经 evidence.ProcessingStateOf 从
// 底座的 parse_status 映射而来，就绪就是 evidence.AssetStateReady。这与绑定层
// （evidence.Bindings.RefreshAsset）走同一条链，所以「就绪」在这里和在 F01-06
// 的响应里是同一个判定，不会出现两套口径。
package materiallingest

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"os"
	"strings"
	"unicode"
)

// ErrInvalidMaterial 表示入参不成立：缺前缀、缺知识库、文件读不了。
var ErrInvalidMaterial = errors.New("invalid material")

// ErrMaterialHashMismatch 表示「运行前记录的材料哈希」与「运行时算出的文件哈希」
// 不一致。
//
// 两处不一致就是材料对不上号（材料 A 的哈希被记在材料 B 上），此时不能继续接入。
var ErrMaterialHashMismatch = errors.New("material hash mismatch")

// shortDigestLength 是标题里携带的哈希位数。
//
// 12 位够把同一批合成材料区分开，又不会让标题长到难读；完整哈希在
// MaterialID.SHA256 里，标题只是给人看的那一份。
const shortDigestLength = 12

// maxPrefixLength 是标题前缀的长度上限。
const maxPrefixLength = 48

// MaterialID 是材料的稳定身份：固定标题前缀 + 文件内容 SHA-256。
//
// 它刻意不含时间戳、不含随机数、也不含底座那条知识的 ID：同一份文件在任何一次
// 接入里都得到同一个 MaterialID。于是「同一套材料被后续多轮验收复用」有唯一依据，
// 而底座的知识 ID 只在这一次接入里有意义（换环境会变）。
type MaterialID struct {
	// Prefix 是调用方给的固定标题前缀（例如 "f01-synth-q"）。
	Prefix string
	// SHA256 是文件内容的十六进制 SHA-256（小写）。
	SHA256 string
}

// Identify 读一遍文件，算出这份材料的稳定身份。
//
// 文件读不了就返回错误，不返回半个身份：调用方拿到的每个 MaterialID 都对应一份
// 确实读过、确实算过哈希的文件。
func Identify(prefix, path string) (MaterialID, error) {
	clean := sanitizePrefix(prefix)
	if clean == "" {
		return MaterialID{}, fmt.Errorf("%w: 标题前缀不能为空", ErrInvalidMaterial)
	}
	sum, err := FileSHA256(path)
	if err != nil {
		return MaterialID{}, err
	}
	return MaterialID{Prefix: clean, SHA256: sum}, nil
}

// FileSHA256 算文件的十六进制 SHA-256（小写）。
func FileSHA256(path string) (string, error) {
	file, err := os.Open(path)
	if err != nil {
		return "", fmt.Errorf("%w: 打开材料文件 %s: %v", ErrInvalidMaterial, path, err)
	}
	defer func() { _ = file.Close() }()
	digest := sha256.New()
	if _, err := io.Copy(digest, file); err != nil {
		return "", fmt.Errorf("%w: 读取材料文件 %s: %v", ErrInvalidMaterial, path, err)
	}
	return hex.EncodeToString(digest.Sum(nil)), nil
}

// Title 是这份材料在底座上的标题：<前缀>-<哈希前 12 位>。
func (m MaterialID) Title() string {
	return m.Prefix + "-" + m.shortDigest()
}

// FileName 是这份材料在底座上使用的文件名：标题 + 原扩展名。
//
// 扩展名必须留：底座靠它选解析引擎，去了这份材料就解析不了。
func (m MaterialID) FileName(ext string) string {
	if ext == "" {
		return m.Title()
	}
	if !strings.HasPrefix(ext, ".") {
		ext = "." + ext
	}
	return m.Title() + ext
}

// String 是材料的完整标识：<前缀>-<完整哈希>。
//
// 它是跨轮次记录材料时该写下的那一个字符串：前缀让人认得出，哈希保证唯一。
func (m MaterialID) String() string {
	return m.Prefix + "-" + m.SHA256
}

// MatchesSHA256 判断实际哈希是否与期望一致（忽略大小写）。
func (m MaterialID) MatchesSHA256(expected string) bool {
	expected = strings.TrimSpace(expected)
	if expected == "" {
		return false
	}
	return strings.EqualFold(expected, m.SHA256)
}

// shortDigest 是标题里带的那一段哈希。
func (m MaterialID) shortDigest() string {
	if len(m.SHA256) <= shortDigestLength {
		return m.SHA256
	}
	return m.SHA256[:shortDigestLength]
}

// sanitizePrefix 把前缀收拾成底座文件名能接受的样子。
//
// 底座对文件名有输入校验（secutils.ValidateInput）：控制字符与 XSS 模式会被拒，
// 路径分隔符会让文件名跑偏；中文没问题，所以这里只替换掉那些字符，字母、数字
// （含中文）与连字符、下划线、点都留下。
func sanitizePrefix(prefix string) string {
	prefix = strings.TrimSpace(prefix)
	if prefix == "" {
		return ""
	}
	var builder strings.Builder
	for _, r := range prefix {
		switch {
		case unicode.IsLetter(r) || unicode.IsDigit(r):
			builder.WriteRune(r)
		case r == '-' || r == '_' || r == '.':
			builder.WriteRune(r)
		default:
			builder.WriteRune('-')
		}
	}
	clean := strings.Trim(builder.String(), "-")
	if len(clean) > maxPrefixLength {
		clean = clean[:maxPrefixLength]
	}
	return clean
}
