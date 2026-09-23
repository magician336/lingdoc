package evidence

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/Tencent/WeKnora/internal/types"
)

// 契约文件路径，相对本包目录。测试直接读它，不手抄字段名——
// 手抄的副本本身就是会漂移的东西。
const contractSpecPath = "../../docs/08-本轮实施方案/contracts/openapi.json"

func loadContractSchema(t *testing.T, name string) map[string]any {
	t.Helper()
	raw, err := os.ReadFile(filepath.Clean(contractSpecPath))
	if err != nil {
		t.Fatalf("读取契约 openapi.json: %v", err)
	}
	var spec struct {
		Components struct {
			Schemas map[string]map[string]any `json:"schemas"`
		} `json:"components"`
	}
	if err := json.Unmarshal(raw, &spec); err != nil {
		t.Fatalf("解析契约 openapi.json: %v", err)
	}
	schema, ok := spec.Components.Schemas[name]
	if !ok {
		t.Fatalf("契约里没有 schema %q", name)
	}
	return schema
}

func marshalToMap(t *testing.T, v any) map[string]any {
	t.Helper()
	raw, err := json.Marshal(v)
	if err != nil {
		t.Fatalf("序列化: %v", err)
	}
	var got map[string]any
	if err := json.Unmarshal(raw, &got); err != nil {
		t.Fatalf("反序列化: %v", err)
	}
	return got
}

// 本包的类型是对外契约形状的 Go 表达，序列化出去必须正好合契约。
// 契约对 Asset/Source 都写了 additionalProperties: false，所以键集合必须
// 逐字相等——多一个键（比如内部锚点漏出去）或少一个字段都算失败。
//
// 这条测试是被真事逼出来的：AssetRevision 原先写成 string，而契约是
// integer minimum 1。人工比对会漏，让它自己报。
func TestTypesSerializeToContractShape(t *testing.T) {
	resolver := NewSourceResolver(fakeOrigin{text: syntheticOrigin(t), ok: true})
	sources, err := resolver.Resolve(context.Background(), testAsset("a-1", AssetStateReady),
		[]*types.SearchResult{goalHit()})
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	if len(sources) != 1 {
		t.Fatalf("Resolve 返回 %d 条, want 1", len(sources))
	}

	for _, tc := range []struct {
		schema string
		value  any
	}{
		{"Asset", testAsset("a-1", AssetStateReady)},
		{"Source", sources[0]},
	} {
		t.Run(tc.schema, func(t *testing.T) {
			schema := loadContractSchema(t, tc.schema)
			props, _ := schema["properties"].(map[string]any)
			got := marshalToMap(t, tc.value)

			// 键集合必须与契约 properties 完全一致（additionalProperties: false）。
			for key := range got {
				if _, ok := props[key]; !ok {
					t.Errorf("多出契约没有的字段 %q", key)
				}
			}
			// required 字段必须都出现。
			if required, ok := schema["required"].([]any); ok {
				for _, r := range required {
					name, _ := r.(string)
					if _, present := got[name]; !present {
						t.Errorf("缺少 required 字段 %q", name)
					}
				}
			}
			// 逐字段核类型与枚举。
			for name, raw := range props {
				prop, _ := raw.(map[string]any)
				value, present := got[name]
				if !present {
					continue
				}
				switch prop["type"] {
				case "integer":
					if _, ok := value.(float64); !ok {
						t.Errorf("字段 %q 应为整数，实际 %T (%v)", name, value, value)
					}
				case "string":
					s, ok := value.(string)
					if !ok {
						t.Errorf("字段 %q 应为字符串，实际 %T (%v)", name, value, value)
						continue
					}
					if enum, ok := prop["enum"].([]any); ok {
						allowed := make([]string, 0, len(enum))
						for _, e := range enum {
							allowed = append(allowed, e.(string))
						}
						if !slicesContains(allowed, s) {
							t.Errorf("字段 %q 的值 %q 不在契约枚举 %v 内", name, s, allowed)
						}
					}
				}
			}
		})
	}
}

func slicesContains(haystack []string, needle string) bool {
	for _, h := range haystack {
		if h == needle {
			return true
		}
	}
	return false
}
