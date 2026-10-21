package workspace

import (
	"go/ast"
	"go/parser"
	"go/token"
	"path/filepath"
	"runtime"
	"sort"
	"strconv"
	"strings"
	"testing"
)

// backdoorCoreImportPath is the aggregate's own package. The transport
// imports it as `core` in most files.
const backdoorCoreImportPath = "github.com/Tencent/WeKnora/internal/lingdoc/workspacecore"

// TestTheTransportDoesNotAssertItsWayBackToTheAggregate is the promise that
// the transport layer cannot reach the aggregate's operations by asking a
// value at run time.
//
// The transport holds narrow ports, and that is what bounds its reach. It
// used to also *discover* the optional capabilities with `value.(SomeInterface)`,
// and the value it asked about was the aggregate: the assertion could hand
// back any operation the asserted interface named, so the narrow port's
// declaration was not the limit of what the transport could call.
//
// The rule this test pins:
//
//	a run-time assertion in the transport must not target an interface whose
//	method set is a subset of the aggregate's.
//
// Such an interface is a way back to the aggregate. An assertion that targets
// the transport's own capability - DeliveryCurrentness, TemplateCheckApplication,
// IssueDispositionApplication - is not a way back: those methods are not the
// aggregate's, so the assertion can only answer "does this value have that
// capability", never "here is more than the port declares".
func TestTheTransportDoesNotAssertItsWayBackToTheAggregate(t *testing.T) {
	root := backdoorRepositoryRoot(t)
	coreDirectory := filepath.Join(root, "internal", "lingdoc", "workspacecore")
	transportDirectory := filepath.Join(root, "internal", "lingdoc", "workspace")

	aggregate := backdoorAggregateOperations(t, coreDirectory)
	core := backdoorReadPackage(t, coreDirectory, nil)
	transport := backdoorReadPackage(t, transportDirectory, core)

	for _, path := range backdoorSourceFiles(t, transportDirectory, false) {
		fset := token.NewFileSet()
		file, err := parser.ParseFile(fset, path, nil, 0)
		if err != nil {
			t.Fatalf("parse %s: %v", path, err)
		}
		ast.Inspect(file, func(node ast.Node) bool {
			assertion, ok := node.(*ast.TypeAssertExpr)
			if !ok {
				return true
			}
			name, methods, status := backdoorAssertedMethodSet(assertion, transport, core)
			switch status {
			case backdoorTargetConcrete:
				// A concrete type: the assertion narrows, it cannot widen.
				return true
			case backdoorTargetUnreadable:
				t.Errorf("%s:%d asserts %s, whose method set this guard cannot read; "+
					"a guard that cannot see a target is not a guard",
					backdoorRelative(t, root, path), fset.Position(assertion.Pos()).Line, name)
				return true
			}
			shared := []string{}
			for _, method := range methods {
				if aggregate[method] {
					shared = append(shared, method)
				}
			}
			if len(shared) == 0 {
				return true
			}
			t.Errorf("%s:%d asserts %s, whose method set is a subset of the aggregate's (%s); "+
				"the assertion can hand the transport back an aggregate operation "+
				"that the narrow port does not declare",
				backdoorRelative(t, root, path), fset.Position(assertion.Pos()).Line, name, strings.Join(shared, ", "))
			return true
		})
	}
}

// backdoorTargetStatus says what an assertion target turned out to be.
const (
	// backdoorTargetConcrete is a concrete type: the assertion narrows the
	// value, it cannot widen it.
	backdoorTargetConcrete = iota
	// backdoorTargetInterface is an interface whose method set this guard
	// read.
	backdoorTargetInterface
	// backdoorTargetUnreadable is an interface whose method set this guard
	// could not work out.
	backdoorTargetUnreadable
)

// backdoorAssertedMethodSet returns the method set of the interface an
// assertion targets.
//
// A name that is declared as an interface but whose method set cannot be
// read is reported as unreadable rather than skipped: a guard that cannot
// see a target is not a guard.
func backdoorAssertedMethodSet(
	assertion *ast.TypeAssertExpr,
	transport *backdoorPackage,
	core *backdoorPackage,
) (string, []string, int) {
	switch target := assertion.Type.(type) {
	case *ast.Ident:
		declared, ok := transport.types[target.Name]
		if !ok {
			return target.Name, nil, backdoorTargetConcrete
		}
		if declared.aliasOf != "" {
			methods, ok := transport.methodsOfAlias(declared.aliasOf, core)
			if !ok {
				return target.Name, nil, backdoorTargetUnreadable
			}
			return target.Name, methods, backdoorTargetInterface
		}
		if !declared.isInterface {
			return target.Name, nil, backdoorTargetConcrete
		}
		methods, ok := transport.methodsOf(target.Name, core)
		if !ok {
			return target.Name, nil, backdoorTargetUnreadable
		}
		return target.Name, methods, backdoorTargetInterface
	case *ast.SelectorExpr:
		qualifier, ok := target.X.(*ast.Ident)
		if !ok || !core.coreAliases[qualifier.Name] {
			return "", nil, backdoorTargetConcrete
		}
		declared, found := core.types[target.Sel.Name]
		if !found {
			return "", nil, backdoorTargetConcrete
		}
		if !declared.isInterface {
			return qualifier.Name + "." + target.Sel.Name, nil, backdoorTargetConcrete
		}
		methods, ok := core.methodsOf(target.Sel.Name, core)
		if !ok {
			return qualifier.Name + "." + target.Sel.Name, nil, backdoorTargetUnreadable
		}
		return qualifier.Name + "." + target.Sel.Name, methods, backdoorTargetInterface
	default:
		return "", nil, backdoorTargetConcrete
	}
}

// backdoorType is what a package declares about one of its type names.
type backdoorType struct {
	isInterface bool
	// aliasOf names the interface this type is an alias of, qualified by the
	// package it lives in when that is another package. Empty for a plain
	// declaration.
	aliasOf string
}

// backdoorPackage is one package's type declarations, read from its source.
type backdoorPackage struct {
	// types maps a declared name to what the package declares about it.
	types map[string]backdoorType
	// interfaces maps a declared interface to the elements of its body.
	interfaces map[string]*ast.InterfaceType
	// coreAliases is the set of local names the aggregate's own package is
	// imported under in this package's files.
	coreAliases map[string]bool
}

// backdoorReadPackage reads one package's type declarations.
//
// The second argument is the aggregate's own package, which a package may
// refer to; it is nil for the aggregate's package itself.
func backdoorReadPackage(t *testing.T, directory string, core *backdoorPackage) *backdoorPackage {
	t.Helper()
	pkg := &backdoorPackage{
		types:       map[string]backdoorType{},
		interfaces:  map[string]*ast.InterfaceType{},
		coreAliases: map[string]bool{},
	}
	for _, path := range backdoorSourceFiles(t, directory, true) {
		file, err := parser.ParseFile(token.NewFileSet(), path, nil, 0)
		if err != nil {
			t.Fatalf("parse %s: %v", path, err)
		}
		for alias := range backdoorCoreAliases(file) {
			pkg.coreAliases[alias] = true
		}
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
				if typed.Assign != 0 {
					pkg.types[typed.Name.Name] = backdoorType{aliasOf: backdoorAliasTarget(typed, pkg.coreAliases)}
					continue
				}
				iface, ok := typed.Type.(*ast.InterfaceType)
				if !ok {
					continue
				}
				pkg.types[typed.Name.Name] = backdoorType{isInterface: true}
				pkg.interfaces[typed.Name.Name] = iface
			}
		}
	}
	return pkg
}

// methodsOf returns the method names of an interface, including the ones it
// embeds.
func (p *backdoorPackage) methodsOf(name string, core *backdoorPackage) ([]string, bool) {
	return p.methodsOfNamed(name, core, map[string]bool{})
}

func (p *backdoorPackage) methodsOfNamed(name string, core *backdoorPackage, seen map[string]bool) ([]string, bool) {
	if seen[name] {
		return nil, false
	}
	seen[name] = true
	iface, ok := p.interfaces[name]
	if !ok {
		return nil, false
	}
	methods := []string{}
	for _, element := range iface.Methods.List {
		if len(element.Names) == 0 {
			// An embedded interface: its methods are part of this one.
			embedded, ok := element.Type.(*ast.Ident)
			if !ok {
				return nil, false
			}
			inner, resolved := p.methodsOfNamed(embedded.Name, core, seen)
			if !resolved {
				return nil, false
			}
			methods = append(methods, inner...)
			continue
		}
		methods = append(methods, element.Names[0].Name)
	}
	sort.Strings(methods)
	return methods, true
}

// methodsOfAlias follows a type alias to the interface it names.
func (p *backdoorPackage) methodsOfAlias(alias string, core *backdoorPackage) ([]string, bool) {
	qualifier, target, qualified := strings.Cut(alias, ".")
	if !qualified {
		return p.methodsOf(target, core)
	}
	if core == nil || !core.coreAliases[qualifier] {
		return nil, false
	}
	return core.methodsOf(target, core)
}

// backdoorAliasTarget returns the interface a type alias points at, qualified
// by the package it lives in when that is another package.
func backdoorAliasTarget(typed *ast.TypeSpec, coreAliases map[string]bool) string {
	switch target := typed.Type.(type) {
	case *ast.SelectorExpr:
		qualifier, ok := target.X.(*ast.Ident)
		if !ok || !coreAliases[qualifier.Name] {
			return ""
		}
		return qualifier.Name + "." + target.Sel.Name
	case *ast.Ident:
		return target.Name
	default:
		return ""
	}
}

// backdoorCoreAliases returns the local names the aggregate's own package is
// imported under in one file.
func backdoorCoreAliases(file *ast.File) map[string]bool {
	aliases := map[string]bool{}
	for _, imported := range file.Imports {
		value, err := strconv.Unquote(imported.Path.Value)
		if err != nil || value != backdoorCoreImportPath {
			continue
		}
		if imported.Name != nil {
			aliases[imported.Name.Name] = true
			continue
		}
		aliases["workspacecore"] = true
	}
	return aliases
}

// backdoorAggregateOperations reads the aggregate's method names from its
// own source, so the comparison is against the code that exists.
func backdoorAggregateOperations(t *testing.T, directory string) map[string]bool {
	t.Helper()
	operations := map[string]bool{}
	for _, path := range backdoorSourceFiles(t, directory, false) {
		file, err := parser.ParseFile(token.NewFileSet(), path, nil, 0)
		if err != nil {
			t.Fatalf("parse %s: %v", path, err)
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
			name, ok := receiver.X.(*ast.Ident)
			if !ok || name.Name != "Service" {
				continue
			}
			operations[function.Name.Name] = true
		}
	}
	return operations
}

// backdoorSourceFiles lists the Go files of a directory.
func backdoorSourceFiles(t *testing.T, directory string, withTests bool) []string {
	t.Helper()
	entries, err := filepath.Glob(filepath.Join(directory, "*.go"))
	if err != nil {
		t.Fatalf("list %s: %v", directory, err)
	}
	files := []string{}
	for _, path := range entries {
		if !withTests && strings.HasSuffix(path, "_test.go") {
			continue
		}
		files = append(files, path)
	}
	sort.Strings(files)
	return files
}

// backdoorRepositoryRoot returns the repository root, derived from this
// file's location (internal/lingdoc/workspace).
func backdoorRepositoryRoot(t *testing.T) string {
	t.Helper()
	directory := backdoorPackageDirectory(t)
	for range 3 {
		directory = filepath.Dir(directory)
	}
	return directory
}

// backdoorPackageDirectory returns the directory of this source file.
func backdoorPackageDirectory(t *testing.T) string {
	t.Helper()
	_, file, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("cannot locate the test source")
	}
	return filepath.Dir(file)
}

// backdoorRelative renders a path relative to the repository root, so the
// failure message points at a file the reader can open.
func backdoorRelative(t *testing.T, root, path string) string {
	t.Helper()
	relative, err := filepath.Rel(root, path)
	if err != nil {
		return path
	}
	return relative
}
