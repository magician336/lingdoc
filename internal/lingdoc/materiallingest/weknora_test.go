package materiallingest

import (
	"context"
	"mime/multipart"
	"testing"

	"github.com/Tencent/WeKnora/internal/types"
	"github.com/stretchr/testify/require"
)

// fakeWeKnoraService 是真实 WeKnora 知识服务在本包测试里的替身：只记下收到什么。
type fakeWeKnoraService struct {
	fileName  string
	metadata  map[string]string
	kbID      string
	knowledge *types.Knowledge
	err       error
}

func (s *fakeWeKnoraService) CreateKnowledgeFromFile(
	_ context.Context,
	kbID string,
	file *multipart.FileHeader,
	metadata map[string]string,
	_ *bool,
	customFileName string,
	_ []string,
	_ string,
	_ *types.KnowledgeProcessOverrides,
) (*types.Knowledge, error) {
	s.kbID = kbID
	s.fileName = file.Filename
	s.metadata = metadata
	if s.err != nil {
		return nil, s.err
	}
	if s.knowledge != nil {
		return s.knowledge, nil
	}
	return &types.Knowledge{
		ID:       "k-1",
		Title:    customFileName,
		FileName: file.Filename,
		FileHash: "md5-hash",
		FileSize: 12,
	}, nil
}

func (s *fakeWeKnoraService) ListKnowledgeByKnowledgeBaseID(
	_ context.Context, _ string,
) ([]*types.Knowledge, error) {
	return nil, nil
}

// TestWeKnoraBottomLayerUploadsWithDeterministicFileName 盯住上传用的文件名：
// 它必须带材料身份的哈希，底座才认得出「这是同一份材料」。
func TestWeKnoraBottomLayerUploadsWithDeterministicFileName(t *testing.T) {
	t.Parallel()
	path := writeMaterial(t, "synthetic.md", "# 合成材料\n")
	service := &fakeWeKnoraService{}
	bottom := NewWeKnoraBottomLayer(service, 7)

	id, err := Identify("f01-synth-q", path)
	require.NoError(t, err)
	material := Material{
		ID:       id,
		FilePath: path,
		FileName: id.FileName(".md"),
		FileSize: 12,
	}

	result, err := bottom.UploadMaterial(context.Background(), "kb-1", material)
	require.NoError(t, err)
	require.False(t, result.Reused)
	require.Equal(t, "kb-1", service.kbID)
	require.Equal(t, material.FileName, service.fileName)
	require.Equal(t, material.FileName, result.Knowledge.FileName)
	require.Equal(t, material.ID.SHA256, service.metadata[MetadataMaterialSHA])
	require.Equal(t, material.ID.String(), service.metadata[MetadataMaterialID])
}

// TestWeKnoraBottomLayerTreatsDuplicateAsReuse 盯住底座去重的那条路：重复接入
// 返回既有那条知识，不是失败。
func TestWeKnoraBottomLayerTreatsDuplicateAsReuse(t *testing.T) {
	t.Parallel()
	path := writeMaterial(t, "synthetic.md", "# 合成材料\n")
	service := &fakeWeKnoraService{
		err: types.NewDuplicateFileError(&types.Knowledge{
			ID: "k-existing", Title: "f01-synth-q-0123456789ab", FileName: "f01-synth-q-0123456789ab.md",
		}),
	}
	bottom := NewWeKnoraBottomLayer(service, 7)

	id, err := Identify("f01-synth-q", path)
	require.NoError(t, err)
	result, err := bottom.UploadMaterial(context.Background(), "kb-1", Material{
		ID: id, FilePath: path, FileName: id.FileName(".md"), FileSize: 12,
	})
	require.NoError(t, err)
	require.True(t, result.Reused)
	require.Equal(t, "k-existing", result.Knowledge.ID)
}

// TestWeKnoraBottomLayerPropagatesRealFailures 盯住别的错误不被吞掉：接不通就是
// 接不通，不能被当成「复用了已有那条」。
func TestWeKnoraBottomLayerPropagatesRealFailures(t *testing.T) {
	t.Parallel()
	path := writeMaterial(t, "synthetic.md", "# 合成材料\n")
	service := &fakeWeKnoraService{err: context.DeadlineExceeded}
	bottom := NewWeKnoraBottomLayer(service, 7)

	id, err := Identify("f01-synth-q", path)
	require.NoError(t, err)
	_, err = bottom.UploadMaterial(context.Background(), "kb-1", Material{
		ID: id, FilePath: path, FileName: id.FileName(".md"), FileSize: 12,
	})
	require.ErrorIs(t, err, context.DeadlineExceeded)
}
