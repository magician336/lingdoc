package workspacecore

import (
	"go/ast"
	"go/parser"
	"go/token"
	"path/filepath"
	"runtime"
	"testing"
)

// declaredGates pins what the research domain declares at every human
// decision gate.
//
// The baseline enforced the gate inside each method, so the requirement was
// invisible at the call: the only place it showed up was the method body. The
// shared pipeline needs the caller to declare it instead, and a declaration is
// only as good as the promise that it matches what the baseline did.
//
// This table is that promise. It was read mechanically from the baseline
// (28a76a5): for every call site, whether the method required the gate, and
// under which condition. Changing a declaration now fails this test until the
// change is acknowledged here.
var declaredGates = map[string]string{
	"saveResearchTask":       "NotRequired",
	"approveResearchTask":    "Required",
	"controlResearchTask":    "NotRequired",
	"runResearchTask":        "NotRequired",
	"createImportCandidate":  "NotRequired",
	"confirmImportCandidate": "Required",
	"rejectImportCandidate":  "Required",
	"saveSourceAnchor":       "Required",
	"saveResearchClaim":      "Required",
	"saveEvidenceCard":       "Required",
	"confirmEvidenceCard":    "Required",
	"reviewEvidenceCard":     "Required",
	"invalidateEvidence":     "NotRequired",
	"createAgentSession":     "NotRequired",
	"sendAgentTurn":          "conditional",
	"createActionProposal":   "NotRequired",
	"confirmActionProposal":  "Required",
	"rejectActionProposal":   "Required",
}

// researchGateCallSites is the number of g2Operation call sites in the
// research domain. A new operation changes it, and it has to come with a
// decision about its gate.
const researchGateCallSites = 19

// The research domain must declare the gate at every call site, and the
// declaration must be the one the baseline enforced.
//
// The shared pipeline treats an undeclared gate as a refusal, so a call site
// that forgot to declare one would silently change what the operation does.
// Pinning the declaration here is what keeps the shared core from holding a
// second copy of the gate knowledge.
func TestTheResearchDomainDeclaresTheGateItUsedToEnforce(t *testing.T) {
	directory := packageDirectory(t)
	found := map[string]string{}
	sites := 0

	for _, name := range []string{"g2_service.go", "g2_action_proposal_replay.go"} {
		path := filepath.Join(directory, name)
		file, err := parser.ParseFile(token.NewFileSet(), path, nil, 0)
		if err != nil {
			t.Fatalf("parse %s: %v", path, err)
		}
		ast.Inspect(file, func(node ast.Node) bool {
			call, ok := node.(*ast.CallExpr)
			if !ok || !isG2OperationCall(call) {
				return true
			}
			sites++
			if len(call.Args) < 11 {
				t.Fatalf("%s: a g2Operation call has %d arguments, want 11", path, len(call.Args))
			}
			operation, ok := stringLiteral(call.Args[4])
			if !ok {
				t.Fatalf("%s: the operation name is not a literal", path)
			}
			gate := declaredGate(call.Args[9])
			if gate == "Undeclared" {
				t.Errorf("%s: %s declares the undeclared gate; the pipeline would refuse it", path, operation)
			}
			if previous, seen := found[operation]; seen && previous != gate {
				t.Errorf("%s: %s declares %s here and %s elsewhere", path, operation, gate, previous)
			}
			found[operation] = gate
			return true
		})
	}

	if sites != researchGateCallSites {
		t.Errorf("the research domain has %d g2Operation call sites, want %d; a new operation must come with a decision about its gate", sites, researchGateCallSites)
	}
	for operation, want := range declaredGates {
		got, ok := found[operation]
		if !ok {
			t.Errorf("%s has no call site any more; drop it from declaredGates", operation)
			continue
		}
		if got != want {
			t.Errorf("%s declares %s, want %s (the baseline enforced %s)", operation, got, want, want)
		}
	}
	for operation := range found {
		if _, ok := declaredGates[operation]; !ok {
			t.Errorf("%s has a call site but no entry in declaredGates; the gate it declares must be recorded", operation)
		}
	}
}

func isG2OperationCall(call *ast.CallExpr) bool {
	selector, ok := call.Fun.(*ast.SelectorExpr)
	if !ok || selector.Sel.Name != "g2Operation" {
		return false
	}
	receiver, ok := selector.X.(*ast.Ident)
	return ok && receiver.Name == "s"
}

func declaredGate(expression ast.Expr) string {
	selector, ok := expression.(*ast.SelectorExpr)
	if !ok {
		// A computed gate: the call site decided for itself.
		return "conditional"
	}
	switch selector.Sel.Name {
	case "HumanGateRequired":
		return "Required"
	case "HumanGateNotRequired":
		return "NotRequired"
	case "HumanGateUndeclared":
		return "Undeclared"
	default:
		return "conditional"
	}
}

func stringLiteral(expression ast.Expr) (string, bool) {
	literal, ok := expression.(*ast.BasicLit)
	if !ok || literal.Kind != token.STRING {
		return "", false
	}
	return literal.Value[1 : len(literal.Value)-1], true
}

func packageDirectory(t *testing.T) string {
	t.Helper()
	_, file, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("cannot locate the test source")
	}
	return filepath.Dir(file)
}
