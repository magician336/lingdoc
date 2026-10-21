package writingaction_test

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

// 这条守护守的是「执行方不自建动作语义」（开发计划 A33 / I-15 / I-16）。
//
// 执行方只做三件事：调 PlanFor 取执行计划、按 Plan.Context 组装上下文、把
// Plan.Instruction 交给模型。它不得按动作名分支、不得自己解释产出参数、
// 不得自己写动作指令。
//
// 三条判据：
//
//   1. 执行方里不出现任何动作取值的字符串字面量——动作身份只能通过
//      writingaction 的常量引用。
//   2. 写给模型的动作指令只有一个来源：带执行计划的函数里，指令必须是
//      RenderInstruction 的返回值；不带执行计划的函数里，指令只能来自客户端
//      的自由文本（旧路径）。
//   3. 产出参数的取值不在执行方里出现字面量——执行方不解释参数，只把它
//      交给 PlanFor。
//
// 判据都按**取值**判，不按类型名判（开发计划 D28）：仓里的
// rulesetstore.Action、templategovernance.Action、workspacecore.AuthorizationAction
// 都是同名不同义的治理动作词汇，按类型名判会全误报。

const (
	// executionRepoRoot 是仓库根目录相对本包（internal/lingdoc/writingaction）的路径。
	executionRepoRoot = "../../.."
	// executionHostPackage 是执行方所在的包目录。
	executionHostPackage = "internal/lingdoc/workspace"
	// promptTypeName 是给模型的提示结构体的名字。
	promptTypeName = "SelectedRewritePrompt"
	// requestTypeName 是改写请求结构体的名字。
	requestTypeName = "SelectedRewriteRequest"
)

// canonicalActionValuesForExecution 是八类动作的取值集合。
//
// 取值抄自词汇包本身——词汇包是这份知识的唯一拥有者，守护照着它判。
var canonicalActionValuesForExecution = map[string]bool{
	string(writingaction.ActionOutline):   true,
	string(writingaction.ActionGenerate):  true,
	string(writingaction.ActionContinue):  true,
	string(writingaction.ActionRewrite):   true,
	string(writingaction.ActionCompress):  true,
	string(writingaction.ActionExpand):    true,
	string(writingaction.ActionTransform): true,
	string(writingaction.ActionReview):    true,
}

// canonicalOutputParamValuesForExecution 是产出参数每个维度的闭合取值集合。
//
// 取值抄自开发计划 D17 的清单：长度、语气、目标语言、读者层次与转写子模式。
var canonicalOutputParamValuesForExecution = map[string]bool{
	string(writingaction.LengthShort):        true,
	string(writingaction.LengthMedium):       true,
	string(writingaction.LengthLong):         true,
	string(writingaction.ToneNeutral):        true,
	string(writingaction.ToneFormal):         true,
	string(writingaction.ToneAcademic):       true,
	string(writingaction.TonePlain):          true,
	string(writingaction.LangZH):             true,
	string(writingaction.LangEN):             true,
	string(writingaction.LangJA):             true,
	string(writingaction.LangDE):             true,
	string(writingaction.LangFR):             true,
	string(writingaction.AudiencePeer):       true,
	string(writingaction.AudienceReviewer):   true,
	string(writingaction.AudienceNovice):     true,
	string(writingaction.TransformTranslate): true,
	string(writingaction.TransformExplain):   true,
}

// TestExecutorDoesNotBuildItsOwnActionSemantics 断言执行方不自建动作语义。
func TestExecutorDoesNotBuildItsOwnActionSemantics(t *testing.T) {
	root := filepath.Clean(executionRepoRoot)
	host := filepath.Join(root, "internal", "lingdoc", "workspace")
	if _, err := os.Stat(host); err != nil {
		t.Fatalf("找不到执行方目录：%v", err)
	}

	fset := token.NewFileSet()
	var violations []string
	checked := 0

	entries, err := os.ReadDir(host)
	if err != nil {
		t.Fatalf("读取执行方目录失败：%v", err)
	}
	for _, entry := range entries {
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".go") || strings.HasSuffix(entry.Name(), "_test.go") {
			continue
		}
		path := filepath.Join(host, entry.Name())
		source, err := os.ReadFile(path)
		if err != nil {
			t.Fatalf("读取 %s 失败：%v", entry.Name(), err)
		}
		file, err := parser.ParseFile(fset, path, source, parser.SkipObjectResolution)
		if err != nil {
			t.Fatalf("解析 %s 失败：%v", entry.Name(), err)
		}
		rel := executionHostPackage + "/" + entry.Name()
		if !isHostAdapter(file) {
			continue
		}
		checked++

		// 判据一与判据三：取值字面量。
		ast.Inspect(file, func(node ast.Node) bool {
			literal, ok := node.(*ast.BasicLit)
			if !ok || literal.Kind != token.STRING {
				return true
			}
			text, err := strconv.Unquote(literal.Value)
			if err != nil {
				return true
			}
			if canonicalActionValuesForExecution[text] {
				violations = append(violations, rel+":"+line(fset, literal.Pos())+
					" 写了动作取值 "+text+" 的字面量（动作身份只能引用 writingaction 的常量）")
			}
			if canonicalOutputParamValuesForExecution[text] {
				violations = append(violations, rel+":"+line(fset, literal.Pos())+
					" 写了产出参数取值 "+text+" 的字面量（执行方不解释产出参数，只把它交给 PlanFor）")
			}
			return true
		})

		// 判据二：写给模型的指令只有一个来源。
		violations = append(violations, instructionSourceViolations(fset, rel, file)...)
	}
	if checked == 0 {
		t.Fatal("没有扫描到任何执行方文件，断言可能已经失效")
	}
	for _, violation := range violations {
		t.Error(violation)
	}
}

// isHostAdapter 判断一个文件是不是执行方。
//
// 执行方是那两件事：给模型组装提示，或引用动作词汇包。其余文件与动作语义
// 无关，不该被这条守护管。
//
// 范围必须收在执行方里，不能扫整个领域：internal/lingdoc/workspacecore/g6_trial.go
// 里的试用窗口决定是 expand / continue / pause，与写作动作碰词但不同义——
// 按取值扫全领域会把它误判成第二处动作枚举。
func isHostAdapter(file *ast.File) bool {
	for _, decl := range file.Decls {
		generic, ok := decl.(*ast.GenDecl)
		if !ok || generic.Tok != token.TYPE {
			continue
		}
		for _, spec := range generic.Specs {
			typeSpec, ok := spec.(*ast.TypeSpec)
			if !ok {
				continue
			}
			ident, ok := typeSpec.Type.(*ast.Ident)
			if ok && ident.Name == promptTypeName {
				return true
			}
		}
	}
	for _, spec := range file.Imports {
		path, err := strconv.Unquote(spec.Path.Value)
		if err != nil {
			continue
		}
		if strings.HasSuffix(path, "internal/lingdoc/"+ownerPackage) {
			return true
		}
	}
	return false
}

// instructionSourceViolations 报告一个文件里写给模型的指令来源不合规的地方。
func instructionSourceViolations(fset *token.FileSet, rel string, file *ast.File) []string {
	var violations []string
	alias := ownerImportAlias(file)

	for _, decl := range file.Decls {
		function, ok := decl.(*ast.FuncDecl)
		if !ok || function.Body == nil {
			continue
		}
		planParams, requestParams, renderResults := functionBindings(function, alias)
		ast.Inspect(function.Body, func(node ast.Node) bool {
			literal, ok := node.(*ast.CompositeLit)
			if !ok {
				return true
			}
			ident, ok := literal.Type.(*ast.Ident)
			if !ok || ident.Name != promptTypeName {
				return true
			}
			if len(literal.Elts) == 0 {
				// 零值不是「组装了一个提示」：它是出错时交回去的空提示。
				return true
			}
			value, found := keyedValue(literal, "Instruction")
			if !found {
				violations = append(violations, rel+":"+line(fset, literal.Pos())+
					" 组装的提示没有指令字段（写给模型的指令必须有且只有一个来源）")
				return true
			}
			if len(planParams) > 0 {
				// 带执行计划的函数：指令只能来自执行计划。
				name, ok := value.(*ast.Ident)
				if !ok || !renderResults[name.Name] {
					violations = append(violations, rel+":"+line(fset, value.Pos())+
						" 的指令不是执行计划渲染出来的（带执行计划的函数里，指令只能是 RenderInstruction 的返回值）")
				}
				return true
			}
			// 不带执行计划的函数：指令只能来自客户端的自由文本（旧路径）。
			selector, ok := value.(*ast.SelectorExpr)
			if !ok || selector.Sel.Name != "Instruction" {
				violations = append(violations, rel+":"+line(fset, value.Pos())+
					" 的指令既不来自执行计划也不来自客户端的说明")
				return true
			}
			base, ok := selector.X.(*ast.Ident)
			if !ok || !requestParams[base.Name] {
				violations = append(violations, rel+":"+line(fset, value.Pos())+
					" 的指令不是客户端请求里的说明（旧路径的指令只能来自客户端的自由文本）")
			}
			return true
		})
	}
	return violations
}

// functionBindings 取一个函数里的三类名字：带执行计划的参数、带改写请求的
// 参数，以及绑定到 RenderInstruction 返回值的局部变量。
func functionBindings(function *ast.FuncDecl, alias string) (planParams, requestParams, renderResults map[string]bool) {
	planParams = map[string]bool{}
	requestParams = map[string]bool{}
	renderResults = map[string]bool{}

	if function.Type.Params != nil {
		for _, field := range function.Type.Params.List {
			isPlan := typeIsPlan(field.Type, alias)
			isRequest := typeIsIdent(field.Type, requestTypeName)
			for _, name := range field.Names {
				if isPlan {
					planParams[name.Name] = true
				}
				if isRequest {
					requestParams[name.Name] = true
				}
			}
		}
	}
	ast.Inspect(function.Body, func(node ast.Node) bool {
		assign, ok := node.(*ast.AssignStmt)
		if !ok || assign.Tok != token.DEFINE || len(assign.Rhs) != 1 {
			return true
		}
		call, ok := assign.Rhs[0].(*ast.CallExpr)
		if !ok || !callIsRenderInstruction(call, alias) {
			return true
		}
		for _, left := range assign.Lhs {
			if ident, ok := left.(*ast.Ident); ok {
				renderResults[ident.Name] = true
			}
		}
		return true
	})
	return planParams, requestParams, renderResults
}

// typeIsPlan 判断一个类型是不是词汇包的执行计划。
func typeIsPlan(expr ast.Expr, alias string) bool {
	switch typed := expr.(type) {
	case *ast.StarExpr:
		return typeIsPlan(typed.X, alias)
	case *ast.SelectorExpr:
		ident, ok := typed.X.(*ast.Ident)
		if !ok || alias == "" || ident.Name != alias {
			return false
		}
		return typed.Sel.Name == "Plan"
	}
	return false
}

// typeIsIdent 判断一个类型是不是某个本地结构体。
func typeIsIdent(expr ast.Expr, name string) bool {
	ident, ok := expr.(*ast.Ident)
	return ok && ident.Name == name
}

// callIsRenderInstruction 判断一个调用是不是词汇包的 RenderInstruction。
func callIsRenderInstruction(call *ast.CallExpr, alias string) bool {
	selector, ok := call.Fun.(*ast.SelectorExpr)
	if !ok {
		return false
	}
	ident, ok := selector.X.(*ast.Ident)
	if !ok || alias == "" || ident.Name != alias {
		return false
	}
	return selector.Sel.Name == "RenderInstruction"
}

// keyedValue 取一个复合字面量里某个键的值。
func keyedValue(literal *ast.CompositeLit, key string) (ast.Expr, bool) {
	for _, element := range literal.Elts {
		keyed, ok := element.(*ast.KeyValueExpr)
		if !ok {
			continue
		}
		ident, ok := keyed.Key.(*ast.Ident)
		if !ok || ident.Name != key {
			continue
		}
		return keyed.Value, true
	}
	return nil, false
}
