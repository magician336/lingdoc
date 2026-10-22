package membership

import (
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"runtime"
	"sort"
	"strings"
	"testing"

	core "github.com/Tencent/WeKnora/internal/lingdoc/workspacecore"
)

// The two ports in this package describe the member and owner operations.
// Their method sets and signatures must match the ones this package
// implements, character for character apart from the package qualifier this
// package has to write.
//
// The promise is pinned here because the implementation is here: the service
// in this package is what answers these operations, and the aggregate is not.
var expectedPorts = map[string][]string{
	"Application": {
		"AcceptOwnerTransferResult",
		"RequestOwnerTransferResult",
		"SaveMembersResult",
		"TransferOwnerResult",
	},
	"MemberLifecycleApplication": {
		"AcceptProjectInvitation",
		"ChangeMemberState",
		"EffectivePermission",
		"InviteProjectMember",
		"ListMyProjectInvitations",
		"ListOwnerTransfers",
		"ListProjectInvitations",
		"OwnerAvailability",
		"ProjectMemberDirectory",
		"RejectOwnerTransfer",
		"SaveProjectAPIKeyGrant",
		"SetMemberAssignment",
	},
}

// TestTheMemberAndOwnerPortsMatchTheServiceVerbatim is the promise that
// moving the port did not restate it.
//
// A port may only narrow: every method it names must exist on the service
// with the very same signature. If the port restated the operation, the two
// would drift apart silently - the port would describe a contract the
// service does not actually offer.
func TestTheMemberAndOwnerPortsMatchTheServiceVerbatim(t *testing.T) {
	directory := packageDirectory(t)
	declared := portMethodSignatures(t, filepath.Join(directory, "ports.go"))
	service := serviceMethodSignatures(t, directory)

	for name, expected := range expectedPorts {
		methods, ok := declared[name]
		if !ok {
			t.Errorf("the port %s is not declared in ports.go", name)
			continue
		}
		got := make([]string, 0, len(methods))
		for method := range methods {
			got = append(got, method)
		}
		sort.Strings(got)
		want := append([]string(nil), expected...)
		sort.Strings(want)
		if strings.Join(got, ",") != strings.Join(want, ",") {
			t.Errorf("the port %s carries %v, want %v; a port only changes when the context gains or loses an operation, and that decision has to be written down", name, got, want)
		}
		for _, method := range want {
			signature, ok := methods[method]
			if !ok {
				continue
			}
			origin, ok := service[method]
			if !ok {
				t.Errorf(
					"%s.%s is not a service operation any more; drop it from the port or move the operation back",
					name, method,
				)
				continue
			}
			if signature != origin {
				t.Errorf(
					"%s.%s is declared as %q, the service declares %q; a narrow port may only narrow, never restate",
					name, method, signature, origin,
				)
			}
		}
	}

	for name := range declared {
		if _, ok := expectedPorts[name]; !ok {
			t.Errorf("the port %s is declared but not recorded in expectedPorts", name)
		}
	}
}

// TestTheAggregateNoLongerImplementsTheMemberAndOwnerPorts is the promise
// that the context actually left the aggregate.
//
// Go cannot say "this type does not satisfy this interface" at compile time,
// so the promise is made at run time: the aggregate is handed to the ports as
// an `any`, and the ports must refuse it.
//
// This is the half that a port declaration cannot carry. A port may name
// operations the aggregate does not have, and the aggregate may still answer
// them; only this assertion says the answer moved.
func TestTheAggregateNoLongerImplementsTheMemberAndOwnerPorts(t *testing.T) {
	var aggregate any = (*core.Service)(nil)

	if _, ok := aggregate.(Application); ok {
		t.Error("the aggregate still implements the member and owner port; the context has not left it")
	}
	if _, ok := aggregate.(MemberLifecycleApplication); ok {
		t.Error("the aggregate still implements the member and owner lifecycle port; the context has not left it")
	}
}

// serviceMethodSignatures reads the service's method signatures from this
// package's source, so the comparison is against the code that exists rather
// than against a compiled type.
func serviceMethodSignatures(t *testing.T, directory string) map[string]string {
	t.Helper()
	signatures := map[string]string{}
	entries, err := os.ReadDir(directory)
	if err != nil {
		t.Fatalf("read the package: %v", err)
	}
	for _, entry := range entries {
		name := entry.Name()
		if entry.IsDir() || !strings.HasSuffix(name, ".go") || strings.HasSuffix(name, "_test.go") {
			continue
		}
		file, err := parser.ParseFile(token.NewFileSet(), filepath.Join(directory, name), nil, 0)
		if err != nil {
			t.Fatalf("parse %s: %v", name, err)
		}
		for _, declaration := range file.Decls {
			function, ok := declaration.(*ast.FuncDecl)
			if !ok || function.Recv == nil || len(function.Recv.List) != 1 {
				continue
			}
			receiver, ok := function.Recv.List[0].Type.(*ast.StarExpr)
			if !ok {
				continue
			}
			ident, ok := receiver.X.(*ast.Ident)
			if !ok || ident.Name != "Service" {
				continue
			}
			signatures[function.Name.Name] = normalizeFuncType(function.Type)
		}
	}
	return signatures
}

// portMethodSignatures reads the method sets of the declared ports.
func portMethodSignatures(t *testing.T, path string) map[string]map[string]string {
	t.Helper()
	file, err := parser.ParseFile(token.NewFileSet(), path, nil, 0)
	if err != nil {
		t.Fatalf("parse %s: %v", path, err)
	}
	ports := map[string]map[string]string{}
	for _, declaration := range file.Decls {
		generic, ok := declaration.(*ast.GenDecl)
		if !ok || generic.Tok != token.TYPE {
			continue
		}
		for _, specification := range generic.Specs {
			typed, ok := specification.(*ast.TypeSpec)
			if !ok {
				continue
			}
			iface, ok := typed.Type.(*ast.InterfaceType)
			if !ok {
				continue
			}
			methods := map[string]string{}
			for _, element := range iface.Methods.List {
				if len(element.Names) == 0 {
					continue
				}
				function, ok := element.Type.(*ast.FuncType)
				if !ok {
					continue
				}
				methods[element.Names[0].Name] = normalizeFuncType(function)
			}
			ports[typed.Name.Name] = methods
		}
	}
	return ports
}

// normalizeFuncType renders a function's parameter and result text with the
// whitespace collapsed and the package qualifier removed, so that this
// package writing core.Actor and the service writing core.Actor are the same
// contract.
func normalizeFuncType(function *ast.FuncType) string {
	parts := []string{}
	if function.Params != nil {
		parts = append(parts, renderFieldList(function.Params))
	}
	if function.Results != nil {
		parts = append(parts, renderFieldList(function.Results))
	}
	return strings.Join(parts, " -> ")
}

// renderFieldList renders the types of the fields, without the parameter
// names: the names are not part of the contract, and this package writes
// unnamed parameters where the service names them.
func renderFieldList(list *ast.FieldList) string {
	parts := []string{}
	for _, field := range list.List {
		typ := renderType(field.Type)
		if len(field.Names) == 0 {
			parts = append(parts, typ)
			continue
		}
		for range field.Names {
			parts = append(parts, typ)
		}
	}
	return "(" + strings.Join(parts, ", ") + ")"
}

func renderType(expression ast.Expr) string {
	switch typed := expression.(type) {
	case *ast.Ident:
		return typed.Name
	case *ast.StarExpr:
		return "*" + renderType(typed.X)
	case *ast.SelectorExpr:
		// This package writes core.Actor where the service writes core.Actor.
		// The qualifier is not part of the contract, so it is dropped.
		if ident, ok := typed.X.(*ast.Ident); ok && ident.Name == "core" {
			return typed.Sel.Name
		}
		return renderType(typed.X) + "." + typed.Sel.Name
	case *ast.ArrayType:
		return "[]" + renderType(typed.Elt)
	case *ast.MapType:
		return "map[" + renderType(typed.Key) + "]" + renderType(typed.Value)
	case *ast.InterfaceType:
		if typed.Methods == nil || len(typed.Methods.List) == 0 {
			return "any"
		}
		return "interface{...}"
	case *ast.Ellipsis:
		return "..." + renderType(typed.Elt)
	case *ast.FuncType:
		return "func" + normalizeFuncType(typed)
	case *ast.StructType:
		return "struct{...}"
	default:
		return "?"
	}
}

func packageDirectory(t *testing.T) string {
	t.Helper()
	_, file, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("cannot locate the test source")
	}
	return filepath.Dir(file)
}
