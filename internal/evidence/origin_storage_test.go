package evidence

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/Tencent/WeKnora/internal/types"
)

// ---------------------------------------------------------------------------
// The origin text of an uploaded material lives in storage.
//
// The material's file path is a storage reference ("resource://…"), not a
// filesystem path, so a reader that only opens local files can never prove a
// quote against the original. Every source of such a material then reports
// "not a stable original" (verbatim = false), and the anchor that would
// carry the quote cannot be saved (#485 / D1).
//
// The reader that knows how to fetch the bytes is an optional capability, so
// the local-path case keeps working unchanged.
// ---------------------------------------------------------------------------

// storageBackedKnowledge is a reader whose material lives in storage: the
// bytes come from the storage side, and the file path is a reference.
type storageBackedKnowledge struct {
	bytes    []byte
	bytesErr error
	present  bool
	calls    int
}

func (f *storageBackedKnowledge) KnowledgeForOrigin(_ context.Context, _ string) (*types.Knowledge, error) {
	return &types.Knowledge{
		ID:       "k-1",
		FilePath: "resource://handle-1",
		FileType: "md",
	}, nil
}

func (f *storageBackedKnowledge) UsesBuiltinConverter(_ context.Context, _ string) (bool, error) {
	return true, nil
}

func (f *storageBackedKnowledge) OriginBytes(_ context.Context, _ string) ([]byte, bool, error) {
	f.calls++
	if f.bytesErr != nil {
		return nil, false, f.bytesErr
	}
	if f.bytes == nil {
		return nil, false, nil
	}
	return f.bytes, true, nil
}

func TestOriginReaderReadsAStorageBackedMaterial(t *testing.T) {
	origin := "# 合成材料\n\n正文第一段。\n"
	reader := NewOriginReader(&storageBackedKnowledge{bytes: []byte(origin)})

	text, ok, err := reader.OriginText(context.Background(), "k-1")

	if err != nil {
		t.Fatalf("OriginText: %v", err)
	}
	if !ok {
		t.Fatal("a storage-backed material must be readable: the quote cannot be proven otherwise")
	}
	if text != origin {
		t.Fatalf("origin text = %q, want %q", text, origin)
	}
}

func TestOriginReaderDegradesWhenTheBytesAreNotAvailable(t *testing.T) {
	reader := NewOriginReader(&storageBackedKnowledge{bytes: nil})

	text, ok, err := reader.OriginText(context.Background(), "k-1")

	if err != nil {
		t.Fatalf("OriginText: %v", err)
	}
	if ok {
		t.Fatalf("without the bytes there is no origin, got %q", text)
	}
}

// The local-path reader (the pre-storage case) keeps working: the bytes are
// read from the file the path points at.
func TestOriginReaderStillReadsALocalFilePath(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "material.md")
	origin := "# 本地材料\n\n正文。\n"
	if err := os.WriteFile(path, []byte(origin), 0o600); err != nil {
		t.Fatal(err)
	}
	reader := NewOriginReader(&localPathKnowledge{path: path})

	text, ok, err := reader.OriginText(context.Background(), "k-1")

	if err != nil || !ok {
		t.Fatalf("OriginText: ok=%v err=%v", ok, err)
	}
	if text != origin {
		t.Fatalf("origin text = %q, want %q", text, origin)
	}
}

// A reader that cannot answer does not remove the local path.
//
// "The storage side could not read it" means "this road is blocked", not
// "this material has no original". A material that predates storage keeps
// its original on disk, and a storage reader that answers "no" must not
// take that road away from it.
func TestOriginReaderFallsBackToTheLocalPathWhenTheStorageSideCannotAnswer(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "material.md")
	origin := "# 本地材料\n\n正文。\n"
	if err := os.WriteFile(path, []byte(origin), 0o600); err != nil {
		t.Fatal(err)
	}
	reader := NewOriginReader(&storageBackedKnowledgeWithPath{path: path})

	text, ok, err := reader.OriginText(context.Background(), "k-1")

	if err != nil {
		t.Fatalf("OriginText: %v", err)
	}
	if !ok {
		t.Fatal("the local path must still be readable when the storage side cannot answer")
	}
	if text != origin {
		t.Fatalf("origin text = %q, want %q", text, origin)
	}
}

// storageBackedKnowledgeWithPath is a reader that knows about storage and
// cannot answer — and whose material still has a local file path.
type storageBackedKnowledgeWithPath struct {
	path string
}

func (f *storageBackedKnowledgeWithPath) KnowledgeForOrigin(_ context.Context, _ string) (*types.Knowledge, error) {
	return &types.Knowledge{ID: "k-1", FilePath: f.path, FileType: "md"}, nil
}

func (f *storageBackedKnowledgeWithPath) UsesBuiltinConverter(_ context.Context, _ string) (bool, error) {
	return true, nil
}

func (f *storageBackedKnowledgeWithPath) OriginBytes(_ context.Context, _ string) ([]byte, bool, error) {
	return nil, false, nil
}

// localPathKnowledge is a reader that only knows a local file path.
type localPathKnowledge struct {
	path string
}

func (f *localPathKnowledge) KnowledgeForOrigin(_ context.Context, _ string) (*types.Knowledge, error) {
	return &types.Knowledge{ID: "k-1", FilePath: f.path, FileType: "md"}, nil
}

func (f *localPathKnowledge) UsesBuiltinConverter(_ context.Context, _ string) (bool, error) {
	return true, nil
}
