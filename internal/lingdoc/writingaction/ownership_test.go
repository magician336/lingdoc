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

// 两条守护。
//
// 第一条：写作动作的词汇只有一个拥有者。动作语义一旦在第二个包里再写一遍，
// 两条管道就会各自解释「压缩」是什么意思，改动也不会编译报错。
//
// 第二条：客户端提交的产出参数不得进入服务端的 AI 安全边界。产出参数是
// 「要多长、什么语气」，输出约束是「不许做什么」；混在一起等于让客户端改
// 安全边界。
//
// 两条守护都走 go/ast 扫源码，不靠字符串匹配：注释与文档里提到这些名字是
// 允许的，只有真正声明或写入才算违规。
const (
	// repoRoot 是仓库根目录相对本包（internal/lingdoc/writingaction）的路径。
	repoRoot = "../../.."
	// ownerPackage 是动作词汇的拥有者包名；它自己的声明不算复制。
	ownerPackage = "writingaction"
)

// canonicalActionConstants 是八类动作的常量名与取值。
//
// 名字抄自开发计划 §3.2 的接口契约——它是独立的事实来源，不是从实现里读的。
var canonicalActionConstants = map[string]writingaction.Action{
	"ActionOutline":   writingaction.ActionOutline,
	"ActionGenerate":  writingaction.ActionGenerate,
	"ActionContinue":  writingaction.ActionContinue,
	"ActionRewrite":   writingaction.ActionRewrite,
	"ActionCompress":  writingaction.ActionCompress,
	"ActionExpand":    writingaction.ActionExpand,
	"ActionTransform": writingaction.ActionTransform,
	"ActionReview":    writingaction.ActionReview,
}

// canonicalActionValues 是八类动作的取值集合。
var canonicalActionValues = func() map[string]bool {
	values := map[string]bool{}
	for _, action := range canonicalActionConstants {
		values[string(action)] = true
	}
	return values
}()

// canonicalActionShapes 是「看起来在声明写作动作」的标识符形状。
//
// 判据是标识符的**形状**而不是它含哪些词：`AuthorizationReview` 也是
// AuthorizationAction 的一份取值，它与写作动作无关，只是碰巧同词。
func canonicalActionShapes() map[string]bool {
	shapes := map[string]bool{}
	for name := range canonicalActionConstants {
		title := strings.TrimPrefix(name, "Action")
		shapes[name] = true
		shapes["Action"+title] = true
		shapes[title+"Action"] = true
		shapes["Writing"+title] = true
	}
	return shapes
}

// outputParamIdentifiers 是产出参数的标识符。
//
// 守护二找的是「客户端提交的产出参数被写进了安全边界」，所以判据是产出
// 参数的标识符出现在 OutputConstraints 的取值里。
var outputParamIdentifiers = map[string]bool{
	"OutputParams":    true,
	"TransformParams": true,
	"TransformMode":   true,
	"Length":          true,
	"Tone":            true,
	"Ratio":           true,
	"TargetLanguage":  true,
	"Audience":        true,
}

// TestWritingActionVocabularyHasOneOwner 断言仓内不存在第二处写作动作词汇。
func TestWritingActionVocabularyHasOneOwner(t *testing.T) {
	root := filepath.Clean(repoRoot)
	domain := filepath.Join(root, "internal", "lingdoc")
	if _, err := os.Stat(domain); err != nil {
		t.Fatalf("找不到 LingDoc 领域目录：%v", err)
	}

	fset := token.NewFileSet()
	shapes := canonicalActionShapes()
	var copies []string
	checked := 0

	err := filepath.Walk(domain, func(path string, entry os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		if entry.IsDir() {
			// 拥有者自己不是复制。
			if entry.Name() == ownerPackage {
				return filepath.SkipDir
			}
			return nil
		}
		if !strings.HasSuffix(path, ".go") || strings.HasSuffix(path, "_test.go") {
			return nil
		}
		source, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		rel, _ := filepath.Rel(root, path)
		rel = filepath.ToSlash(rel)
		checked++

		file, err := parser.ParseFile(fset, path, source, parser.SkipObjectResolution)
		if err != nil {
			return err
		}
		ast.Inspect(file, func(node ast.Node) bool {
			switch typed := node.(type) {
			case *ast.ValueSpec:
				for _, value := range typed.Values {
					literal, ok := value.(*ast.BasicLit)
					if !ok || literal.Kind != token.STRING {
						continue
					}
					text, err := strconv.Unquote(literal.Value)
					if err != nil {
						continue
					}
					if !canonicalActionValues[text] {
						continue
					}
					if shapes[typed.Names[0].Name] {
						copies = append(copies, rel+":"+line(fset, typed.Pos())+
							" 声明了第二处动作枚举常量 "+typed.Names[0].Name)
					}
					if declaresActionType(typed) {
						copies = append(copies, rel+":"+line(fset, typed.Pos())+
							" 用第二个动作类型承载写作动作取值（动作类型归 writingaction 独占）")
					}
				}
			case *ast.GenDecl:
				if typed.Tok != token.CONST {
					return true
				}
				if countCanonicalActions(constantStrings(typed)) >= 3 {
					copies = append(copies, rel+":"+line(fset, typed.Pos())+
						" 有一组动作取值常量（动作枚举归 writingaction 独占）")
				}
			case *ast.CompositeLit:
				if countCanonicalActions(literalStrings(typed.Elts)) >= 3 {
					copies = append(copies, rel+":"+line(fset, typed.Pos())+
						" 有一张动作字符串字面量表（动作词汇归 writingaction 独占）")
				}
			}
			return true
		})
		return nil
	})
	if err != nil {
		t.Fatalf("扫描 LingDoc 领域失败：%v", err)
	}
	if checked == 0 {
		t.Fatal("没有扫描到任何文件，断言可能已经失效")
	}
	for _, copy := range copies {
		t.Error(copy)
	}
}

// TestOutputParamsStayOutOfTheServerSafetyBoundary 断言客户端提交的产出参数
// 不进入服务端的 AI 安全边界。
func TestOutputParamsStayOutOfTheServerSafetyBoundary(t *testing.T) {
	root := filepath.Clean(repoRoot)
	domain := filepath.Join(root, "internal", "lingdoc")
	owner := filepath.Join(domain, ownerPackage)
	if _, err := os.Stat(owner); err != nil {
		t.Fatalf("找不到动作词汇包：%v", err)
	}

	fset := token.NewFileSet()
	var violations []string
	ownerFiles := 0
	domainFiles := 0

	// 规则一：词汇包自己不提安全边界。它是产出参数的拥有者，不该知道边界在哪。
	err := filepath.Walk(owner, func(path string, entry os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		if entry.IsDir() || !strings.HasSuffix(path, ".go") {
			return nil
		}
		source, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		file, err := parser.ParseFile(fset, path, source, parser.SkipObjectResolution)
		if err != nil {
			return err
		}
		rel, _ := filepath.Rel(root, path)
		rel = filepath.ToSlash(rel)
		ownerFiles++
		ast.Inspect(file, func(node ast.Node) bool {
			ident, ok := node.(*ast.Ident)
			if !ok || ident.Name != safetyBoundaryField {
				return true
			}
			violations = append(violations, rel+":"+line(fset, ident.Pos())+
				" 引用了服务端的 AI 安全边界（产出参数与安全边界不得合并）")
			return true
		})
		return nil
	})
	if err != nil {
		t.Fatalf("扫描动作词汇包失败：%v", err)
	}

	// 规则二：调用面不得把产出参数写进安全边界。
	err = filepath.Walk(domain, func(path string, entry os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		if entry.IsDir() {
			if entry.Name() == ownerPackage {
				return filepath.SkipDir
			}
			return nil
		}
		if !strings.HasSuffix(path, ".go") || strings.HasSuffix(path, "_test.go") {
			return nil
		}
		source, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		file, err := parser.ParseFile(fset, path, source, parser.SkipObjectResolution)
		if err != nil {
			return err
		}
		domainFiles++
		if !importsOwnerPackage(file) {
			return nil
		}
		rel, _ := filepath.Rel(root, path)
		rel = filepath.ToSlash(rel)
		ast.Inspect(file, func(node ast.Node) bool {
			switch typed := node.(type) {
			case *ast.CompositeLit:
				for _, element := range typed.Elts {
					keyed, ok := element.(*ast.KeyValueExpr)
					if !ok {
						continue
					}
					key, ok := keyed.Key.(*ast.Ident)
					if !ok || key.Name != safetyBoundaryField {
						continue
					}
					if mentionsOutputParam(keyed.Value) {
						violations = append(violations, rel+":"+line(fset, keyed.Pos())+
							" 把产出参数写进了服务端的 AI 安全边界")
					}
				}
			case *ast.AssignStmt:
				for _, lhs := range typed.Lhs {
					selector, ok := lhs.(*ast.SelectorExpr)
					if !ok {
						continue
					}
					field := selector.Sel
					if field.Name != safetyBoundaryField {
						continue
					}
					if len(typed.Rhs) == 1 && mentionsOutputParam(typed.Rhs[0]) {
						violations = append(violations, rel+":"+line(fset, typed.Pos())+
							" 把产出参数写进了服务端的 AI 安全边界")
					}
				}
			}
			return true
		})
		return nil
	})
	if err != nil {
		t.Fatalf("扫描 LingDoc 领域失败：%v", err)
	}
	if ownerFiles == 0 {
		t.Fatal("没有扫描到动作词汇包的文件，断言可能已经失效")
	}
	if domainFiles == 0 {
		t.Fatal("没有扫描到任何领域文件，断言可能已经失效")
	}
	for _, violation := range violations {
		t.Error(violation)
	}
}

// safetyBoundaryField 是服务端 AI 安全边界的字段名。
//
// 它是服务端组装的「不许做什么」，与客户端提交的产出参数不得混用。
const safetyBoundaryField = "OutputConstraints"

// declaresActionType 判断一个常量声明是不是用「动作类型」承载写作动作取值。
//
// 仓里另有几处同名的 Action 类型（规则集治理、模板治理），它们承载的是
// create_draft / publish 这类治理动作，与写作动作无关——所以判据是**取值**，
// 不是类型名。
func declaresActionType(spec *ast.ValueSpec) bool {
	if spec.Type == nil {
		return false
	}
	ident, ok := spec.Type.(*ast.Ident)
	if !ok {
		return false
	}
	return isActionTypeName(ident.Name)
}

// isActionTypeName 判断这个名字是否在声明「写作动作」这个类型。
func isActionTypeName(name string) bool {
	return name == "Action" || name == "WritingAction"
}

// constantStrings 取一个常量声明组里的全部字符串取值。
func constantStrings(decl *ast.GenDecl) []string {
	var values []string
	for _, spec := range decl.Specs {
		valueSpec, ok := spec.(*ast.ValueSpec)
		if !ok {
			continue
		}
		for _, value := range valueSpec.Values {
			literal, ok := value.(*ast.BasicLit)
			if !ok || literal.Kind != token.STRING {
				continue
			}
			text, err := strconv.Unquote(literal.Value)
			if err != nil {
				continue
			}
			values = append(values, text)
		}
	}
	return values
}

// literalStrings 取一个复合字面量的直接元素里的全部字符串。
func literalStrings(elements []ast.Expr) []string {
	var values []string
	for _, element := range elements {
		target := element
		if keyed, ok := element.(*ast.KeyValueExpr); ok {
			target = keyed.Value
		}
		literal, ok := target.(*ast.BasicLit)
		if !ok || literal.Kind != token.STRING {
			continue
		}
		text, err := strconv.Unquote(literal.Value)
		if err != nil {
			continue
		}
		values = append(values, text)
	}
	return values
}

// countCanonicalActions 数出一组字符串里有几个不同的动作取值。
func countCanonicalActions(values []string) int {
	seen := map[string]bool{}
	for _, value := range values {
		if canonicalActionValues[value] {
			seen[value] = true
		}
	}
	return len(seen)
}

// mentionsOutputParam 判断一个表达式里有没有产出参数的标识符。
func mentionsOutputParam(expr ast.Expr) bool {
	found := false
	ast.Inspect(expr, func(node ast.Node) bool {
		ident, ok := node.(*ast.Ident)
		if ok && outputParamIdentifiers[ident.Name] {
			found = true
			return false
		}
		return !found
	})
	return found
}

// importsOwnerPackage 判断一个文件是否引入了动作词汇包。
func importsOwnerPackage(file *ast.File) bool {
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

// line 返回一个位置所在的行号。
func line(fset *token.FileSet, pos token.Pos) string {
	return strconv.Itoa(fset.Position(pos).Line)
}
