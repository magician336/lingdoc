package workspacecore

// The guard for the Owner-only entry registry.
//
// The registry in owner_capability_registry.go is the only place that knows
// which entries are Owner-only and which capability the interface reads each
// of them by. The risk is not that the table is wrong today: it is that the
// code grows an Owner-only entry and the table does not. Then the freeze of
// that entry is unreadable, and the interface shows "you may not" where the
// truth is "the Owner is gone" -- exactly the confusion the freeze exists to
// prevent.
//
// So the guard reads the Go sources under internal/lingdoc/ with go/parser
// and go/ast -- not with a regular expression over a whole file -- and finds
// every function that takes the Owner-only decision. Each of them has to be
// in the registry, and each registry row has to be one of them: the two lists
// may not drift apart in either direction.
//
// One detail differs from the single-source guard next door, and it is
// deliberate. That guard skips closures, because a closure there is the
// operation runner's transaction callback and not the judgement of the
// function that hands it over. Here a closure *is* the entry's body -- the
// Owner-only decision is taken inside the operation -- so a closure is
// followed.

import (
	"go/ast"
	"go/parser"
	"go/token"
	"io/fs"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
	"testing"
)

// ownerOnlyGateCalls are the calls that take the Owner-only decision.
//
// A call to one of them is what makes the enclosing function an Owner-only
// entry: the freeze is enforced there, so that is where the entry lives.
//
// The exported doorway itself is not among them: it is the doorway, not an
// entry, and the entries that reach it are the ones that matter.
var ownerOnlyGateCalls = map[string]bool{
	"requireOwnerDecision":       true,
	"ownerDecisionInTransaction": true,
}

// ownerOnlyEntryPoint is one function that takes the Owner-only decision.
type ownerOnlyEntryPoint struct {
	file string
	fn   string
}

// TestOwnerOnlyEntryRegistryCoversEveryOwnerOnlyEntry is the guard itself.
//
// Every function that takes the Owner-only decision has to be in the
// registry, because the registry is what says which capability the interface
// reads that entry's freeze by. An entry that is not in it cannot be
// reported as frozen, and the interface falls back to "you may not".
//
// Every registry row has to be a function that takes the decision too,
// because a row that names an entry that does not exist sends the interface
// to a capability that no entry uses.
func TestOwnerOnlyEntryRegistryCoversEveryOwnerOnlyEntry(t *testing.T) {
	found := findOwnerOnlyEntryPoints(t)

	registered := make([]string, 0, len(ownerOnlyEntries))
	for _, entry := range ownerOnlyEntries {
		registered = append(registered, entry.Name)
	}
	for _, problem := range compareOwnerOnlyEntries(registered, found) {
		t.Error(problem)
	}
}

// compareOwnerOnlyEntries reports every way the code's Owner-only entries and
// the registry disagree.
//
// The two directions are reported separately on purpose. An entry in the
// code that is not in the registry cannot be reported as frozen, so the
// interface shows "you may not" where the truth is "the Owner is gone". A
// row in the registry that is not in the code sends the interface to a
// capability that no entry uses.
func compareOwnerOnlyEntries(registered []string, found []ownerOnlyEntryPoint) []string {
	var problems []string

	registeredSet := map[string]bool{}
	for _, name := range registered {
		if registeredSet[name] {
			problems = append(problems, "the registry lists "+name+" twice")
		}
		registeredSet[name] = true
	}

	seen := map[string]bool{}
	for _, point := range found {
		if seen[point.fn] {
			continue
		}
		seen[point.fn] = true
		if !registeredSet[point.fn] {
			problems = append(problems, point.file+":"+point.fn+
				" takes the Owner-only decision but is not in the registry: "+
				"its freeze would be unreadable, because the interface does not know "+
				"which capability to read it by. Add it to owner_capability_registry.go "+
				"in the same change.")
		}
	}
	for _, name := range registered {
		if !seen[name] {
			problems = append(problems, "the registry lists "+name+
				" but no source takes the Owner-only decision there: the entry does not "+
				"exist any more, and the registry would send the interface to a capability "+
				"that no entry uses.")
		}
	}
	return problems
}

// TestOwnerOnlyCapabilityComesFromTheRegistry is the derivation: the answer
// follows the table, and the table is the enumeration.
//
// The list is written out here so that a change to it is visible: a new
// Owner-only entry capability has to show up here as well as in the
// registry, because the read outlet has to answer for every one of them.
func TestOwnerOnlyCapabilityComesFromTheRegistry(t *testing.T) {
	capabilities := OwnerOnlyEntryCapabilities()
	want := []string{string(AuthorizationManage), string(AuthorizationOpsControl)}
	if !slices.Equal(capabilities, want) {
		t.Fatalf("Owner-only entry capabilities = %v, want %v", capabilities, want)
	}
	for _, capability := range capabilities {
		if !OwnerOnlyCapability(capability) {
			t.Errorf("registered capability %q is not reported as Owner-only", capability)
		}
	}
	for _, entry := range ownerOnlyEntries {
		for _, capability := range entry.ReadCapabilities {
			if !OwnerOnlyCapability(capability) {
				t.Errorf("entry %s is read by %q, but OwnerOnlyCapability says it is not "+
					"an Owner-only capability", entry.Name, capability)
			}
		}
	}
}

// TestOwnerOnlyCapabilityKeepsOrdinaryRefusalsOrdinary is the other half of
// the contract: a refusal that has nothing to do with the freeze keeps the
// reason it has always carried.
//
// "write" is the capability the activation authorizes with, and it is a
// capability every member may hold. Treating it as Owner-only would swallow
// the existing "capability_or_scope_denied" meaning: a member refused a write
// that is outside their scope would be told the project is frozen.
func TestOwnerOnlyCapabilityKeepsOrdinaryRefusalsOrdinary(t *testing.T) {
	ordinary := []string{
		string(AuthorizationWrite),
		string(AuthorizationRead),
		"review",
		"evidence-write",
		"confirm",
		"change-assess",
		"issue-resolve",
	}
	for _, capability := range ordinary {
		if OwnerOnlyCapability(capability) {
			t.Errorf("ordinary capability %q is reported as Owner-only; a refusal of it "+
				"would be answered with the freeze instead of the actor's own lack of "+
				"authority", capability)
		}
	}
}

// TestOwnerOnlyEntryGuardFindsAnUnregisteredEntry proves the guard bites: the
// shape it looks for is the shape it finds.
//
// Without this the guard could be written so that it never matches anything,
// and it would still be green.
func TestOwnerOnlyEntryGuardFindsAnUnregisteredEntry(t *testing.T) {
	source := `package workspacecore

func unregisteredOwnerOnlyEntry(s *Service, ctx context.Context, actor Actor, projectID string) error {
	return s.requireOwnerDecision(ctx, actor, projectID)
}
`
	file, err := parser.ParseFile(token.NewFileSet(), "unregistered.go", source, parser.SkipObjectResolution)
	if err != nil {
		t.Fatalf("parse the synthetic entry: %v", err)
	}
	decl, ok := file.Decls[0].(*ast.FuncDecl)
	if !ok {
		t.Fatal("the synthetic entry is not a function")
	}
	if !takesOwnerOnlyDecision(decl.Body) {
		t.Fatal("the guard does not recognise the shape of an Owner-only entry")
	}

	// The comparison has to report it, not only the detector.
	problems := compareOwnerOnlyEntries(
		[]string{"ActivateProject"},
		[]ownerOnlyEntryPoint{{file: "unregistered.go", fn: "unregisteredOwnerOnlyEntry"}},
	)
	if len(problems) == 0 {
		t.Fatal("the guard accepts an Owner-only entry that is not in the registry")
	}
	if !strings.Contains(problems[0], "unregisteredOwnerOnlyEntry") {
		t.Fatalf("the guard reported %q, want the unregistered entry named", problems[0])
	}
}

// findOwnerOnlyEntryPoints reads every Go source under internal/lingdoc/ and
// returns the functions that take the Owner-only decision.
//
// The package directory is found with runtime.Caller, so the guard needs no
// hard-coded path and keeps working wherever the tree is checked out.
func findOwnerOnlyEntryPoints(t *testing.T) []ownerOnlyEntryPoint {
	t.Helper()
	_, thisFile, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("cannot locate the guard's own file")
	}
	// internal/lingdoc/workspacecore -> internal/lingdoc
	lingdocRoot := filepath.Dir(filepath.Dir(thisFile))

	var found []ownerOnlyEntryPoint
	walkErr := filepath.WalkDir(lingdocRoot, func(path string, entry fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if entry.IsDir() {
			return nil
		}
		if filepath.Ext(path) != ".go" || strings.HasSuffix(path, "_test.go") {
			return nil
		}
		file, err := parser.ParseFile(token.NewFileSet(), path, nil, parser.SkipObjectResolution)
		if err != nil {
			return err
		}
		relative, relErr := filepath.Rel(lingdocRoot, path)
		if relErr != nil {
			relative = path
		}
		relative = filepath.ToSlash(relative)
		for _, decl := range file.Decls {
			function, ok := decl.(*ast.FuncDecl)
			if !ok || function.Body == nil {
				continue
			}
			if !takesOwnerOnlyDecision(function.Body) {
				continue
			}
			found = append(found, ownerOnlyEntryPoint{file: relative, fn: function.Name.Name})
		}
		return nil
	})
	if walkErr != nil {
		t.Fatalf("read the LingDoc sources: %v", walkErr)
	}
	return found
}

// takesOwnerOnlyDecision reports whether the function takes the Owner-only
// decision.
func takesOwnerOnlyDecision(body *ast.BlockStmt) bool {
	found := false
	ast.Inspect(body, func(node ast.Node) bool {
		if found {
			return false
		}
		call, ok := node.(*ast.CallExpr)
		if !ok {
			return true
		}
		switch fun := call.Fun.(type) {
		case *ast.SelectorExpr:
			if ownerOnlyGateCalls[fun.Sel.Name] {
				found = true
			}
		case *ast.Ident:
			if ownerOnlyGateCalls[fun.Name] {
				found = true
			}
		}
		return !found
	})
	return found
}
