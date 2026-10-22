package workspacecore

// The single-source guard for the Owner-availability judgement.
//
// The judgement answers one question -- may the Owner still act -- and it has
// exactly one implementation. A second copy of the same conditions is a second
// answer to the same question, and the two answers drift apart: that is
// exactly what the inline copy in the owner transfer request used to be.
//
// So the guard checks the shape of that drift instead of trusting every
// reader to notice it. It reads the Go sources under internal/lingdoc/ with
// go/parser and go/ast -- not with a regular expression over a whole file --
// and reports every function that both asks whether a membership is active
// and talks about the Owner. That combination is what an inline copy of the
// judgement looks like: it re-derives "is the Owner still able to act" on its
// own, beside the one place that is supposed to answer it.
//
// Two details keep the shape honest.
//
// The guard reads a *named* function's own statements. A closure belongs to
// the operation runner's transaction callback, not to the judgement of the
// function that hands it over, and a scattered second copy of the judgement
// is a named function -- the old owner_transfer_proof.go had named functions.
//
// The owner role has to be *asked about*, not merely spelled: the guard looks
// for the calls that say who the Owner is and for a comparison of a member's
// role field against the literal "owner". A function that happens to mention
// the owner role in another sense -- naming the role it is about to assign,
// say -- is not re-deriving the judgement and is left alone.
//
// The two functions that may hold the shape are named below, each with the
// reason it is not a second answer. The exception is written down rather than
// left mysterious, so a reader can judge it instead of just accepting it.

import (
	"go/ast"
	"go/parser"
	"go/token"
	"io/fs"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"testing"
)

// allowedOwnerAvailabilityCopies are the functions that may hold the shape of
// an inline copy, each with the reason it is not one.
//
// The key is "<file>:<function>" relative to internal/lingdoc/, so a file that
// is renamed or moved makes the allowance stop matching instead of silently
// exempting a different function.
var allowedOwnerAvailabilityCopies = map[string]string{
	"workspacecore/owner_unavailable.go:ownerCurrentlyAvailable": "the one implementation of the judgement",
	"workspacecore/service_facade.go:legacyAuthorizeProject": "the legacy authorization path: it answers " +
		"\"may this actor act\", not \"may the Owner still act\", and it does not read the " +
		"Owner's own availability",
}

// ownerRoleCalls are the calls that ask about the Owner's role.
//
// The package-private "governanceRole" is deliberately not among them: it is
// the workspace's own helper, and a function that asks it about a member it
// already holds is asking about that member, not about the Owner.
var ownerRoleCalls = map[string]bool{
	"projectOwner":   true,
	"OwnerMayAct":    true,
	"ProjectOwner":   true,
	"GovernanceRole": true,
}

// ownerAvailabilityCopy is one function that holds the shape of an inline copy
// of the Owner-availability judgement.
type ownerAvailabilityCopy struct {
	file string
	fn   string
}

// TestOwnerAvailabilityJudgementHasOneImplementation is the guard itself: the
// judgement has one implementation, and a scattered second copy is rejected
// here rather than in a review that may not read every file.
func TestOwnerAvailabilityJudgementHasOneImplementation(t *testing.T) {
	copies := findOwnerAvailabilityCopies(t)
	for _, copy := range copies {
		key := copy.file + ":" + copy.fn
		if reason, allowed := allowedOwnerAvailabilityCopies[key]; allowed {
			t.Logf("%s is allowed to hold the shape: %s", key, reason)
			continue
		}
		t.Errorf("%s holds the shape of an inline copy of the Owner-availability judgement: "+
			"it asks whether a membership is active and talks about the Owner, so it re-derives "+
			"\"is the Owner still able to act\" on its own. Delete it and ask owner_unavailable.go "+
			"instead (OwnerAuthorityOf for the fact, JudgeOwnerAction for the answer), or -- if it "+
			"is genuinely a different question -- state the reason in "+
			"allowedOwnerAvailabilityCopies so the exemption is written down.",
			key)
	}
}

// findOwnerAvailabilityCopies reads every Go source under internal/lingdoc/ and
// returns the functions that hold the shape of an inline copy of the
// Owner-availability judgement.
//
// The package directory is found with runtime.Caller, so the guard needs no
// hard-coded path and keeps working wherever the tree is checked out.
func findOwnerAvailabilityCopies(t *testing.T) []ownerAvailabilityCopy {
	t.Helper()
	_, thisFile, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("cannot locate the guard's own file")
	}
	// internal/lingdoc/workspacecore -> internal/lingdoc
	lingdocRoot := filepath.Dir(filepath.Dir(thisFile))

	var copies []ownerAvailabilityCopy
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
			if !callsActiveMember(function.Body) || !mentionsTheOwner(function.Body) {
				continue
			}
			copies = append(copies, ownerAvailabilityCopy{file: relative, fn: function.Name.Name})
		}
		return nil
	})
	if walkErr != nil {
		t.Fatalf("read the LingDoc sources: %v", walkErr)
	}
	return copies
}

// callsActiveMember reports whether the function asks the transaction whether
// a membership is active.
func callsActiveMember(body *ast.BlockStmt) bool {
	found := false
	ast.Inspect(body, func(node ast.Node) bool {
		if found {
			return false
		}
		switch typed := node.(type) {
		case *ast.FuncLit:
			// A closure is the runner's transaction callback, not this
			// function's own judgement.
			return false
		case *ast.CallExpr:
			switch fun := typed.Fun.(type) {
			case *ast.SelectorExpr:
				if fun.Sel.Name == "ActiveMember" {
					found = true
				}
			case *ast.Ident:
				if fun.Name == "ActiveMember" {
					found = true
				}
			}
		}
		return !found
	})
	return found
}

// mentionsTheOwner reports whether the function asks about the Owner role: it
// either calls the project's owner predicate, or it compares a member's role
// field against the literal "owner".
func mentionsTheOwner(body *ast.BlockStmt) bool {
	found := false
	ast.Inspect(body, func(node ast.Node) bool {
		if found {
			return false
		}
		switch typed := node.(type) {
		case *ast.FuncLit:
			return false
		case *ast.CallExpr:
			switch fun := typed.Fun.(type) {
			case *ast.Ident:
				if ownerRoleCalls[fun.Name] {
					found = true
				}
			case *ast.SelectorExpr:
				if ownerRoleCalls[fun.Sel.Name] {
					found = true
				}
			}
		case *ast.BinaryExpr:
			if typed.Op != token.EQL && typed.Op != token.NEQ {
				break
			}
			if isOwnerRoleLiteral(typed.X) && isMemberRoleField(typed.Y) ||
				isOwnerRoleLiteral(typed.Y) && isMemberRoleField(typed.X) {
				found = true
			}
		}
		return !found
	})
	return found
}

// isOwnerRoleLiteral reports whether the expression is the literal "owner".
func isOwnerRoleLiteral(expression ast.Expr) bool {
	literal, ok := expression.(*ast.BasicLit)
	if !ok || literal.Kind != token.STRING {
		return false
	}
	value, err := strconv.Unquote(literal.Value)
	return err == nil && value == "owner"
}

// isMemberRoleField reports whether the expression reads a member's role.
func isMemberRoleField(expression ast.Expr) bool {
	selector, ok := expression.(*ast.SelectorExpr)
	return ok && (selector.Sel.Name == "Role" || selector.Sel.Name == "GovernanceRole")
}

// TestOwnerAvailabilitySingleSourceGuardFindsAnInlineCopy proves the guard
// bites: the shape it looks for is the shape it finds.
//
// It is the guard's own detector, run against a function that holds the shape
// on purpose. Without this the guard could be written so that it never
// matches anything, and it would still be green.
func TestOwnerAvailabilitySingleSourceGuardFindsAnInlineCopy(t *testing.T) {
	source := `package workspacecore

func inlineOwnerAvailabilityCopy(tx Transaction, p Project, actor Actor) bool {
	if !projectOwner(p, actor.UserID) {
		return false
	}
	active, err := tx.ActiveMember(actor)
	if err != nil {
		return false
	}
	return active
}
`
	file, err := parser.ParseFile(token.NewFileSet(), "inline_copy.go", source, parser.SkipObjectResolution)
	if err != nil {
		t.Fatalf("parse the synthetic copy: %v", err)
	}
	function, ok := file.Decls[0].(*ast.FuncDecl)
	if !ok {
		t.Fatal("the synthetic copy is not a function")
	}
	if !callsActiveMember(function.Body) || !mentionsTheOwner(function.Body) {
		t.Fatal("the guard does not recognise the shape of an inline copy")
	}
}
