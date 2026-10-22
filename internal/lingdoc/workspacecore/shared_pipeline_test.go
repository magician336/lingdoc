package workspacecore

import (
	"context"
	"encoding/json"
	"errors"
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Tencent/WeKnora/internal/types"
)

// The research domain and the workspace now run the same pipeline, so the five
// behaviours that used to be implemented twice must answer the same way in
// both.
//
// The assertion drives one scenario per behaviour through the research
// domain's own operations and checks the answer that matters:
//
//   - the idempotency key conflict: the same key with a different body is
//     refused;
//   - the request body digest: the same key with the same body is a replay,
//     not a second effect;
//   - the replay order: a replay does not repeat the effect;
//   - the source recheck: a source that was revoked is refused even on a
//     replay;
//   - the human decision gate: a machine identity is refused.
//
// The workspace's side of each behaviour is covered by the workspace's own
// tests; this file pins the research domain's side, and the shared pipeline
// is what makes the two the same code.
func TestResearchDomainBehavesLikeTheSharedPipeline(t *testing.T) {
	t.Run("idempotency key conflict", func(t *testing.T) {
		f := newAgentFixture(t, false)
		input := CreateImportCandidateInput{
			VersionExpectation: f.expectation(0),
			SourceID:           "source-1",
			Purpose:            "定义参考",
			SaveMode:           "link_excerpt",
			Scope:              "project",
			ModelUseAllowed:    true,
			License:            "合成公开fixture",
		}
		if _, _, _, err := f.svc.CreateImportCandidate(context.Background(), f.owner, f.project.ID, "g2-shared-key", input); err != nil {
			t.Fatalf("first call: %v", err)
		}
		altered := input
		altered.Purpose = "换了用途的同一个键"
		if _, _, _, err := f.svc.CreateImportCandidate(context.Background(), f.owner, f.project.ID, "g2-shared-key", altered); !errors.Is(err, ErrIdempotencyConflict) {
			t.Fatalf("same key, different body = %v, want ErrIdempotencyConflict", err)
		}
	})

	t.Run("request body digest and replay order", func(t *testing.T) {
		f := newAgentFixture(t, false)
		input := CreateImportCandidateInput{
			VersionExpectation: f.expectation(0),
			SourceID:           "source-1",
			Purpose:            "定义参考",
			SaveMode:           "link_excerpt",
			Scope:              "project",
			ModelUseAllowed:    true,
			License:            "合成公开fixture",
		}
		first, _, firstReplay, err := f.svc.CreateImportCandidate(context.Background(), f.owner, f.project.ID, "g2-shared-digest", input)
		if err != nil {
			t.Fatalf("first call: %v", err)
		}
		if firstReplay {
			t.Fatal("the first call must not be a replay")
		}
		second, _, secondReplay, err := f.svc.CreateImportCandidate(context.Background(), f.owner, f.project.ID, "g2-shared-digest", input)
		if err != nil {
			t.Fatalf("replay: %v", err)
		}
		if !secondReplay {
			t.Fatal("the same key with the same body must be a replay")
		}
		if string(first) != string(second) {
			t.Fatalf("a replay must answer the stored body:\nfirst  %s\nsecond %s", first, second)
		}
	})

	t.Run("source recheck", func(t *testing.T) {
		f := newAgentFixture(t, false)
		input := CreateImportCandidateInput{
			VersionExpectation: f.expectation(0),
			SourceID:           "source-1",
			Purpose:            "定义参考",
			SaveMode:           "link_excerpt",
			Scope:              "project",
			ModelUseAllowed:    true,
			License:            "合成公开fixture",
		}
		if _, _, _, err := f.svc.CreateImportCandidate(context.Background(), f.owner, f.project.ID, "g2-shared-source", input); err != nil {
			t.Fatalf("first call: %v", err)
		}
		// The source is revoked after the operation was recorded. A replay must
		// be refused: the saved result does not survive the loss of the
		// source it was derived from.
		f.source.denied["source-1"] = true
		if _, _, _, err := f.svc.CreateImportCandidate(context.Background(), f.owner, f.project.ID, "g2-shared-source", input); !errors.Is(err, ErrSourceUnavailable) {
			t.Fatalf("replay after the source was revoked = %v, want ErrSourceUnavailable", err)
		}
	})

	t.Run("human decision gate", func(t *testing.T) {
		f := newAgentFixture(t, false)
		machine := machinePrincipalContext(context.Background(), f.owner)
		input := SaveResearchClaimInput{
			VersionExpectation: f.expectation(0),
			Text:               "声称已经核实",
			Scope:              "本章",
			Reason:             "机器确认",
			Confirmed:          true,
		}
		// The research domain declares the gate per call, so the same
		// operation is refused for a machine identity and allowed for a person.
		if _, _, _, err := f.svc.SaveResearchClaim(machine, f.owner, f.project.ID, "g2-shared-gate", input); !errors.Is(err, ErrHumanDecisionRequired) {
			t.Fatalf("a machine identity = %v, want ErrHumanDecisionRequired", err)
		}
		if _, _, _, err := f.svc.SaveResearchClaim(context.Background(), f.owner, f.project.ID, "g2-shared-gate", input); err != nil {
			t.Fatalf("a person: %v", err)
		}
	})
}

// The gate is declared per call, and the declaration is the only thing that
// decides it.
//
// The research domain's operations do not agree about the gate: one operation
// needs it and another with the same shape does not, and one operation needs
// it only for some of its calls. A rule that keyed the gate off the operation
// name could not express that, so the caller declares it and the pipeline
// applies the declaration.
func TestTheResearchDomainDeclaresItsGatePerCall(t *testing.T) {
	f := newAgentFixture(t, false)
	machine := machinePrincipalContext(context.Background(), f.owner)

	// An operation that needs the gate: a machine identity is refused.
	claim := SaveResearchClaimInput{
		VersionExpectation: f.expectation(0),
		Text:               "声称已经核实",
		Scope:              "本章",
		Reason:             "机器确认",
		Confirmed:          true,
	}
	if _, _, _, err := f.svc.SaveResearchClaim(machine, f.owner, f.project.ID, "g2-gate-required", claim); !errors.Is(err, ErrHumanDecisionRequired) {
		t.Fatalf("a gated operation for a machine = %v, want ErrHumanDecisionRequired", err)
	}

	// An operation that does not need the gate: the machine identity gets the
	// authorization's answer, not the gate's. The two errors are different
	// shapes, so the difference is visible even though both refuse.
	proposal := CreateActionProposalInput{
		VersionExpectation: f.expectation(0),
		Action:             "save_spec",
		TargetID:           f.project.ID,
		Input:              json.RawMessage(`{"expected_spec_revision":1,"fields":{"research_goal":"候选条件"}}`),
		SourceIDs:          []string{},
		ExpectedEffect:     "生成新条件版本",
	}
	_, _, _, err := f.svc.CreateActionProposal(machine, f.owner, f.project.ID, "g2-gate-not-required", proposal)
	if errors.Is(err, ErrHumanDecisionRequired) {
		t.Fatal("an operation that declared no gate must not be refused by the gate")
	}
}

// machinePrincipalContext makes the caller a machine identity.
//
// The human decision gate is an identity check on the context's principal,
// not on the actor's user id: a machine acts under an API principal, and that
// is what the gate reads.
func machinePrincipalContext(ctx context.Context, owner Actor) context.Context {
	ctx = context.WithValue(ctx, types.TenantIDContextKey, owner.TenantID)
	ctx = types.WithPrincipal(ctx, types.Principal{Type: types.PrincipalAPITenant, ID: "machine-key"})
	ctx = types.WithTenantAPIKeyScope(ctx, types.TenantAPIKeyScope{
		KeyID:        1,
		ScopeType:    types.APIKeyScopeTenant,
		Capabilities: types.StringArray{string(types.APIKeyCapabilityRetrieve)},
	})
	return ctx
}

// The gate itself still refuses a machine identity.
//
// The research domain's own operations may refuse a machine identity for
// another reason first - the request may fail a check that runs before the
// gate. The gate's own refusal is what this asserts: a request that is
// otherwise valid is refused because the caller may not act for themselves.
//
// The error is the gate's, not the authorization's: the request is otherwise
// valid, so the only thing that can refuse it is the gate.
func TestTheGateRefusesAMachineIdentityOnAnOtherwiseValidRequest(t *testing.T) {
	f := newAgentFixture(t, false)
	machine := machinePrincipalContext(context.Background(), f.owner)
	input := SaveResearchClaimInput{
		VersionExpectation: f.expectation(0),
		Text:               "声称已经核实",
		Scope:              "本章",
		Reason:             "机器确认",
		Confirmed:          true,
	}
	err := func() error {
		_, _, _, e := f.svc.SaveResearchClaim(machine, f.owner, f.project.ID, "g2-gate-refuses", input)
		return e
	}()
	if !errors.Is(err, ErrHumanDecisionRequired) {
		t.Fatalf("an otherwise valid request from a machine identity = %v, want ErrHumanDecisionRequired", err)
	}
}

// The shared pipeline must not keep a copy of the operation list that decides
// the gate.
//
// The list belongs to the domain that owns the operations: the workspace has
// one, the research domain declares per call. A copy inside the pipeline would
// be a second owner of the same knowledge, which is exactly what the shared
// core is meant to remove.
//
// The assertion reads the pipeline's source and reports a function that
// decides the gate from an operation name. A function that keys some other
// rule off an operation name - the retry set, for one - is not a copy of the
// gate list, so the check is on the name of the function, not on its shape.
func TestThePipelineHasNoCopyOfTheGateOperationList(t *testing.T) {
	core := filepath.Join("..", "operationcore")
	fset := token.NewFileSet()
	entries, err := os.ReadDir(core)
	if err != nil {
		t.Fatalf("read the shared core: %v", err)
	}
	checked := 0
	for _, entry := range entries {
		name := entry.Name()
		if !strings.HasSuffix(name, ".go") || strings.HasSuffix(name, "_test.go") {
			continue
		}
		checked++
		source, err := os.ReadFile(filepath.Join(core, name))
		if err != nil {
			t.Fatalf("read %s: %v", name, err)
		}
		file, err := parser.ParseFile(fset, filepath.Join(core, name), source, parser.SkipObjectResolution)
		if err != nil {
			t.Fatalf("parse %s: %v", name, err)
		}
		ast.Inspect(file, func(node ast.Node) bool {
			decl, ok := node.(*ast.FuncDecl)
			if !ok || decl.Recv != nil {
				return true
			}
			// A function whose whole purpose is to answer "does this
			// operation need the gate" is the copy this assertion forbids.
			lower := strings.ToLower(decl.Name.Name)
			if !strings.Contains(lower, "human") && !strings.Contains(lower, "gate") {
				return true
			}
			if lower == "requirehumandecision" {
				// The check itself is the mechanism the pipeline applies; it
				// is not a list of operations.
				return true
			}
			t.Errorf("%s: %s decides the gate from the operation name: the caller declares it",
				name, decl.Name.Name)
			return true
		})
	}
	if checked == 0 {
		t.Fatal("no source file was checked, the assertion may have gone stale")
	}
}
