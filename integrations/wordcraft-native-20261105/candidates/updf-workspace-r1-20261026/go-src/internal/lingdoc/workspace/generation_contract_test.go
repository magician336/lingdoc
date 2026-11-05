package workspace

import (
	"errors"
	"testing"

	"github.com/Tencent/WeKnora/internal/evidence"
	"github.com/Tencent/WeKnora/internal/lingdoc/generation"
)

func TestGenerationDeniedAssetsErrorPreservesContractReasons(t *testing.T) {
	tests := []struct {
		name      string
		denied    []evidence.DeniedAsset
		want      error
		wantItems []generation.DeniedAsset
	}{
		{name: "all not ready", denied: []evidence.DeniedAsset{{AssetID: "asset-1", Reason: evidence.DenyNotReady}}, want: generation.ErrAssetNotReady},
		{name: "unauthorized with reason", denied: []evidence.DeniedAsset{{AssetID: "asset-1", Reason: evidence.DenyNotAuthorized}}, want: generation.ErrAssetNotAuthorized, wantItems: []generation.DeniedAsset{{AssetID: "asset-1", Reason: "not_authorized"}}},
		{name: "mixed reasons preserve every item", denied: []evidence.DeniedAsset{{AssetID: "asset-1", Reason: evidence.DenyNotReady}, {AssetID: "asset-2", Reason: evidence.DenyNotFound}}, want: generation.ErrAssetNotAuthorized, wantItems: []generation.DeniedAsset{{AssetID: "asset-1", Reason: "not_ready"}, {AssetID: "asset-2", Reason: "not_found"}}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := generationDeniedAssetsError(tt.denied)
			if !errors.Is(err, tt.want) {
				t.Fatalf("error = %v, want %v", err, tt.want)
			}
			var denied *generation.AssetNotAuthorizedError
			if len(tt.wantItems) == 0 {
				if errors.As(err, &denied) {
					t.Fatalf("error = %T, want a typed not-ready error", err)
				}
				return
			}
			if !errors.As(err, &denied) {
				t.Fatalf("error = %T, want typed denied details", err)
			}
			if len(denied.Denied) != len(tt.wantItems) {
				t.Fatalf("denied = %+v, want %+v", denied.Denied, tt.wantItems)
			}
			for i := range tt.wantItems {
				if denied.Denied[i] != tt.wantItems[i] {
					t.Fatalf("denied[%d] = %+v, want %+v", i, denied.Denied[i], tt.wantItems[i])
				}
			}
		})
	}
}
