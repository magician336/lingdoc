package writingaction_test

import (
	"encoding/json"
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Tencent/WeKnora/internal/lingdoc/writingaction"
)

// 这组断言守的是「可选上下文的选择」这一组类型只有一个拥有者。
//
// 同一个概念——用户对这次执行要纳入的可选上下文集合——曾经有两套类型：生成
// 管道的 GenerationContextSelection 与选段管道的 RewriteContextSelection，
// 字段逐字相同。两套类型意味着两条管道各说各话：改一处形状要改两处，客户端
// 也要面对两种形状。
//
// 拥有者是 writingaction：它是动作词汇的拥有者，而「可选上下文」正是动作
// 上下文策略在请求侧的那一份形状。

// contextSelectionTypes 是这一组类型的名字。
var contextSelectionTypes = map[string]string{
	"ContextSelection":    "可选上下文的选择",
	"ContextEntry":        "一个上下文条目",
	"DroppedContextEntry": "一个没有进入模型请求的可选上下文条目",
}

// TestContextSelectionTypesHaveExactlyOneOwner 断言这一组类型只在 writingaction
// 包里定义。
//
// 别处再定义一份 ⇒ 那个包自己抄了一份形状 ⇒ 红。
func TestContextSelectionTypesHaveExactlyOneOwner(t *testing.T) {
	// 相对路径里带 ".." 时 Rel 不可靠：一律先取绝对路径再算相对路径。
	repoRoot, err := filepath.Abs(filepath.Clean("../../.."))
	if err != nil {
		t.Fatalf("解析仓库根目录失败：%v", err)
	}
	base := filepath.Join(repoRoot, "internal", "lingdoc")
	owners := map[string][]string{}
	fset := token.NewFileSet()
	err = filepath.WalkDir(base, func(path string, entry os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if entry.IsDir() {
			return nil
		}
		if !strings.HasSuffix(entry.Name(), ".go") || strings.HasSuffix(entry.Name(), "_test.go") {
			return nil
		}
		source, readErr := os.ReadFile(path)
		if readErr != nil {
			return readErr
		}
		file, parseErr := parser.ParseFile(fset, path, source, parser.SkipObjectResolution)
		if parseErr != nil {
			return parseErr
		}
		rel, relErr := filepath.Rel(repoRoot, path)
		if relErr != nil {
			return relErr
		}
		rel = filepath.ToSlash(rel)
		for _, name := range definedTypeNames(file) {
			if _, guarded := contextSelectionTypes[name]; guarded {
				owners[name] = append(owners[name], rel)
			}
		}
		return nil
	})
	if err != nil {
		t.Fatalf("扫描 internal/lingdoc 失败：%v", err)
	}
	for name, meaning := range contextSelectionTypes {
		found := owners[name]
		switch {
		case len(found) == 0:
			t.Errorf("类型 %s（%s）没有任何拥有者，守护已经空转", name, meaning)
		case len(found) > 1:
			t.Errorf("类型 %s（%s）有多个拥有者：%s", name, meaning, strings.Join(found, ", "))
		case found[0] != "internal/lingdoc/writingaction/context_selection.go":
			t.Errorf("类型 %s（%s）的拥有者是 %s，应当是 internal/lingdoc/writingaction", name, meaning, found[0])
		}
	}
}

// definedTypeNames 列出这条文件定义的类型名。
func definedTypeNames(file *ast.File) []string {
	var names []string
	for _, decl := range file.Decls {
		gen, ok := decl.(*ast.GenDecl)
		if !ok || gen.Tok != token.TYPE {
			continue
		}
		for _, spec := range gen.Specs {
			typeSpec, ok := spec.(*ast.TypeSpec)
			if !ok {
				continue
			}
			names = append(names, typeSpec.Name.Name)
		}
	}
	return names
}

// TestContextSelectionJSONShapeIsUnchanged 断言这一组类型的 JSON 形状逐字不变。
//
// 形状是客户端看得到的契约：字段名与 tag 都不能变。改形状就是改 API。
func TestContextSelectionJSONShapeIsUnchanged(t *testing.T) {
	selection := writingaction.ContextSelection{
		Include: map[writingaction.ContextItem][]writingaction.ContextEntry{
			writingaction.ContextAssets: {{ID: "asset-1"}, {ID: "asset-2", Version: 7}},
		},
		Exclude: map[writingaction.ContextItem][]writingaction.ContextEntry{
			writingaction.ContextCards: {{ID: "card-1", Version: 3}},
		},
	}
	encoded, err := json.Marshal(selection)
	if err != nil {
		t.Fatalf("编码上下文选择失败：%v", err)
	}
	want := `{"include":{"assets":[{"id":"asset-1"},{"id":"asset-2","version":7}]},` +
		`"exclude":{"cards":[{"id":"card-1","version":3}]}}`
	if string(encoded) != want {
		t.Errorf("上下文选择的 JSON 形状变了：\n得到 %s\n期望 %s", string(encoded), want)
	}

	dropped := writingaction.DroppedContextEntry{
		Category: writingaction.ContextCards,
		ID:       "card-2",
		Reason:   "not_authorized",
	}
	encoded, err = json.Marshal(dropped)
	if err != nil {
		t.Fatalf("编码被丢弃的上下文条目失败：%v", err)
	}
	want = `{"category":"cards","id":"card-2","reason":"not_authorized"}`
	if string(encoded) != want {
		t.Errorf("被丢弃的上下文条目的 JSON 形状变了：\n得到 %s\n期望 %s", string(encoded), want)
	}

	// 空的选择整个字段不出现：客户端不带 context 时，请求体逐字不变。
	encoded, err = json.Marshal(struct {
		Context *writingaction.ContextSelection `json:"context,omitempty"`
	}{})
	if err != nil {
		t.Fatalf("编码可选的上下文选择失败：%v", err)
	}
	if string(encoded) != "{}" {
		t.Errorf("缺省的上下文选择没有整个字段不出现：%s", string(encoded))
	}
}
