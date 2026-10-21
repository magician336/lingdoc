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
// 四条判据：
//
//  1. 执行方里不出现任何动作取值的字符串字面量——动作身份只能通过
//     writingaction 的常量引用。
//  2. 执行方里不引用任何**具体**动作常量（writingaction.ActionRewrite 之类）——
//     只允许用 writingaction.Action 这个**类型**与 writingaction 的管道常量。
//     ⇒ 执行方在语法上就没法按动作分支。
//  3. 产出参数的取值不在执行方里出现字面量——执行方不解释参数，只把它
//     交给 PlanFor。
//  4. 写给模型的动作指令只有一个来源：带执行计划的函数里，指令必须是
//     RenderInstruction 的返回值；不带执行计划的函数里，指令只能来自客户端
//     的自由文本（旧路径）。
//
// 判据都按**取值与引用**判，不按类型名判（开发计划 D28）：仓里的
// rulesetstore.Action、templategovernance.Action、workspacecore.AuthorizationAction
// 都是同名不同义的治理动作词汇，按类型名判会全误报。
//
// 扫描范围必须收在执行方目录内，不能扫整个领域：
// internal/lingdoc/workspacecore/g6_trial.go 里的试用窗口决定是 expand /
// continue / pause，与写作动作碰词但不同义——按取值扫全领域会把它误判成
// 第二处动作枚举。

const (
	// 指令的两种装配形状。
	instructionByLiteral = "literal"
	// instructionByAssignment 表示给模型的输入是**填**出来的：它的指令字段
	// 被逐次赋值。
	instructionByAssignment = "assignment"
)

// executionHost 描述一个执行方：它把动作语义变成写给模型的输入。
type executionHost struct {
	// packagePath 是执行方所在的包目录（相对仓库根）。
	packagePath string
	// promptType 是给模型的输入结构体的名字。
	promptType string
	// requestType 是这个执行方的请求结构体的名字。
	requestType string
	// instructionShape 说明这个执行方怎么把指令放进给模型的输入。
	instructionShape string
}

// executionHosts 是两条执行管道。
//
// 两条管道的形状不同（脚手架差异），语义规则相同：同一个词汇包、同一条
// PlanFor、同一个 RenderInstruction。
var executionHosts = []executionHost{
	{
		packagePath:      "internal/lingdoc/workspace",
		promptType:       "SelectedRewritePrompt",
		requestType:      "SelectedRewriteRequest",
		instructionShape: instructionByLiteral,
	},
	{
		packagePath:      "internal/lingdoc/generation",
		promptType:       "Input",
		requestType:      "Request",
		instructionShape: instructionByAssignment,
	},
}

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

// canonicalActionConstantNames 是八个具体动作常量的名字。
//
// 判据 2 找的是「引用了某个具体动作」：写 writingaction.ActionRewrite 就是按
// 动作分支的起点。只允许用 writingaction.Action 这个**类型**。
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
	root := filepath.Clean(repoRoot)
	for _, host := range executionHosts {
		t.Run(host.packagePath, func(t *testing.T) {
			host := host
			directory := filepath.Join(root, filepath.FromSlash(host.packagePath))
			if _, err := os.Stat(directory); err != nil {
				t.Fatalf("找不到执行方目录：%v", err)
			}

			fset := token.NewFileSet()
			var violations []string
			checked := 0

			entries, err := os.ReadDir(directory)
			if err != nil {
				t.Fatalf("读取执行方目录失败：%v", err)
			}
			for _, entry := range entries {
				if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".go") || strings.HasSuffix(entry.Name(), "_test.go") {
					continue
				}
				path := filepath.Join(directory, entry.Name())
				source, err := os.ReadFile(path)
				if err != nil {
					t.Fatalf("读取 %s 失败：%v", entry.Name(), err)
				}
				file, err := parser.ParseFile(fset, path, source, parser.SkipObjectResolution)
				if err != nil {
					t.Fatalf("解析 %s 失败：%v", entry.Name(), err)
				}
				rel := host.packagePath + "/" + entry.Name()
				if !isHostAdapter(file, host) {
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

				// 判据二：具体动作常量的引用。
				alias := ownerImportAlias(file)
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
							violations = append(violations, rel+":"+line(fset, selector.Pos())+
								" 引用了具体动作常量 writingaction."+selector.Sel.Name+
								"（执行方只按管道引用动作，不得按动作名分支）")
						}
						return true
					})
				}

				// 判据四：写给模型的指令只有一个来源。
				violations = append(violations, instructionSourceViolations(fset, rel, file, host)...)
			}
			if checked == 0 {
				t.Fatal("没有扫描到任何执行方文件，断言可能已经失效")
			}
			for _, violation := range violations {
				t.Error(violation)
			}
		})
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
func isHostAdapter(file *ast.File, host executionHost) bool {
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
			if ok && ident.Name == host.promptType {
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
func instructionSourceViolations(fset *token.FileSet, rel string, file *ast.File, host executionHost) []string {
	var violations []string
	alias := ownerImportAlias(file)

	for _, decl := range file.Decls {
		function, ok := decl.(*ast.FuncDecl)
		if !ok || function.Body == nil {
			continue
		}
		bindings := functionBindings(function, alias, host)
		switch host.instructionShape {
		case instructionByLiteral:
			violations = append(violations, literalInstructionViolations(fset, rel, function, host, bindings)...)
		case instructionByAssignment:
			violations = append(violations, assignedInstructionViolations(fset, rel, function, host, bindings)...)
		}
	}
	return violations
}

// literalInstructionViolations 检查「组装出来的提示」里的指令来源。
func literalInstructionViolations(fset *token.FileSet, rel string, function *ast.FuncDecl,
	host executionHost, bindings functionBinding) []string {
	var violations []string
	ast.Inspect(function.Body, func(node ast.Node) bool {
		literal, ok := node.(*ast.CompositeLit)
		if !ok {
			return true
		}
		ident, ok := literal.Type.(*ast.Ident)
		if !ok || ident.Name != host.promptType {
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
		violations = append(violations, instructionSourceViolation(fset, rel, value, bindings)...)
		return true
	})
	return violations
}

// assignedInstructionViolations 检查「填进提示的指令」的来源。
//
// 它找的是写给模型的输入结构体上的指令赋值：这个结构体是执行方拼出来的，
// 它的指令字段只能来自执行计划或客户端的自由文本。
func assignedInstructionViolations(fset *token.FileSet, rel string, function *ast.FuncDecl,
	host executionHost, bindings functionBinding) []string {
	var violations []string
	ast.Inspect(function.Body, func(node ast.Node) bool {
		assign, ok := node.(*ast.AssignStmt)
		if !ok || assign.Tok != token.ASSIGN || len(assign.Lhs) != 1 || len(assign.Rhs) != 1 {
			return true
		}
		selector, ok := assign.Lhs[0].(*ast.SelectorExpr)
		if !ok || selector.Sel.Name != "Instruction" {
			return true
		}
		ident, ok := selector.X.(*ast.Ident)
		if !ok || !bindings.promptParams[ident.Name] {
			return true
		}
		violations = append(violations, instructionSourceViolation(fset, rel, assign.Rhs[0], bindings)...)
		return true
	})
	return violations
}

// instructionSourceViolation 判定一次指令赋值（或一次指令字段）的来源是否合规。
//
// 带执行计划的函数：指令只能来自执行计划。不带执行计划的函数：指令只能来自
// 客户端的自由文本（旧路径）。
func instructionSourceViolation(fset *token.FileSet, rel string, value ast.Expr, bindings functionBinding) []string {
	if len(bindings.planParams) > 0 {
		// 带执行计划的函数：指令只能来自执行计划。
		name, ok := value.(*ast.Ident)
		if !ok || !bindings.renderResults[name.Name] {
			return []string{rel + ":" + line(fset, value.Pos()) +
				" 的指令不是执行计划渲染出来的（带执行计划的函数里，指令只能是 RenderInstruction 的返回值）"}
		}
		return nil
	}
	// 不带执行计划的函数：指令只能来自客户端的自由文本（旧路径）。
	selector, ok := value.(*ast.SelectorExpr)
	if !ok || selector.Sel.Name != "Instruction" {
		return []string{rel + ":" + line(fset, value.Pos()) +
			" 的指令既不来自执行计划也不来自客户端的说明"}
	}
	base, ok := selector.X.(*ast.Ident)
	if !ok || !bindings.requestParams[base.Name] {
		return []string{rel + ":" + line(fset, value.Pos()) +
			" 的指令不是客户端请求里的说明（旧路径的指令只能来自客户端的自由文本）"}
	}
	return nil
}

// functionBinding 是一个函数里的三类名字。
type functionBinding struct {
	// planParams 是带执行计划的参数。
	planParams map[string]bool
	// requestParams 是带这个执行方的请求结构的参数。
	requestParams map[string]bool
	// promptParams 是带给模型的输入结构的参数。
	promptParams map[string]bool
	// renderResults 是绑定到 RenderInstruction 返回值的局部变量。
	renderResults map[string]bool
}

// functionBindings 取一个函数里的三类名字：带执行计划的参数、带请求结构的
// 参数、带给模型的输入结构的参数，以及绑定到 RenderInstruction 返回值的局部
// 变量。
//
// 绑定到 RenderInstruction 的变量必须**只被赋值一次**：被改写过的变量不再是
// 「执行计划渲染出来的那个值」，它是一个执行方自己写出来的指令。
func functionBindings(function *ast.FuncDecl, alias string, host executionHost) functionBinding {
	binding := functionBinding{
		planParams:    map[string]bool{},
		requestParams: map[string]bool{},
		promptParams:  map[string]bool{},
		renderResults: map[string]bool{},
	}

	if function.Type.Params != nil {
		for _, field := range function.Type.Params.List {
			isPlan := typeIsPlan(field.Type, alias)
			isRequest := typeIsIdent(field.Type, host.requestType)
			isPrompt := typeIsIdent(field.Type, host.promptType) || typeIsPointerToIdent(field.Type, host.promptType)
			for _, name := range field.Names {
				if isPlan {
					binding.planParams[name.Name] = true
				}
				if isRequest {
					binding.requestParams[name.Name] = true
				}
				if isPrompt {
					binding.promptParams[name.Name] = true
				}
			}
		}
	}
	assignments := map[string]*assignmentRecord{}
	ast.Inspect(function.Body, func(node ast.Node) bool {
		assign, ok := node.(*ast.AssignStmt)
		if !ok || len(assign.Rhs) == 0 || len(assign.Lhs) == 0 {
			return true
		}
		fromRender := false
		if len(assign.Rhs) == 1 {
			if call, ok := assign.Rhs[0].(*ast.CallExpr); ok && callIsRenderInstruction(call, alias) {
				fromRender = true
			}
		}
		for index, left := range assign.Lhs {
			ident, ok := left.(*ast.Ident)
			if !ok {
				continue
			}
			record, exists := assignments[ident.Name]
			if !exists {
				record = &assignmentRecord{}
				assignments[ident.Name] = record
			}
			record.count++
			// 多返回值的赋值里，只有第一个左值拿到右边那个值。
			if index == 0 && fromRender {
				record.fromRender = true
			}
		}
		return true
	})
	for name, record := range assignments {
		if record.count == 1 && record.fromRender {
			binding.renderResults[name] = true
		}
	}
	return binding
}

// assignmentRecord 是一个局部变量的赋值记录。
type assignmentRecord struct {
	// count 是它被赋值的次数。
	count int
	// fromRender 说它有没有被赋成 RenderInstruction 的返回值。
	fromRender bool
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

// typeIsPointerToIdent 判断一个类型是不是某个本地结构体的指针。
func typeIsPointerToIdent(expr ast.Expr, name string) bool {
	pointer, ok := expr.(*ast.StarExpr)
	if !ok {
		return false
	}
	return typeIsIdent(pointer.X, name)
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
