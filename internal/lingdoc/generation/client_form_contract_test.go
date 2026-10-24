package generation

import (
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"runtime"
	"sort"
	"strconv"
	"strings"
	"testing"

	"github.com/Tencent/WeKnora/internal/clienterror"
)

// 这个面对外的形态由**错误自己**声明（`clienterror`），传输层只渲染、不决定。
// 于是「一个错误码带几个可重试取值」这件事，答案在声明处，而不在传输层的映射表里。
//
// `dependency_unavailable` 是这件事唯一要紧的码：它的承诺是「再试一次可能成功」。
// 同一个码在仓内曾有第二个取值（工作区代理面 `agent_error_wording.go` 写的是
// `Retryable: false`），而生成面此前把「受控 profile 拒绝模型」也压成了这个码——
// 于是调用方被导向无限重试，而真正要做的（改 profile 授权）重试一万次也不会发生。
//
// 下面的守护把生成面**声明出来**的形态读出来（读声明，不是读传输层），并断言：
// 一个错误码在这个面上只有一个可重试取值。

// declaredForm 是这个面声明出来的一种形态：声明位置、错误码、可重试标记。
type declaredForm struct {
	location  string
	code      string
	retryable bool
}

// TestDependencyUnavailableHasOneRetryableValueOnThisSurface 钉住那一条码。
//
// 它只能是「可重试」的那一个取值：重复一次有可能成功，这是这个码的承诺。
func TestDependencyUnavailableHasOneRetryableValueOnThisSurface(t *testing.T) {
	forms := generationSurfaceForms(t)
	seen := map[bool][]string{}
	for _, form := range forms {
		if form.code == "dependency_unavailable" {
			seen[form.retryable] = append(seen[form.retryable], form.location)
		}
	}
	if len(seen) == 0 {
		t.Fatal("生成面没有声明 dependency_unavailable")
	}
	if len(seen) > 1 {
		t.Fatalf("dependency_unavailable 在生成面有 %d 个可重试取值：%v", len(seen), seen)
	}
	if len(seen[true]) == 0 {
		t.Fatalf("dependency_unavailable 在生成面不是可重试的：%v", seen)
	}
}

// TestEveryCodeOnThisSurfaceHasOneRetryableValue 把那条钉子推广到这个面全部形态。
//
// 同码不同**文案**是允许的（那是不同形态），同码不同**可重试取值**不是：同一个码
// 对一个调用方说「再试一次」、对另一个调用方说「别试了」，没有调用方能据此行动。
func TestEveryCodeOnThisSurfaceHasOneRetryableValue(t *testing.T) {
	forms := generationSurfaceForms(t)
	byCode := map[string]map[bool][]string{}
	for _, form := range forms {
		if byCode[form.code] == nil {
			byCode[form.code] = map[bool][]string{}
		}
		byCode[form.code][form.retryable] = append(byCode[form.code][form.retryable], form.location)
	}
	codes := make([]string, 0, len(byCode))
	for code := range byCode {
		codes = append(codes, code)
	}
	sort.Strings(codes)
	for _, code := range codes {
		if len(byCode[code]) > 1 {
			t.Errorf("错误码 %q 在生成面有 %d 个可重试取值：%v", code, len(byCode[code]), byCode[code])
		}
	}
}

// TestSharedCatalogGivesEveryCodeOneRetryableValue 把同一条钉子钉在共享形态目录上。
//
// 目录是这个仓的形态主人：它按「状态码 + 错误码 + 文案」收形态，所以同一个码在
// 目录里可以有多句文案，但**可重试取值只能有一个**——目录里给出的那个承诺，是
// 所有引用它的面的承诺。
func TestSharedCatalogGivesEveryCodeOneRetryableValue(t *testing.T) {
	catalog := sharedCatalogForms(t)
	byCode := map[string]map[bool][]string{}
	for name, form := range catalog {
		if byCode[form.code] == nil {
			byCode[form.code] = map[bool][]string{}
		}
		byCode[form.code][form.retryable] = append(byCode[form.code][form.retryable], name)
	}
	codes := make([]string, 0, len(byCode))
	for code := range byCode {
		codes = append(codes, code)
	}
	sort.Strings(codes)
	for _, code := range codes {
		if len(byCode[code]) > 1 {
			t.Errorf("共享形态目录里错误码 %q 有 %d 个可重试取值：%v", code, len(byCode[code]), byCode[code])
		}
	}
}

// generationSurfaceForms 读出这个包声明的全部对外形态。
//
// 形态有两种写法：`clienterror.Form{...}` 字面量，和引用共享目录里
// `clienterror.<Name>` 的写法。两种都要读，否则「目录里多了一个同码形态」这件事
// 会从这条守护的视野里漏掉。
func generationSurfaceForms(t *testing.T) []declaredForm {
	t.Helper()
	catalog := sharedCatalogForms(t)
	dir := generationPackageDir(t)
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatalf("读包目录失败：%v", err)
	}
	var forms []declaredForm
	for _, entry := range entries {
		name := entry.Name()
		if entry.IsDir() || !strings.HasSuffix(name, ".go") || strings.HasSuffix(name, "_test.go") {
			continue
		}
		forms = append(forms, formsInFile(t, filepath.Join(dir, name), catalog)...)
	}
	// 同理：这个面读不到形态就是读法坏了。
	if len(forms) == 0 {
		t.Fatalf("生成面没有读到任何形态，这条守护会空跑")
	}
	return forms
}

// generationPackageDir 给出这个包源码所在目录。
//
// 用调用点自己的位置定位，而不是靠相对路径猜层级：这个包在 `internal/lingdoc/`
// 下面，共享目录在 `internal/` 下面，两者的相对位置会随仓库布局变化。
func generationPackageDir(t *testing.T) string {
	t.Helper()
	_, file, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("定位本测试文件失败")
	}
	return filepath.Dir(file)
}

// sharedCatalogForms 读共享形态目录（`internal/clienterror/catalog.go`）里声明的形态。
//
// 按路径读那个文件不会给这个包加依赖边：这个包本来就依赖 `clienterror`，而目录
// 本身是那个叶子包里的一个文件。
func sharedCatalogForms(t *testing.T) map[string]declaredForm {
	t.Helper()
	path := filepath.Join(generationPackageDir(t), "..", "..", "clienterror", "catalog.go")
	file, err := parser.ParseFile(token.NewFileSet(), path, nil, parser.SkipObjectResolution)
	if err != nil {
		t.Fatalf("解析 %s 失败：%v", path, err)
	}
	forms := map[string]declaredForm{}
	for _, decl := range file.Decls {
		gen, ok := decl.(*ast.GenDecl)
		if !ok || gen.Tok != token.VAR {
			continue
		}
		for _, spec := range gen.Specs {
			value, ok := spec.(*ast.ValueSpec)
			if !ok || len(value.Names) != 1 || len(value.Values) != 1 {
				continue
			}
			lit, ok := value.Values[0].(*ast.CompositeLit)
			if !ok || !isCatalogForm(lit) {
				continue
			}
			code, retryable, ok := formLiteralValues(lit)
			if !ok {
				continue
			}
			forms[value.Names[0].Name] = declaredForm{location: path, code: code, retryable: retryable}
		}
	}
	// 读不到形态就说明读法坏了：空目录会让下面两条守护空跑通过。
	if len(forms) == 0 {
		t.Fatalf("%s 里没有读到任何形态，这条守护会空跑", path)
	}
	return forms
}

// formsInFile 读出一个文件里声明的全部对外形态。
func formsInFile(t *testing.T, path string, catalog map[string]declaredForm) []declaredForm {
	t.Helper()
	fset := token.NewFileSet()
	file, err := parser.ParseFile(fset, path, nil, parser.SkipObjectResolution)
	if err != nil {
		t.Fatalf("解析 %s 失败：%v", path, err)
	}
	var forms []declaredForm
	// 先记「作为 New/Wrap 第一个实参」的字面量：它们由调用点记一次，不重复记。
	claimed := map[*ast.CompositeLit]bool{}
	ast.Inspect(file, func(node ast.Node) bool {
		call, ok := node.(*ast.CallExpr)
		if !ok || !isClientErrorConstructor(call) || len(call.Args) == 0 {
			return true
		}
		form, ok := resolveForm(t, call.Args[0], catalog)
		if !ok {
			return true
		}
		form.location = fset.Position(call.Pos()).String()
		forms = append(forms, form)
		if lit, isLit := call.Args[0].(*ast.CompositeLit); isLit {
			claimed[lit] = true
		}
		return true
	})
	// 没有被调用点认领的字面量：它自己就是一处声明。
	ast.Inspect(file, func(node ast.Node) bool {
		lit, ok := node.(*ast.CompositeLit)
		if !ok || claimed[lit] || !isClientErrorForm(lit) {
			return true
		}
		code, retryable, ok := formLiteralValues(lit)
		if !ok {
			return true
		}
		forms = append(forms, declaredForm{location: fset.Position(lit.Pos()).String(), code: code, retryable: retryable})
		return true
	})
	return forms
}

// resolveForm 读出一处形态声明：错误码与可重试标记。
//
// 引用共享目录里的形态时按名字查表；查不到就是引用了一个不存在的东西——
// 那会让这条守护悄悄少看一种形态，所以这里直接失败。
func resolveForm(t *testing.T, expr ast.Expr, catalog map[string]declaredForm) (declaredForm, bool) {
	switch value := expr.(type) {
	case *ast.CompositeLit:
		if !isClientErrorForm(value) {
			return declaredForm{}, false
		}
		code, retryable, ok := formLiteralValues(value)
		if !ok {
			return declaredForm{}, false
		}
		return declaredForm{code: code, retryable: retryable}, true
	case *ast.SelectorExpr:
		ident, ok := value.X.(*ast.Ident)
		if !ok || ident.Name != "clienterror" {
			return declaredForm{}, false
		}
		form, found := catalog[value.Sel.Name]
		if !found {
			t.Fatalf("clienterror.%s 不在共享形态目录里，这条守护读不到它", value.Sel.Name)
		}
		return form, true
	}
	return declaredForm{}, false
}

// formLiteralValues 读出 `clienterror.Form{...}` 字面量里的错误码与可重试标记。
func formLiteralValues(lit *ast.CompositeLit) (string, bool, bool) {
	var code string
	retryable := false
	for _, element := range lit.Elts {
		field, ok := element.(*ast.KeyValueExpr)
		if !ok {
			continue
		}
		key, ok := field.Key.(*ast.Ident)
		if !ok {
			continue
		}
		switch key.Name {
		case "Code":
			// 字符串字面量是 *ast.BasicLit。
			if value, ok := field.Value.(*ast.BasicLit); ok && value.Kind == token.STRING {
				code, _ = strconv.Unquote(value.Value)
			}
		case "Retryable":
			// 布尔字面量在 go/ast 里是 *ast.Ident，不是 *ast.BasicLit。
			if value, ok := field.Value.(*ast.Ident); ok {
				retryable = value.Name == "true"
			}
		}
	}
	return code, retryable, code != ""
}

// isClientErrorForm 判断一个字面量是不是 `clienterror.Form{...}`。
func isClientErrorForm(lit *ast.CompositeLit) bool {
	selector, ok := lit.Type.(*ast.SelectorExpr)
	if !ok {
		return false
	}
	ident, ok := selector.X.(*ast.Ident)
	return ok && ident.Name == "clienterror" && selector.Sel.Name == "Form"
}

// isCatalogForm 判断一个字面量是不是共享目录里那种 `Form{...}`。
//
// 目录文件在 `clienterror` 包自己里面，所以那里的类型名不带包限定名。
func isCatalogForm(lit *ast.CompositeLit) bool {
	ident, ok := lit.Type.(*ast.Ident)
	return ok && ident.Name == "Form"
}

// isClientErrorConstructor 判断一个调用是不是 `clienterror.New` / `clienterror.Wrap`。
func isClientErrorConstructor(call *ast.CallExpr) bool {
	selector, ok := call.Fun.(*ast.SelectorExpr)
	if !ok {
		return false
	}
	ident, ok := selector.X.(*ast.Ident)
	if !ok || ident.Name != "clienterror" {
		return false
	}
	return selector.Sel.Name == "New" || selector.Sel.Name == "Wrap"
}

// 生成面自己声明形态的两种写法都在上面读到了；这里留一条断言提醒：形态变了，
// 目录里也要有对应的登记（否则别的面引不到同一个形态）。
func TestTheSurfaceUsesFormsThatDeclareTheirOwnPresentation(t *testing.T) {
	form, ok := clienterror.FormOf(ErrDependencyUnavailable)
	if !ok {
		t.Fatal("ErrDependencyUnavailable 没有声明对外形态")
	}
	if form.Code != "dependency_unavailable" {
		t.Fatalf("ErrDependencyUnavailable 的错误码 = %q，want dependency_unavailable", form.Code)
	}
	rejected, ok := clienterror.FormOf(ErrAcceptanceModelRejected)
	if !ok {
		t.Fatal("ErrAcceptanceModelRejected 没有声明对外形态")
	}
	if rejected.Code == form.Code {
		t.Fatalf("策略拒绝与依赖不可用仍共用错误码 %q", rejected.Code)
	}
}
