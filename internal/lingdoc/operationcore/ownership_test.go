package operationcore

import (
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
)

// The operation rules have exactly one owner.
//
// The idempotency key length rule and the request body digest used to be
// written out again at every place that needed them. Each copy was a place
// where the copies could drift: nothing forced the research pipeline's digest
// to agree with the main pipeline's, so "the same request" could come to mean
// two things.
//
// The assertion walks the LingDoc domain and reports every remaining copy of
// either rule. It looks for:
//
//   - the key length rule written out as a comparison on the key's length;
//   - a digest computed as sha256 over a freshly marshalled value.
//
// Two kinds of file are out of its reach and are not counted:
//
//   - this package, which owns the rules;
//   - `internal/evidence`, which is host level and therefore cannot import a
//     LingDoc domain package. Its one copy is recorded in the implementation
//     notes as the known exception.
//
// Test files are out of scope too: a test that forges a digest is producing a
// fixture, not computing the rule.

const keyLengthRule = "len(key) < 8 || len(key) > 128"

func TestOperationRulesHaveOneOwner(t *testing.T) {
	root := filepath.Join("..", "..", "..")
	domain := filepath.Join(root, "internal", "lingdoc")
	if _, err := os.Stat(domain); err != nil {
		t.Fatalf("找不到 LingDoc 领域目录：%v", err)
	}

	fset := token.NewFileSet()
	var copies []string
	checked := 0

	err := filepath.Walk(domain, func(path string, entry os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		if entry.IsDir() {
			// The owner itself is not a copy.
			if entry.Name() == "operationcore" {
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

		if strings.Contains(string(source), keyLengthRule) {
			copies = append(copies, rel+": 幂等键长度规则被写死（应经过 operationcore.ValidKey）")
		}
		file, err := parser.ParseFile(fset, path, source, parser.SkipObjectResolution)
		if err != nil {
			return err
		}
		ast.Inspect(file, func(node ast.Node) bool {
			call, ok := node.(*ast.CallExpr)
			if !ok {
				return true
			}
			sel, ok := call.Fun.(*ast.SelectorExpr)
			if !ok {
				return true
			}
			pkg, ok := sel.X.(*ast.Ident)
			if !ok || pkg.Name != "sha256" || sel.Sel.Name != "Sum256" {
				return true
			}
			// sha256 over a freshly marshalled value is the digest rule.
			for _, arg := range call.Args {
				inner, ok := arg.(*ast.CallExpr)
				if !ok {
					continue
				}
				innerSel, ok := inner.Fun.(*ast.SelectorExpr)
				if !ok {
					continue
				}
				innerPkg, ok := innerSel.X.(*ast.Ident)
				if ok && innerPkg.Name == "json" && innerSel.Sel.Name == "Marshal" {
					copies = append(copies, rel+":"+strconv.Itoa(fset.Position(call.Pos()).Line)+
						" 摘要被就地计算（应经过 operationcore.RequestHash / ValueHash）")
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
