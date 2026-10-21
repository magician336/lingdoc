package workspace

import (
	"fmt"
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
)

// This file is the structural assertion behind ticket #266: the transport
// layer must not name the aggregate concrete type.
//
// The aggregate is workspacecore.Service. A transport surface that holds a
// *core.Service can call all of its methods, so every narrow port in this
// package is worth nothing unless the aggregate type itself disappears from
// the transport layer.
//
// The assertion is a source-level scan with go/parser and go/ast, not a string
// match: it reads every type expression of every non-test file in this
// package and reports the file and line that still names the aggregate.
//
// The review anchor surface (review_anchor_http.go) is proven compliant by
// this assertion: it already consumed the narrow ReviewAnchorReadApplication
// and ReviewAnchorWriteApplication ports, and it must never appear in the
// whitelist below.

const workspaceCoreImportPath = "github.com/Tencent/WeKnora/internal/lingdoc/workspacecore"

// aggregateHolders lists the files in this package that still name the
// aggregate concrete type, each with the ticket that owns its migration.
//
// Every entry is a surface that has not been migrated yet. The list must
// shrink to nothing: #274 (full workflow), #267 (impact centre and roadmap)
// and #275 (G2) remove their entries, and #269 retires the alias layer that
// is the last one. A file that stops naming the aggregate must lose its
// entry in the same change, otherwise the assertion reports a stale entry.
//
// #269 retired the last three entries (the alias layer, the wide port layer
// and the disposition proof), so no non-test file of this package names the
// aggregate any more.
var aggregateHolders = map[string]string{}

// aggregateReference is one place where a file names the aggregate type.
type aggregateReference struct {
	line int
	text string
}

// aggregateWhitelistNote explains the whitelist in every failure message, so
// a reader can tell an intentional exception from a new violation.
func aggregateWhitelistNote() string {
	names := make([]string, 0, len(aggregateHolders))
	for name := range aggregateHolders {
		names = append(names, name)
	}
	sort.Strings(names)
	lines := make([]string, 0, len(names))
	for _, name := range names {
		lines = append(lines, fmt.Sprintf("%s (%s)", name, aggregateHolders[name]))
	}
	return "the files still allowed to name the aggregate are: " + strings.Join(lines, ", ")
}

// collectAggregateReferences returns every place where the file names the
// aggregate concrete type, either as core.Service / workspacecore.Service or
// through the package-local alias Service declared in core_alias.go.
func collectAggregateReferences(fset *token.FileSet, path string, file *ast.File) []aggregateReference {
	aliases := map[string]bool{}
	for _, spec := range file.Imports {
		value, err := strconv.Unquote(spec.Path.Value)
		if err != nil || value != workspaceCoreImportPath {
			continue
		}
		if spec.Name != nil {
			aliases[spec.Name.Name] = true
			continue
		}
		aliases["workspacecore"] = true
	}

	var references []aggregateReference
	record := func(expr ast.Expr, text string) {
		references = append(references, aggregateReference{line: fset.Position(expr.Pos()).Line, text: text})
	}

	var visitType func(expr ast.Expr)
	var visitFieldList func(list *ast.FieldList)
	visitFieldList = func(list *ast.FieldList) {
		if list == nil {
			return
		}
		for _, field := range list.List {
			visitType(field.Type)
		}
	}
	visitType = func(expr ast.Expr) {
		switch typed := expr.(type) {
		case nil:
			return
		case *ast.StarExpr:
			visitType(typed.X)
		case *ast.ParenExpr:
			visitType(typed.X)
		case *ast.SelectorExpr:
			if typed.Sel != nil && typed.Sel.Name == "Service" {
				if ident, ok := typed.X.(*ast.Ident); ok && aliases[ident.Name] {
					record(typed, ident.Name+".Service")
					return
				}
			}
			visitType(typed.X)
		case *ast.Ident:
			if typed.Name == "Service" {
				record(typed, "Service (the package alias of core.Service)")
			}
		case *ast.ArrayType:
			visitType(typed.Elt)
		case *ast.Ellipsis:
			visitType(typed.Elt)
		case *ast.MapType:
			visitType(typed.Key)
			visitType(typed.Value)
		case *ast.ChanType:
			visitType(typed.Value)
		case *ast.FuncType:
			visitFieldList(typed.Params)
			visitFieldList(typed.Results)
		case *ast.StructType:
			visitFieldList(typed.Fields)
		case *ast.InterfaceType:
			visitFieldList(typed.Methods)
		case *ast.IndexExpr:
			visitType(typed.X)
		case *ast.IndexListExpr:
			visitType(typed.X)
		}
	}

	for _, decl := range file.Decls {
		switch d := decl.(type) {
		case *ast.FuncDecl:
			visitType(d.Type)
		case *ast.GenDecl:
			for _, spec := range d.Specs {
				switch s := spec.(type) {
				case *ast.TypeSpec:
					visitType(s.Type)
				case *ast.ValueSpec:
					visitType(s.Type)
				}
			}
		}
	}
	ast.Inspect(file, func(node ast.Node) bool {
		switch n := node.(type) {
		case *ast.CompositeLit:
			visitType(n.Type)
		case *ast.TypeAssertExpr:
			visitType(n.Type)
		case *ast.CallExpr:
			// A conversion such as (*core.Service)(nil).
			if paren, ok := n.Fun.(*ast.ParenExpr); ok {
				visitType(paren.X)
			}
		}
		return true
	})
	return references
}

// aggregateReferencesInFile parses one file and returns its aggregate references.
func aggregateReferencesInFile(t *testing.T, path string) []aggregateReference {
	t.Helper()
	fset := token.NewFileSet()
	file, err := parser.ParseFile(fset, path, nil, 0)
	if err != nil {
		t.Fatalf("parse %s: %v", path, err)
	}
	return collectAggregateReferences(fset, path, file)
}

// TestTheTransportLayerHoldsNoAggregateType is the acceptance assertion of
// ticket #266: no non-test file of this package may name the aggregate
// concrete type, except the surfaces listed in aggregateHolders, which are
// owned by the tickets that migrate them.
func TestTheTransportLayerHoldsNoAggregateType(t *testing.T) {
	directory := packageDirectory(t)
	entries, err := os.ReadDir(directory)
	if err != nil {
		t.Fatalf("read package directory: %v", err)
	}
	seen := map[string]bool{}
	for _, entry := range entries {
		name := entry.Name()
		if entry.IsDir() || !strings.HasSuffix(name, ".go") || strings.HasSuffix(name, "_test.go") {
			continue
		}
		seen[name] = true
		references := aggregateReferencesInFile(t, filepath.Join(directory, name))
		reason, allowed := aggregateHolders[name]
		if len(references) == 0 {
			if allowed {
				t.Errorf("%s is whitelisted (%s) but no longer names the aggregate; delete the whitelist entry in the same change", name, reason)
			}
			continue
		}
		if allowed {
			continue
		}
		for _, reference := range references {
			t.Errorf("%s:%d still names the aggregate concrete type (%s); the transport layer must reach the workspace through narrow ports. %s",
				name, reference.line, reference.text, aggregateWhitelistNote())
		}
	}
	for name := range aggregateHolders {
		if !seen[name] {
			t.Errorf("the whitelist names %s, which is not a non-test file of this package; the whitelist must list real files only", name)
		}
	}
}

// TestTheNarrowPortSurfacesHoldNoAggregateType covers the three surfaces of
// ticket #266 by name.
//
// The template governance runtime and the selected rewrite reached the
// aggregate through their own field; both now consume narrow ports. The
// review anchor surface already consumed narrow ports and needs no code
// change: this assertion is what proves it compliant.
func TestTheNarrowPortSurfacesHoldNoAggregateType(t *testing.T) {
	directory := packageDirectory(t)
	for _, name := range []string{"template_governance_runtime.go", "selected_rewrite.go", "review_anchor_http.go"} {
		if reason, allowed := aggregateHolders[name]; allowed {
			t.Errorf("%s is whitelisted (%s); a surface that consumes narrow ports must never be", name, reason)
		}
		for _, reference := range aggregateReferencesInFile(t, filepath.Join(directory, name)) {
			t.Errorf("%s:%d still names the aggregate concrete type (%s); a surface that consumes narrow ports must never do so",
				name, reference.line, reference.text)
		}
	}
}

// packageDirectory returns the directory of this source file.
func packageDirectory(t *testing.T) string {
	t.Helper()
	_, file, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("cannot locate the test source")
	}
	return filepath.Dir(file)
}
