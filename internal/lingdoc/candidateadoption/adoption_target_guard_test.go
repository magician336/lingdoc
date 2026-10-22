package candidateadoption_test

import (
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"github.com/Tencent/WeKnora/internal/lingdoc/writingaction"
)

// 这条守护守的是「采纳端不按动作名写分支」（开发计划 A29 / I-10）。
//
// 「可否采纳」是两维数据：采纳目标 × 该动作允许哪些目标。许可位由动作自己
// 声明（writingaction 的包内规格表），采纳端只查这张表。采纳端一旦按动作名
// 写分支，许可位就多了一份副本：改了许可位，采纳端的分支不会跟着变，而且
// 不会有任何编译错误。
//
// 三条判据：
//
//  1. 采纳端的非测试文件里不出现任何动作取值的字符串字面量——动作身份只能
//     通过词汇包的常量引用。
//  2. 采纳端的非测试文件里不引用任何**具体**动作常量（writingaction.ActionRewrite
//     之类）——只允许用 writingaction.Action 这个类型。⇒ 采纳端在语法上就没法
//     按动作分支。
//  3. 采纳端必须**查许可位**：它调用 writingaction.TargetsFor 并做成员判断，
//     而不是自己写一张许可表。
//
// 判据按**取值与引用**判，不按类型名判（开发计划 D28）：仓里的
// rulesetstore.Action、templategovernance.Action、workspacecore.AuthorizationAction
// 都是同名不同义的治理动作词汇。
//
// 扫描范围必须收在采纳端目录内：internal/lingdoc/workspacecore/g6_trial.go 里的
// 试用窗口决定是 expand / continue / pause，与写作动作碰词但不同义——按取值
// 扫全领域会把它误判成第二处动作枚举。

const (
	// adoptionGuardRepoRoot 是仓库根目录相对本包（internal/lingdoc/candidateadoption）的路径。
	adoptionGuardRepoRoot = "../../.."
	// adoptionGuardPackage 是采纳端的包目录。
	adoptionGuardPackage = "internal/lingdoc/candidateadoption"
	// writingActionPackage 是动作词汇包的目录名。
	writingActionPackage = "writingaction"
	// targetsForFunc 是词汇包派生许可位的入口名。
	targetsForFunc = "TargetsFor"
)

// canonicalActionValuesForAdoption 是八类动作的取值集合。
//
// 取值抄自词汇包本身——词汇包是这份知识的唯一拥有者，守护照着它判。
var canonicalActionValuesForAdoption = map[string]bool{
	string(writingaction.ActionOutline):   true,
	string(writingaction.ActionGenerate):  true,
	string(writingaction.ActionContinue):  true,
	string(writingaction.ActionRewrite):   true,
	string(writingaction.ActionCompress):  true,
	string(writingaction.ActionExpand):    true,
	string(writingaction.ActionTransform): true,
	string(writingaction.ActionReview):    true,
}

// canonicalActionConstantNames 是八个具体动作常量的名字。
//
// 判据 2 找的是「引用了某个具体动作」：写 action.ActionRewrite 就是按动作
// 分支的起点。只允许用 action.Action 这个**类型**。
var canonicalActionConstantNames = map[string]bool{
	"ActionOutline":   true,
	"ActionGenerate":  true,
	"ActionContinue":  true,
	"ActionRewrite":   true,
	"ActionCompress":  true,
	"ActionExpand":    true,
	"ActionTransform": true,
	"ActionReview":    true,
}

// TestAdoptionSideDoesNotBranchOnActionNames 断言采纳端不按动作名写分支。
func TestAdoptionSideDoesNotBranchOnActionNames(t *testing.T) {
	root := filepath.Clean(adoptionGuardRepoRoot)
	adoption := filepath.Join(root, "internal", "lingdoc", "candidateadoption")
	if _, err := os.Stat(adoption); err != nil {
		t.Fatalf("找不到采纳端目录：%v", err)
	}

	fset := token.NewFileSet()
	var violations []string
	checked := 0

	entries, err := os.ReadDir(adoption)
	if err != nil {
		t.Fatalf("读取采纳端目录失败：%v", err)
	}
	for _, entry := range entries {
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".go") || strings.HasSuffix(entry.Name(), "_test.go") {
			continue
		}
		path := filepath.Join(adoption, entry.Name())
		source, err := os.ReadFile(path)
		if err != nil {
			t.Fatalf("读取 %s 失败：%v", entry.Name(), err)
		}
		file, err := parser.ParseFile(fset, path, source, parser.SkipObjectResolution)
		if err != nil {
			t.Fatalf("解析 %s 失败：%v", entry.Name(), err)
		}
		rel := adoptionGuardPackage + "/" + entry.Name()
		checked++

		// 判据一：动作取值的字面量。
		ast.Inspect(file, func(node ast.Node) bool {
			literal, ok := node.(*ast.BasicLit)
			if !ok || literal.Kind != token.STRING {
				return true
			}
			text, err := strconv.Unquote(literal.Value)
			if err != nil {
				return true
			}
			if canonicalActionValuesForAdoption[text] {
				violations = append(violations, rel+":"+guardLine(fset, literal.Pos())+
					" 写了动作取值 "+text+" 的字面量（动作身份只能引用 writingaction 的常量）")
			}
			return true
		})

		// 判据二：具体动作常量的引用。
		alias := writingActionAlias(file)
		if alias != "" {
			ast.Inspect(file, func(node ast.Node) bool {
				selector, ok := node.(*ast.SelectorExpr)
				if !ok {
					return true
				}
				ident, ok := selector.X.(*ast.Ident)
				if !ok || ident.Name != alias {
					return true
				}
				if canonicalActionConstantNames[selector.Sel.Name] {
					violations = append(violations, rel+":"+guardLine(fset, selector.Pos())+
						" 引用了具体动作常量 writingaction."+selector.Sel.Name+
						"（采纳端只查动作声明的许可位，不得按动作名分支）")
				}
				return true
			})
		}
	}
	if checked == 0 {
		t.Fatal("没有扫描到任何采纳端文件，断言可能已经失效")
	}
	for _, violation := range violations {
		t.Error(violation)
	}
}

// TestAdoptionSideLooksUpThePermissionBit 断言采纳端查的是动作声明的许可位。
//
// 判据：采纳端必须调用 writingaction.TargetsFor。它是词汇包派生许可位的唯一
// 入口；自己写一张许可表就是第二份数据源，许可位一改就会漂。
func TestAdoptionSideLooksUpThePermissionBit(t *testing.T) {
	root := filepath.Clean(adoptionGuardRepoRoot)
	adoption := filepath.Join(root, "internal", "lingdoc", "candidateadoption")
	fset := token.NewFileSet()
	found := false
	checked := 0

	entries, err := os.ReadDir(adoption)
	if err != nil {
		t.Fatalf("读取采纳端目录失败：%v", err)
	}
	for _, entry := range entries {
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".go") || strings.HasSuffix(entry.Name(), "_test.go") {
			continue
		}
		path := filepath.Join(adoption, entry.Name())
		source, err := os.ReadFile(path)
		if err != nil {
			t.Fatalf("读取 %s 失败：%v", entry.Name(), err)
		}
		file, err := parser.ParseFile(fset, path, source, parser.SkipObjectResolution)
		if err != nil {
			t.Fatalf("解析 %s 失败：%v", entry.Name(), err)
		}
		alias := writingActionAlias(file)
		if alias == "" {
			continue
		}
		checked++
		ast.Inspect(file, func(node ast.Node) bool {
			call, ok := node.(*ast.CallExpr)
			if !ok {
				return true
			}
			selector, ok := call.Fun.(*ast.SelectorExpr)
			if !ok {
				return true
			}
			ident, ok := selector.X.(*ast.Ident)
			if !ok || ident.Name != alias {
				return true
			}
			if selector.Sel.Name == targetsForFunc {
				found = true
			}
			return true
		})
	}
	if checked == 0 {
		t.Fatal("没有扫描到任何引用动作词汇包的采纳端文件，断言可能已经失效")
	}
	if !found {
		t.Errorf("采纳端没有调用 writingaction.%s：许可位必须向动作词汇包查，不得自建一张表", targetsForFunc)
	}
}

// writingActionAlias 取一个文件里动作词汇包的导入别名。
func writingActionAlias(file *ast.File) string {
	for _, spec := range file.Imports {
		path, err := strconv.Unquote(spec.Path.Value)
		if err != nil {
			continue
		}
		if !strings.HasSuffix(path, "internal/lingdoc/"+writingActionPackage) {
			continue
		}
		if spec.Name != nil {
			return spec.Name.Name
		}
		return writingActionPackage
	}
	return ""
}

// guardLine 返回一个位置所在的行号。
//
// 它不叫 line：同包的另一个守护文件已经有一个 line。
func guardLine(fset *token.FileSet, pos token.Pos) string {
	return strconv.Itoa(fset.Position(pos).Line)
}
