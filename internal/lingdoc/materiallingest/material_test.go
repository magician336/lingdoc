package materiallingest

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

// TestIdentifyIsIndependentOfTime 盯住材料身份与时间无关：改文件的修改时间，
// 身份不变。身份变了就等于「每次接入都造出一份新材料」。
func TestIdentifyIsIndependentOfTime(t *testing.T) {
	t.Parallel()
	path := writeMaterial(t, "synthetic.md", "# 合成材料\n")

	first, err := Identify("f01-synth-q", path)
	require.NoError(t, err)

	stamp := time.Now().Add(-72 * time.Hour)
	require.NoError(t, os.Chtimes(path, stamp, stamp))

	second, err := Identify("f01-synth-q", path)
	require.NoError(t, err)
	require.Equal(t, first, second)
	require.Equal(t, "f01-synth-q-"+first.SHA256[:12], first.Title())
	require.Equal(t, "f01-synth-q-"+first.SHA256, first.String())
}

// TestIdentifyChangesWithContent 盯住另一面：内容变了，身份必须跟着变。
func TestIdentifyChangesWithContent(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	first := filepath.Join(dir, "a.md")
	second := filepath.Join(dir, "b.md")
	require.NoError(t, os.WriteFile(first, []byte("材料一"), 0o600))
	require.NoError(t, os.WriteFile(second, []byte("材料二"), 0o600))

	firstID, err := Identify("f01-synth-q", first)
	require.NoError(t, err)
	secondID, err := Identify("f01-synth-q", second)
	require.NoError(t, err)
	require.NotEqual(t, firstID, secondID)
}

// TestIdentifySanitizesTitlePrefix 盯住前缀的收拾：底座对文件名有输入校验，
// 前缀里带路径分隔符会被拒，所以就地替换成连字符。
func TestIdentifySanitizesTitlePrefix(t *testing.T) {
	t.Parallel()
	path := writeMaterial(t, "synthetic.md", "# 合成材料\n")

	id, err := Identify("f01 合成/材料", path)
	require.NoError(t, err)
	require.Equal(t, "f01-合成-材料", id.Prefix)
	require.NotContains(t, id.Title(), "/")
}

// TestFileNameKeepsExtension 盯住扩展名：底座靠它选解析引擎，去了就解析不了。
func TestFileNameKeepsExtension(t *testing.T) {
	t.Parallel()
	id := MaterialID{Prefix: "f01-synth-q", SHA256: "0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef"}
	require.Equal(t, "f01-synth-q-0123456789ab.md", id.FileName(".md"))
	require.Equal(t, "f01-synth-q-0123456789ab.md", id.FileName("md"))
	require.Equal(t, "f01-synth-q-0123456789ab", id.FileName(""))
}

// TestMatchesSHA256IgnoresCase 盯住哈希对账时的大小写宽容。
func TestMatchesSHA256IgnoresCase(t *testing.T) {
	t.Parallel()
	id := MaterialID{Prefix: "p", SHA256: "abcdef0123456789"}
	require.True(t, id.MatchesSHA256("ABCDEF0123456789"))
	require.True(t, id.MatchesSHA256("  abcdef0123456789 "))
	require.False(t, id.MatchesSHA256(""))
	require.False(t, id.MatchesSHA256("0000000000000000"))
}
