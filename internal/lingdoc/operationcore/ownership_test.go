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
// The digest rule has two spellings. The obvious one puts the marshalling
// inside the digest call. The other one marshals into a variable first and
// digests the variable a few lines later, and that second spelling used to
// escape this guard entirely -- the candidate adoption digest was written
// that way, so the domain really did own a second copy of the rule. Both
// spellings are checked now.
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

// knownDigestExceptions lists the files that still compute a digest over a
// freshly marshalled value.
//
// The list is explicit on purpose. A digest that is not an operation request
// digest -- a content hash, a receipt hash -- is not the rule this guard
// owns, and a package owned by another line of work is not this face's to
// change. Naming those files keeps the debt visible instead of letting the
// check grow a scope it cannot justify: any new two-step digest anywhere else
// still fails this guard, and an entry that stops being true has to be
// removed on purpose.
var knownDigestExceptions = map[string]string{
	"internal/lingdoc/templategovernance/storage.go":                "规则集治理包自己的摘要（内容摘要与操作请求摘要共用一个 hashJSON）；该包归 G5 规则集线，不在本切片范围",
	"internal/lingdoc/workspace/full_workflow_guard.go":             "交付回执的内容摘要；workspace 归另一张票，本切片不改",
	"internal/lingdoc/workspacecore/impact_dependency_candidate.go": "依赖候选批次的内容摘要；workspacecore 归另一张票，本切片不改",
}

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
		if _, known := knownDigestExceptions[rel]; known {
			return nil
		}
		file, err := parser.ParseFile(fset, path, source, parser.SkipObjectResolution)
		if err != nil {
			return err
		}
		copies = append(copies, digestCopies(fset, rel, file)...)
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

// digestCopies reports the digest rules copied inside one file.
//
// A digest over a freshly marshalled value is the rule, whether the
// marshalling happens inside the digest call or a few lines earlier into a
// variable the digest call then reads.
func digestCopies(fset *token.FileSet, rel string, file *ast.File) []string {
	var copies []string
	for _, decl := range file.Decls {
		switch typed := decl.(type) {
		case *ast.FuncDecl:
			if typed.Body == nil {
				continue
			}
			copies = append(copies, digestCopiesIn(fset, rel, typed.Body, marshalledLocals(typed.Body))...)
		case *ast.GenDecl:
			for _, spec := range typed.Specs {
				value, ok := spec.(*ast.ValueSpec)
				if !ok {
					continue
				}
				copies = append(copies, digestCopiesIn(fset, rel, value, nil)...)
			}
		}
	}
	return copies
}

// digestCopiesIn reports the digest rules copied inside one body.
//
// marshalled holds the names of the local variables that hold a freshly
// marshalled value; a digest over one of them is the two-step spelling of
// the same rule.
func digestCopiesIn(fset *token.FileSet, rel string, node ast.Node, marshalled map[string]bool) []string {
	var copies []string
	ast.Inspect(node, func(node ast.Node) bool {
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
		line := strconv.Itoa(fset.Position(call.Pos()).Line)
		for _, arg := range call.Args {
			if isJSONMarshalCall(arg) {
				copies = append(copies, rel+":"+line+
					" 摘要被就地计算（应经过 operationcore.RequestHash / ValueHash）")
				continue
			}
			ident, ok := arg.(*ast.Ident)
			if !ok || !marshalled[ident.Name] {
				continue
			}
			copies = append(copies, rel+":"+line+
				" 摘要被就地计算（两步式：先 json.Marshal 再 sha256.Sum256，应经过 operationcore.RequestHash / ValueHash）")
		}
		return true
	})
	return copies
}

// marshalledLocals returns the local variables that hold a freshly marshalled
// JSON value.
//
// The two-step spelling of the digest rule is a marshalling assignment
// followed by a digest of the variable it assigned, so the guard has to know
// the variable names to recognise it.
func marshalledLocals(node ast.Node) map[string]bool {
	names := map[string]bool{}
	ast.Inspect(node, func(node ast.Node) bool {
		switch typed := node.(type) {
		case *ast.AssignStmt:
			for i, rhs := range typed.Rhs {
				if !isJSONMarshalCall(rhs) || i >= len(typed.Lhs) {
					continue
				}
				if ident, ok := typed.Lhs[i].(*ast.Ident); ok {
					names[ident.Name] = true
				}
			}
		case *ast.ValueSpec:
			for i, value := range typed.Values {
				if !isJSONMarshalCall(value) || i >= len(typed.Names) {
					continue
				}
				names[typed.Names[i].Name] = true
			}
		}
		return true
	})
	return names
}

// isJSONMarshalCall reports whether expr is a call to json.Marshal.
func isJSONMarshalCall(expr ast.Expr) bool {
	call, ok := expr.(*ast.CallExpr)
	if !ok {
		return false
	}
	sel, ok := call.Fun.(*ast.SelectorExpr)
	if !ok {
		return false
	}
	pkg, ok := sel.X.(*ast.Ident)
	return ok && pkg.Name == "json" && sel.Sel.Name == "Marshal"
}
