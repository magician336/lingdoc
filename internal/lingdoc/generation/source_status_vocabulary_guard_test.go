package generation

import (
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"github.com/Tencent/WeKnora/internal/evidence"
)

// 这组断言守的是「来源可用」这个词表**只有一个拥有者**。
//
// 「来源可用」是仓内**一个**概念：一条来源此刻能不能被当作证据指回原文。它的词表
// 由 `internal/evidence` 拥有（`evidence.SourceStatus` / `evidence.SourceAvailable`）。
//
// 词表一旦在别处被抄一份取值，两处就会各说各话：工作区装配输入时产出
// `string(evidence.SourceAvailable)`，生成面若拿自己抄的 `"available"` 去比，
// 那么词表一改（`SourceAvailable` 改成别的值），工作区不再产出可用的来源，
// 生成面会把**所有**来源判成不可用 ⇒ 运行在 `len(used) == 0` 那里失败，
// 而错误信息指向的是「生成结果没引用来源」——根因又一次被掩盖。
//
// 判据分两层：
//
//  1. **判定必须引用权威常量**：生成面不得拿字符串字面量判来源状态；
//  2. **词表的取值被盯着**：`evidence.SourceAvailable` 的取值是这条守护的一部分，
//     改它是一次要**显式承认**的改动（守护会转红），而不是一次静默漂移。
//
// 第 2 层是这个守护与「只匹配一个字符串」的区别：只匹配字符串的守护在词表改名后
// 仍然绿着，而这里在词表一改时就转红——它盯的是那个常量，不是那个字面量。

// sourceAvailableVocabularyValue 是「来源可用」这个词表的取值，被这条守护盯着。
//
// 它出现在这里不是为了**判定**（判定必须走 `evidence.SourceAvailable` 那个常量），
// 而是为了让词表的改动是一次显式承认的改动：改了权威常量而不同步这里，守护会转红。
const sourceAvailableVocabularyValue = "available"

// TestTheSourceStatusVocabularyValueIsWatched 断言词表的取值被盯着。
//
// 这一层是「变异验证」要转红的那一层：把 `evidence.SourceAvailable` 改成别的值，
// 这里就会报出「词表变了」，于是那次改动必须是一次显式承认的改动。
func TestTheSourceStatusVocabularyValueIsWatched(t *testing.T) {
	if string(evidence.SourceAvailable) != sourceAvailableVocabularyValue {
		t.Fatalf("evidence.SourceAvailable = %q，want %q；改了权威常量必须同时更新这条守护（词表改动是一次显式承认的改动）",
			string(evidence.SourceAvailable), sourceAvailableVocabularyValue)
	}
	if evidence.SourceAvailable == evidence.SourceStale || evidence.SourceAvailable == evidence.SourceUnavailable {
		t.Fatalf("来源状态词表的成员互相重叠：available=%q stale=%q unavailable=%q",
			evidence.SourceAvailable, evidence.SourceStale, evidence.SourceUnavailable)
	}
}

// TestTheSourceStatusJudgmentComesFromTheEvidenceVocabulary 断言生成面判来源状态
// 时引用权威常量，而不是自己抄一份取值。
//
// 判据是「拿字符串字面量比一个状态」——它比「字面量等于 available」更严：任何
// 字面量都算抄了一份（抄成别的值同样会漂移）。
func TestTheSourceStatusJudgmentComesFromTheEvidenceVocabulary(t *testing.T) {
	dir := generationPackageDir(t)
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatalf("读包目录失败：%v", err)
	}
	var violations []string
	referenced := false
	fset := token.NewFileSet()
	for _, entry := range entries {
		name := entry.Name()
		if entry.IsDir() || !strings.HasSuffix(name, ".go") || strings.HasSuffix(name, "_test.go") {
			continue
		}
		path := filepath.Join(dir, name)
		file, parseErr := parser.ParseFile(fset, path, nil, parser.SkipObjectResolution)
		if parseErr != nil {
			t.Fatalf("解析 %s 失败：%v", path, parseErr)
		}
		qualifier := evidencePackageQualifier(file)
		ast.Inspect(file, func(node ast.Node) bool {
			selector, ok := node.(*ast.SelectorExpr)
			if ok && selector.Sel.Name == "SourceAvailable" {
				if ident, isIdent := selector.X.(*ast.Ident); isIdent && (qualifier == "" || ident.Name == qualifier) {
					referenced = true
				}
			}
			binary, ok := node.(*ast.BinaryExpr)
			if !ok {
				return true
			}
			switch binary.Op {
			case token.EQL, token.NEQ:
			default:
				return true
			}
			literal, other := literalSide(binary)
			if literal == nil || other == nil {
				return true
			}
			status, ok := other.(*ast.SelectorExpr)
			if !ok || status.Sel.Name != "Status" {
				return true
			}
			violations = append(violations, fset.Position(literal.Pos()).String()+
				" 拿字符串字面量 "+strconv.Quote(literalValue(literal))+" 判来源状态；"+
				"来源可用这个词表由 evidence 包拥有，请引用 evidence.SourceAvailable")
			return true
		})
	}
	for _, violation := range violations {
		t.Error(violation)
	}
	// 守护盯的是那个常量：生成面没有引用它，这条守护就盯不到东西。
	if !referenced {
		t.Fatal("生成面没有引用 evidence.SourceAvailable，这条守护盯不到那个常量")
	}
}

// literalSide 说一个比较式里哪一边是字符串字面量、哪一边是别的。
func literalSide(binary *ast.BinaryExpr) (*ast.BasicLit, ast.Expr) {
	if literal, ok := binary.X.(*ast.BasicLit); ok && literal.Kind == token.STRING {
		if _, otherIsLiteral := binary.Y.(*ast.BasicLit); !otherIsLiteral {
			return literal, binary.Y
		}
	}
	if literal, ok := binary.Y.(*ast.BasicLit); ok && literal.Kind == token.STRING {
		if _, otherIsLiteral := binary.X.(*ast.BasicLit); !otherIsLiteral {
			return literal, binary.X
		}
	}
	return nil, nil
}

// literalValue 读出字符串字面量的取值。
func literalValue(literal *ast.BasicLit) string {
	value, err := strconv.Unquote(literal.Value)
	if err != nil {
		return literal.Value
	}
	return value
}

// evidencePackageQualifier 说这条文件用什么名字导入 evidence 包。
//
// 别名也算：别名仍然是「通过拥有者包引用词表」，不是抄了一份。
func evidencePackageQualifier(file *ast.File) string {
	for _, spec := range file.Imports {
		path, err := strconv.Unquote(spec.Path.Value)
		if err != nil || path != "github.com/Tencent/WeKnora/internal/evidence" {
			continue
		}
		if spec.Name != nil {
			return spec.Name.Name
		}
		return "evidence"
	}
	return ""
}
