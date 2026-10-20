package workspacecore

import (
	"go/ast"
	"go/parser"
	"go/token"
	"go/types"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
)

// The change-set status vocabulary has one owner: this package's declaration.
//
// The vocabulary used to be spelled as bare string literals in ten production
// files, so adding a state meant finding every place that had to learn about
// it — and a place that was missed would silently keep accepting the old set.
//
// The assertion below locates the places where a change set's own status is
// written or compared, and refuses a bare literal there. Locating them needs
// to know which value is a change set, so the check is type-aware: the
// package is type-checked and the receiver of a ".Status" selector is resolved
// to its type.
//
// Other aggregates in this package have a status of their own and use some of
// the same words — projects use "draft" and "active", rule sets use "draft",
// member records use "rejected", template copies use "draft", spec fields use
// "draft", review anchors use "superseded", runtime events use "rejected" and
// "stale", G2 tasks and runs use "draft" and "stale", dependency edges use
// "rejected". A string-based scan would flag all of them, so the scan has to
// be type-aware to mean anything.

func TestChangeSetStatusHasOneOwner(t *testing.T) {
	dir := "."
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatalf("读取包目录失败：%v", err)
	}
	fset := token.NewFileSet()
	files := map[string]*ast.File{}
	ordered := []*ast.File{}
	for _, entry := range entries {
		name := entry.Name()
		if entry.IsDir() || !strings.HasSuffix(name, ".go") || strings.HasSuffix(name, "_test.go") {
			continue
		}
		parsed, parseErr := parser.ParseFile(fset, filepath.Join(dir, name), nil, parser.SkipObjectResolution)
		if parseErr != nil {
			t.Fatalf("解析 %s 失败：%v", name, parseErr)
		}
		files[name] = parsed
		ordered = append(ordered, parsed)
	}
	if len(files) == 0 {
		t.Fatal("没有找到生产文件")
	}

	info := &types.Info{
		Types: map[ast.Expr]types.TypeAndValue{},
		Defs:  map[*ast.Ident]types.Object{},
		Uses:  map[*ast.Ident]types.Object{},
	}
	conf := types.Config{
		Importer:                 stubImporter{},
		IgnoreFuncBodies:         false,
		DisableUnusedImportCheck: true,
		Error:                    func(error) {}, // 未解析的第三方导入不阻断本地类型推导
	}
	pkg, _ := conf.Check("lingdoc/workspacecore", fset, ordered, info)
	if pkg == nil {
		t.Fatal("类型检查没有产出包信息")
	}

	var problems []string
	report := func(name string, node ast.Node, value string) {
		problems = append(problems, name+":"+strconv.Itoa(fset.Position(node.Pos()).Line)+
			" 变更集状态位置出现了裸字面量 "+value)
	}

	for name, file := range files {
		ast.Inspect(file, func(node ast.Node) bool {
			switch current := node.(type) {
			case *ast.AssignStmt:
				// change.Status = "applied"
				for i, lhs := range current.Lhs {
					sel, ok := lhs.(*ast.SelectorExpr)
					if !ok || sel.Sel.Name != "Status" || !isChangeSetType(info, sel.X) {
						continue
					}
					if i < len(current.Rhs) {
						checkLiteral(name, report, info, current.Rhs[i])
					}
				}
			case *ast.BinaryExpr:
				// change.Status == "applied"
				if current.Op != token.EQL && current.Op != token.NEQ {
					return true
				}
				left, leftOK := current.X.(*ast.SelectorExpr)
				right, rightOK := current.Y.(*ast.SelectorExpr)
				switch {
				case leftOK && left.Sel.Name == "Status" && isChangeSetType(info, left.X):
					checkLiteral(name, report, info, current.Y)
				case rightOK && right.Sel.Name == "Status" && isChangeSetType(info, right.X):
					checkLiteral(name, report, info, current.X)
				}
			case *ast.CompositeLit:
				// ChangeSet{Status: "assessed"}
				if !isChangeSetType(info, current) {
					return true
				}
				for _, element := range current.Elts {
					kv, ok := element.(*ast.KeyValueExpr)
					if !ok {
						continue
					}
					if key, ok := kv.Key.(*ast.Ident); ok && key.Name == "Status" {
						checkLiteral(name, report, info, kv.Value)
					}
				}
			case *ast.CallExpr:
				// slices.Contains([]string{"applied"}, change.Status)
				if !isSlicesContains(current) {
					return true
				}
				var statusArg ast.Expr
				var listArg ast.Expr
				for _, arg := range current.Args {
					sel, ok := arg.(*ast.SelectorExpr)
					if ok && sel.Sel.Name == "Status" && isChangeSetType(info, sel.X) {
						statusArg = arg
						continue
					}
					if lit, ok := arg.(*ast.CompositeLit); ok {
						listArg = lit
					}
				}
				if statusArg == nil || listArg == nil {
					return true
				}
				list, ok := listArg.(*ast.CompositeLit)
				if !ok {
					return true
				}
				for _, element := range list.Elts {
					checkLiteral(name, report, info, element)
				}
			case *ast.SwitchStmt:
				// switch change.Status { case "draft": ... }
				tag, tagOK := current.Tag.(*ast.SelectorExpr)
				if !tagOK || tag.Sel.Name != "Status" || !isChangeSetType(info, tag.X) {
					return true
				}
				for _, stmt := range current.Body.List {
					clause, clauseOK := stmt.(*ast.CaseClause)
					if !clauseOK {
						continue
					}
					for _, expr := range clause.List {
						checkLiteral(name, report, info, expr)
					}
				}
			case *ast.TypeSwitchStmt:
				return true
			}
			return true
		})
	}
	if len(problems) > 0 {
		t.Fatalf("变更集状态仍有裸字面量，请改用声明处的常量：\n  %s", strings.Join(problems, "\n  "))
	}
}

// checkLiteral reports a bare string literal that carries a change-set status.
func checkLiteral(name string, report func(string, ast.Node, string), _ *types.Info, expr ast.Expr) {
	lit, ok := expr.(*ast.BasicLit)
	if !ok || lit.Kind != token.STRING {
		return
	}
	value, err := strconv.Unquote(lit.Value)
	if err != nil || !IsChangeSetStatus(value) {
		return
	}
	report(name, lit, value)
}

// isChangeSetType reports whether the expression's type is a change set.
func isChangeSetType(info *types.Info, expr ast.Expr) bool {
	tv, ok := info.Types[expr]
	if !ok {
		return false
	}
	named, ok := tv.Type.(*types.Named)
	if !ok {
		return false
	}
	obj := named.Obj()
	return obj != nil && obj.Name() == "ChangeSet" && obj.Pkg() != nil &&
		obj.Pkg().Path() == "lingdoc/workspacecore"
}

// isSlicesContains reports whether the call is slices.Contains.
func isSlicesContains(call *ast.CallExpr) bool {
	sel, ok := call.Fun.(*ast.SelectorExpr)
	if !ok || sel.Sel.Name != "Contains" {
		return false
	}
	pkg, ok := sel.X.(*ast.Ident)
	return ok && pkg.Name == "slices"
}

// stubImporter answers every import with an empty package. The local
// declarations — including ChangeSet — still type-check, which is all this
// assertion needs; expressions that depend on a third-party type simply carry
// no type and are ignored.
type stubImporter struct{}

func (stubImporter) Import(path string) (*types.Package, error) {
	return types.NewPackage(path, ""), nil
}
