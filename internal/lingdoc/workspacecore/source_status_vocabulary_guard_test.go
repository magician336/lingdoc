package workspacecore

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

// 这组断言守的是「来源可用」这个词表**只有一个拥有者**，与
// internal/lingdoc/generation/source_status_vocabulary_guard_test.go 同源
// （#597 把同一套守护从生成面推广到核心面）。
//
// 核心面的 ResearchSource.Status 持有的是 evidence.SourceStatus 的字符串化取值
// （装配层的 readResearchSource 用 `Status: string(source.Status)` 写入），判定处
// 若自己抄一份 `"available"`，词表一改这里就把全部来源判成不可用，且错误信息
// 指向「没有引用来源」而不是「词表变了」——根因被掩盖。
//
// 判据分两层：
//
//  1. **判定必须引用权威常量**：不得拿字符串字面量 `"available"` 判来源状态；
//  2. **词表的取值被盯着**：改 `evidence.SourceAvailable` 的取值是一次必须显式
//     承认的改动（守护会转红），而不是一次静默漂移。
//
// 与生成面守护的差异：生成面包内只有来源状态一种 `.Status`，故那里禁止**任何**
// 字面量判状态；核心面还有别的状态词表（CitationStatus 的展示三态、agent run
// 状态、ProcessingState），全字面量判据会误伤它们。这里的判据收窄到
// `"available"` 字面量——「来源可用」判定的漂移特征。CitationStatus 的展示词表
// 由本包 source_policy.go 的常量拥有（它不是 evidence.SourceStatus 的第二份：
// needs_review 不在 evidence 词表里），引用常量后天然不命中此判据。

// sourceAvailableVocabularyValue 是「来源可用」这个词表的取值，被这条守护盯着。
// 它出现在这里不是为了判定（判定必须走 evidence.SourceAvailable），而是为了让
// 词表的改动是一次显式承认的改动。
const sourceAvailableVocabularyValue = "available"

// TestTheSourceStatusVocabularyValueIsWatched 断言词表的取值被盯着。
// 这是变异验证要转红的那一层：把 evidence.SourceAvailable 改成别的值，
// 生成面、工作区面、核心面的守护会同时报出「词表变了」。
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

// TestTheSourceStatusJudgmentComesFromTheEvidenceVocabulary 断言本包判来源状态时
// 引用权威常量，而不是自己抄一份取值。
func TestTheSourceStatusJudgmentComesFromTheEvidenceVocabulary(t *testing.T) {
	dir := workspacecorePackageDir(t)
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
			if literalValue(literal) != sourceAvailableVocabularyValue {
				return true
			}
			if _, ok := statusOperand(other); !ok {
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
	// 守护盯的是那个常量：本包没有引用它，这条守护就盯不到东西。
	if !referenced {
		t.Fatal("本包没有引用 evidence.SourceAvailable，这条守护盯不到那个常量")
	}
}

// statusOperand 说比较式另一边判的是不是某个 .Status 字段。
//
// 直接的 `x.Status` 与转换包裹的 `string(x.Status)` 都算：包一层 string() 不会
// 让「拿字面量判来源状态」变成别的判定（#597 修掉的第一处就是这种形状）。
func statusOperand(expr ast.Expr) (*ast.SelectorExpr, bool) {
	if selector, ok := expr.(*ast.SelectorExpr); ok {
		return selector, selector.Sel.Name == "Status"
	}
	if call, ok := expr.(*ast.CallExpr); ok && len(call.Args) == 1 {
		return statusOperand(call.Args[0])
	}
	return nil, false
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

// workspacecorePackageDir 定位本包的源码目录（测试工作目录随 go test 的形式变化）。
func workspacecorePackageDir(t *testing.T) string {
	t.Helper()
	wd, err := os.Getwd()
	if err != nil {
		t.Fatalf("取工作目录失败：%v", err)
	}
	return wd
}
