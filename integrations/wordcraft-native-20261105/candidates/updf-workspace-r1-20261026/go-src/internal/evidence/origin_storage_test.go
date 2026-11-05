package evidence

import (
	"context"
	"errors"
	"testing"

	"github.com/Tencent/WeKnora/internal/types"
)

type storageOriginKnowledge struct {
	*fakeKnowledge
	data    []byte
	readErr error
}

func (r storageOriginKnowledge) OriginBytes(context.Context, string) ([]byte, bool, error) {
	return r.data, r.data != nil, r.readErr
}

func TestOriginStorageReferencesRequireVerbatimBytes(t *testing.T) {
	for _, test := range []struct {
		name, text string
		strong     bool
	}{
		{"utf8", "原始资料。\n可核对的第二行。", true},
		{"normalized-line-endings", "第一行。\r\n第二行。", false},
		{"image-rewrite", "正文 ![image](https://example.invalid/image.png)", false},
	} {
		t.Run(test.name, func(t *testing.T) {
			reader := storageOriginKnowledge{
				fakeKnowledge: &fakeKnowledge{rows: map[string]*types.Knowledge{
					"source": {ID: "source", FilePath: "resource://opaque", FileType: "txt"},
				}},
				data: []byte(test.text),
			}
			text, strong, err := NewOriginReader(reader).OriginText(context.Background(), "source")
			if err != nil || strong != test.strong || strong && text != test.text {
				t.Fatalf("storage origin = %q %v %v", text, strong, err)
			}
		})
	}
}

func TestOriginStorageFailureIsNotReportedAsWeakEvidence(t *testing.T) {
	want := errors.New("storage catalog unavailable")
	reader := storageOriginKnowledge{
		fakeKnowledge: &fakeKnowledge{rows: map[string]*types.Knowledge{
			"source": {ID: "source", FilePath: "resource://opaque", FileType: "md"},
		}}, readErr: want,
	}
	if _, ok, err := NewOriginReader(reader).OriginText(context.Background(), "source"); ok || !errors.Is(err, want) {
		t.Fatalf("storage failure was swallowed: %v %v", ok, err)
	}
}
