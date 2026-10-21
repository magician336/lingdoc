package writingaction_test

import (
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"reflect"
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

// 第三条守护（A34）：跨包落到字段上的写作动作必须用写作动作的专名，
// 并且来源取值表只有词汇包有一份。
//
// 两条判据：
//
//   - 跨包字段承载写作动作时，字段名必须是 WritingAction、JSON 键必须是
//     writing_action、类型必须是词汇包的 Action。Action 这个名字在治理层
//     已经被三义占用（依赖边动作、审计动作、治理操作），跨包再叫 Action 就是
//     撞名——两处代码各读各的，改动也不会编译报错。
//   - 来源取值表只有词汇包有一份。规格字段的来源（SpecField.Origin）沿用同一
//     组三值，是一笔**已记的债**；它被显式列为例外，例外带理由，不静默忽略。
//
// 两条判据都按**取值**判，不按类型名判（开发计划 D28）：仓里的
// rulesetstore.Action、templategovernance.Action、workspacecore.AuthorizationAction
// 都是同名不同义的治理动作词汇，按类型名判会全误报。
const (
	// writingActionFieldName 是跨包承载写作动作的字段必须用的名字。
	writingActionFieldName = "WritingAction"
	// writingActionJSONKey 是跨包承载写作动作的字段必须用的 JSON 键。
	writingActionJSONKey = "writing_action"
)

// canonicalOriginValues 是来源词表的取值集合。
//
// 取值抄自词汇包本身——词汇包是这份知识的唯一拥有者，守护照着它判，
// 词汇包加了取值，守护跟着长。
var canonicalOriginValues = map[string]bool{
	string(writingaction.OriginHuman):     true,
	string(writingaction.OriginGenerated): true,
	string(writingaction.OriginAssisted):  true,
}

// knownOriginVocabularyExceptions 列出仍然带着来源取值表副本的文件。
//
// 例外必须具名：写明是哪一处重复、为什么现在允许它、以及什么时候该收拢。
// 静默忽略等于没有守护——下一个人会以为词表只有一份。
//
// 这一笔债是规格字段的来源（workspacecore.SpecField.Origin）：它与词汇包
// 同三值，因此收拢到 internal/lingdoc/writingaction 属后续项，不在本面范围
// （开发计划 D24）。例外按文件登记，与 operationcore 的摘要守护同一写法：
// 一条具名例外对应一处已知重复；债还了就把这一条删掉，守护会自己发现。
var knownOriginVocabularyExceptions = map[string]string{
	"internal/lingdoc/workspacecore/g1_inherited_fields.go":    "继承字段的来源判定（SpecField.Origin）沿用既有三值词表，与词汇包同取值；收拢到 writingaction 属后续项，不在本面范围",
	"internal/lingdoc/workspacecore/g1_provenance_service.go":  "来源标注的产出（SpecField.Origin）沿用既有三值词表，与词汇包同取值；收拢到 writingaction 属后续项，不在本面范围",
	"internal/lingdoc/workspacecore/g2_change_draft_action.go": "变更草案的来源判定（SpecField.Origin）沿用既有三值词表，与词汇包同取值；收拢到 writingaction 属后续项，不在本面范围",
	"internal/lingdoc/workspacecore/g2_change_draft_edit.go":   "AI 建议来源的判定（SpecField.Origin）沿用既有三值词表，与词汇包同取值；收拢到 writingaction 属后续项，不在本面范围",
	"internal/lingdoc/workspacecore/service_facade.go":         "规格字段元数据的来源校验（SpecField.Origin）沿用既有三值词表，与词汇包同取值；收拢到 writingaction 属后续项，不在本面范围",
}

// TestCrossPackageWritingActionFieldUsesTheProperName 断言跨包落到字段上的
// 写作动作用写作动作的专名，而不是与治理层同名的泛称。
func TestCrossPackageWritingActionFieldUsesTheProperName(t *testing.T) {
	root := filepath.Clean(repoRoot)
	domain := filepath.Join(root, "internal", "lingdoc")
	if _, err := os.Stat(domain); err != nil {
		t.Fatalf("找不到 LingDoc 领域目录：%v", err)
	}

	fset := token.NewFileSet()
	var violations []string
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
		violations = append(violations, crossPackageActionFieldViolations(fset, rel, file)...)
		return nil
	})
	if err != nil {
		t.Fatalf("扫描 LingDoc 领域失败：%v", err)
	}
	if checked == 0 {
		t.Fatal("没有扫描到任何文件，断言可能已经失效")
	}
	for _, violation := range violations {
		t.Error(violation)
	}
}

// TestOriginVocabularyHasOneOwner 断言除词汇包外，任何包都不得再定义
// 来源取值表。
//
// 判据是**取值**：一处声明或一个函数里出现两个以上的来源取值，就是一份
// 取值表。按类型名判会误报——来源标注在规格字段与候选上用的是同一个词，
// 但只有词汇包是它的拥有者。
func TestOriginVocabularyHasOneOwner(t *testing.T) {
	root := filepath.Clean(repoRoot)
	domain := filepath.Join(root, "internal", "lingdoc")
	if _, err := os.Stat(domain); err != nil {
		t.Fatalf("找不到 LingDoc 领域目录：%v", err)
	}

	fset := token.NewFileSet()
	var violations []string
	// 例外覆盖到的站点数：例外一旦不再被用到，就该被删掉。
	exceptionUse := map[string]int{}
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
		for _, site := range originVocabularySites(fset, rel, file) {
			if _, known := knownOriginVocabularyExceptions[rel]; known {
				exceptionUse[rel]++
				continue
			}
			violations = append(violations, site+"（来源取值表归 writingaction 独占）")
		}
		return nil
	})
	if err != nil {
		t.Fatalf("扫描 LingDoc 领域失败：%v", err)
	}
	if checked == 0 {
		t.Fatal("没有扫描到任何文件，断言可能已经失效")
	}

	// 例外不得静默失效：一条不再覆盖任何站点的例外说明债已经还了，
	// 应当从名单里删掉，否则它会变成一个藏住新违规的口子。
	for file := range knownOriginVocabularyExceptions {
		if exceptionUse[file] == 0 {
			t.Errorf("例外 %s 已经不再覆盖任何来源取值表站点：债还了，请把这一条从名单里删掉", file)
		}
	}
	for _, violation := range violations {
		t.Error(violation)
	}
}

// crossPackageActionFieldViolations 报告一个文件里跨包承载写作动作、
// 却没用写作动作专名的字段。
func crossPackageActionFieldViolations(fset *token.FileSet, rel string, file *ast.File) []string {
	alias := ownerImportAlias(file)
	aliases := localActionAliases(file, alias)
	if alias == "" && len(aliases) == 0 {
		return nil
	}

	var violations []string
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
			structType, ok := typeSpec.Type.(*ast.StructType)
			if !ok {
				continue
			}
			for _, field := range structType.Fields.List {
				if len(field.Names) == 0 {
					// 嵌入字段没有名字，规则管不到它。
					continue
				}
				name := field.Names[0].Name
				key, tagged := jsonKeyOf(field)
				typed := typeIsWritingAction(field.Type, alias, aliases)
				carriesAction := typed || key == writingActionJSONKey
				if !carriesAction {
					continue
				}
				if name != writingActionFieldName {
					violations = append(violations, rel+":"+line(fset, field.Pos())+
						" 跨包承载写作动作的字段叫 "+name+
						"（必须叫 "+writingActionFieldName+"：Action 这个名字在治理层已被三义占用）")
				}
				if tagged && key != writingActionJSONKey {
					violations = append(violations, rel+":"+line(fset, field.Pos())+
						" 写作动作字段的 JSON 键是 "+key+"（必须是 "+writingActionJSONKey+"）")
				}
				if key == writingActionJSONKey && !typed {
					violations = append(violations, rel+":"+line(fset, field.Pos())+
						" 写作动作字段的类型不是词汇包的 Action（写作动作的类型归 writingaction 独占）")
				}
			}
		}
	}
	return violations
}

// originVocabularySites 返回一个文件里定义了来源取值表的站点。
//
// 一处常量组、一个变量组或一个函数里出现两个以上的来源取值，就是一份取值表。
func originVocabularySites(fset *token.FileSet, rel string, file *ast.File) []string {
	var sites []string
	for _, decl := range file.Decls {
		switch typed := decl.(type) {
		case *ast.FuncDecl:
			if typed.Body == nil {
				continue
			}
			if len(originLiterals(typed.Body)) >= 2 {
				sites = append(sites, rel+":"+line(fset, typed.Pos())+
					" 在 "+typed.Name.Name+" 里定义了来源取值表")
			}
		case *ast.GenDecl:
			if typed.Tok != token.CONST && typed.Tok != token.VAR {
				continue
			}
			if len(originLiterals(typed)) >= 2 {
				sites = append(sites, rel+":"+line(fset, typed.Pos())+
					" 有一组来源取值常量")
			}
		}
	}
	return sites
}

// originLiterals 取一个节点里出现过的来源取值（去重）。
func originLiterals(node ast.Node) []string {
	seen := map[string]bool{}
	var values []string
	ast.Inspect(node, func(node ast.Node) bool {
		literal, ok := node.(*ast.BasicLit)
		if !ok || literal.Kind != token.STRING {
			return true
		}
		text, err := strconv.Unquote(literal.Value)
		if err != nil || !canonicalOriginValues[text] || seen[text] {
			return true
		}
		seen[text] = true
		values = append(values, text)
		return true
	})
	return values
}

// ownerImportAlias 返回一个文件里词汇包的导入别名；没有引入就是空串。
func ownerImportAlias(file *ast.File) string {
	for _, spec := range file.Imports {
		path, err := strconv.Unquote(spec.Path.Value)
		if err != nil {
			continue
		}
		if !strings.HasSuffix(path, "internal/lingdoc/"+ownerPackage) {
			continue
		}
		if spec.Name != nil {
			return spec.Name.Name
		}
		return ownerPackage
	}
	return ""
}

// localActionAliases 返回一个文件里指向词汇包 Action 的本地类型别名。
//
// 别名是绕过守护的后门：type Action = writingaction.Action 之后，
// 字段类型写成 Action 就不再带包名，守护必须照样认出它。
func localActionAliases(file *ast.File, alias string) map[string]bool {
	names := map[string]bool{}
	if alias == "" {
		return names
	}
	for _, decl := range file.Decls {
		generic, ok := decl.(*ast.GenDecl)
		if !ok || generic.Tok != token.TYPE {
			continue
		}
		for _, spec := range generic.Specs {
			typeSpec, ok := spec.(*ast.TypeSpec)
			if !ok || typeSpec.Assign == token.NoPos {
				continue
			}
			selector, ok := typeSpec.Type.(*ast.SelectorExpr)
			if !ok {
				continue
			}
			ident, ok := selector.X.(*ast.Ident)
			if !ok || ident.Name != alias || selector.Sel.Name != "Action" {
				continue
			}
			names[typeSpec.Name.Name] = true
		}
	}
	return names
}

// typeIsWritingAction 判断一个类型是不是词汇包的写作动作类型。
func typeIsWritingAction(expr ast.Expr, alias string, aliases map[string]bool) bool {
	switch typed := expr.(type) {
	case *ast.StarExpr:
		return typeIsWritingAction(typed.X, alias, aliases)
	case *ast.SelectorExpr:
		ident, ok := typed.X.(*ast.Ident)
		if !ok || alias == "" || ident.Name != alias {
			return false
		}
		return typed.Sel.Name == "Action"
	case *ast.Ident:
		return aliases[typed.Name]
	}
	return false
}

// jsonKeyOf 取一个结构体字段的 JSON 键；没有 JSON 标签时第二个返回值为 false。
func jsonKeyOf(field *ast.Field) (string, bool) {
	if field.Tag == nil {
		return "", false
	}
	raw, err := strconv.Unquote(field.Tag.Value)
	if err != nil {
		return "", false
	}
	value, ok := reflect.StructTag(raw).Lookup("json")
	if !ok {
		return "", false
	}
	key := strings.Split(value, ",")[0]
	return key, true
}
