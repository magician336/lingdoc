package workspace

import (
	"bytes"
	"context"
	"crypto/md5"
	"encoding/hex"
	"errors"
	"io"
	"strings"
	"testing"

	"github.com/Tencent/WeKnora/internal/evidence"
	"github.com/Tencent/WeKnora/internal/types"
)

// ---------------------------------------------------------------------------
// The storage seam of the origin reader.
//
// A material that was uploaded carries a storage reference ("resource://…") as
// its file path, not a filesystem path. The origin text — the text a quoted
// text is proven against — is therefore only obtainable through the storage
// layer. The database side of the workspace knows which file a material came
// from; the storage side knows what is in it.
//
// These cases pin that seam: the reader must actually go through the storage
// side, and every way the storage side can fail to answer must degrade to
// "no origin" (ok=false) rather than to a claim it could not prove.
// ---------------------------------------------------------------------------

// storageStub is the storage side: it answers with the bytes of the file, or
// with the way it fails.
type storageStub struct {
	data    []byte
	err     error
	nilFile bool
	calls   int
}

func (s *storageStub) GetKnowledgeFile(_ context.Context, _ string) (io.ReadCloser, string, error) {
	s.calls++
	if s.err != nil {
		return nil, "", s.err
	}
	if s.nilFile {
		return nil, "material.md", nil
	}
	return io.NopCloser(bytes.NewReader(s.data)), "material.md", nil
}

func TestOriginBytesReadsTheMaterialThroughStorage(t *testing.T) {
	origin := "# 合成材料\n\n正文第一段。\n"
	storage := &storageStub{data: []byte(origin)}
	reader := KnowledgeFileOriginReader{Service: storage}

	data, ok, err := reader.OriginBytes(context.Background(), "k-1")

	if err != nil {
		t.Fatalf("OriginBytes: %v", err)
	}
	if !ok {
		t.Fatal("a material that storage can answer for must be readable")
	}
	if string(data) != origin {
		t.Fatalf("bytes = %q, want %q", string(data), origin)
	}
	if storage.calls != 1 {
		t.Fatalf("the reader must go through the storage side exactly once, calls=%d", storage.calls)
	}
}

// The reader must not answer from a cache of its own: every question about a
// material is a question about the file as it is now.
func TestOriginBytesAsksStorageEveryTime(t *testing.T) {
	storage := &storageStub{data: []byte("first")}
	reader := KnowledgeFileOriginReader{Service: storage}

	if _, ok, _ := reader.OriginBytes(context.Background(), "k-1"); !ok {
		t.Fatal("the first read must succeed")
	}
	storage.data = []byte("second")
	data, ok, err := reader.OriginBytes(context.Background(), "k-1")
	if err != nil || !ok {
		t.Fatalf("the second read must succeed: ok=%v err=%v", ok, err)
	}
	if string(data) != "second" {
		t.Fatalf("bytes = %q, want %q", string(data), "second")
	}
}

// Every failure of the storage side degrades to "no origin". None of them may
// become an error the request fails on: the caller degrades to the
// self-consistency tier, which is the answer it gave before this reader
// existed.
func TestOriginBytesDegradesWhenStorageCannotAnswer(t *testing.T) {
	cases := []struct {
		name   string
		stub   *storageStub
		reason string
	}{
		{"the storage side fails", &storageStub{err: errors.New("storage unavailable")}, "storage error"},
		{"the storage side has no file", &storageStub{nilFile: true}, "nil file"},
		{"the file is empty", &storageStub{data: nil}, "empty file"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			reader := KnowledgeFileOriginReader{Service: c.stub}
			data, ok, err := reader.OriginBytes(context.Background(), "k-1")
			if err != nil {
				t.Fatalf("%s: a storage failure must not become a request failure: %v", c.reason, err)
			}
			if ok {
				t.Fatalf("%s: the reader must not claim an origin it could not read", c.reason)
			}
			if data != nil {
				t.Fatalf("%s: no origin means no bytes, got %d bytes", c.reason, len(data))
			}
		})
	}
}

// A file bigger than the direct-read ceiling is refused: the ceiling is
// shared with the local-path reader, so both tiers agree on what "too big
// to read" means.
func TestOriginBytesRefusesAFileAboveTheCeiling(t *testing.T) {
	storage := &storageStub{data: []byte(strings.Repeat("x", evidence.MaxOriginBytes+1))}
	reader := KnowledgeFileOriginReader{Service: storage}

	data, ok, err := reader.OriginBytes(context.Background(), "k-1")
	if err != nil {
		t.Fatalf("OriginBytes: %v", err)
	}
	if ok {
		t.Fatal("a file above the direct-read ceiling must not be read")
	}
	if data != nil {
		t.Fatalf("no origin means no bytes, got %d bytes", len(data))
	}
}

// Without a storage side there is no origin. The runtime assembles this
// reader, so the nil case is a real one (a runtime wired without the storage
// side must degrade, not panic).
func TestOriginBytesWithoutAStorageSide(t *testing.T) {
	reader := KnowledgeFileOriginReader{}
	data, ok, err := reader.OriginBytes(context.Background(), "k-1")
	if err != nil {
		t.Fatalf("OriginBytes: %v", err)
	}
	if ok || data != nil {
		t.Fatalf("without the storage side there is no origin: ok=%v bytes=%d", ok, len(data))
	}
}

// The whole chain: the storage side answers, and the origin reader turns that
// into the origin text the source re-check proves a quote against.
func TestOriginTextIsProvenAgainstTheStoredBytes(t *testing.T) {
	origin := "# 合成材料\n\n正文第一段。\n"
	storage := &storageStub{data: []byte(origin)}
	reader := evidence.NewOriginReader(&storageBackedKnowledge{
		filePath: "resource://handle-1",
		files:    KnowledgeFileOriginReader{Service: storage},
	})

	text, ok, err := reader.OriginText(context.Background(), "k-1")

	if err != nil {
		t.Fatalf("OriginText: %v", err)
	}
	if !ok {
		t.Fatal("the origin text must come from the stored bytes")
	}
	if text != origin {
		t.Fatalf("origin text = %q, want %q", text, origin)
	}
	if storage.calls != 1 {
		t.Fatalf("the origin text must come from the storage side, calls=%d", storage.calls)
	}
}

func TestOriginSnapshotUsesOneReadForTextAndRawBytes(t *testing.T) {
	storage := &storageStub{data: []byte("first\r\nsecond\r")}
	sum := md5.Sum(storage.data)
	reader := evidence.NewOriginReader(&storageBackedKnowledge{
		filePath: "resource://handle-1",
		fileHash: hex.EncodeToString(sum[:]),
		files:    KnowledgeFileOriginReader{Service: storage},
	})
	snapshotReader, ok := reader.(evidence.OriginSnapshotReader)
	if !ok {
		t.Fatal("origin reader must expose its atomic source snapshot")
	}
	text, raw, readable, err := snapshotReader.OriginSnapshot(context.Background(), "k-1")
	if err != nil || !readable {
		t.Fatalf("OriginSnapshot readable=%v err=%v", readable, err)
	}
	if text != "first\nsecond\n" || string(raw) != "first\r\nsecond\r" {
		t.Fatalf("snapshot text=%q raw=%q", text, raw)
	}
	if storage.calls != 1 {
		t.Fatalf("text and raw bytes must come from one storage read, calls=%d", storage.calls)
	}
}

// storageBackedKnowledge is a material whose file path is a storage
// reference, not a filesystem path — the shape an uploaded material has.
// Its bytes come from the storage side.
type storageBackedKnowledge struct {
	filePath string
	fileHash string
	files    KnowledgeFileOriginReader
}

func (k *storageBackedKnowledge) KnowledgeForOrigin(_ context.Context, _ string) (*types.Knowledge, error) {
	return &types.Knowledge{ID: "k-1", Type: "file", FileHash: k.fileHash, FilePath: k.filePath, FileType: "md"}, nil
}

func (k *storageBackedKnowledge) UsesBuiltinConverter(_ context.Context, _ string) (bool, error) {
	return true, nil
}

func (k *storageBackedKnowledge) OriginBytes(ctx context.Context, knowledgeID string) ([]byte, bool, error) {
	return k.files.OriginBytes(ctx, knowledgeID)
}
