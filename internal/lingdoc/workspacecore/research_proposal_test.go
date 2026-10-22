package workspacecore

import (
	"context"
	"encoding/json"
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// The research proposal contract is the seam the whole path reads: the model
// proposes, the server decodes against this one shape, and the interface only
// ever sees a proposal that decoded. The cases below pin the decoder at that
// seam - a payload that does not decode is refused whole, never reshaped.

func TestResearchProposalAcceptsLegalShapeAndKeepsSynonymAsExtraQuery(t *testing.T) {
	raw := json.RawMessage(`{"goal":"弄清合成材料的定义","question_type":"定义","scope":"只查项目资料","exclusions":["不含二手综述"],"queries":["合成材料 定义","合成材料 反例","synthetic material definition"],"expected_output":"候选资料与反例"}`)
	proposal, err := DecodeResearchProposal(raw)
	if err != nil {
		t.Fatalf("legal proposal refused: %v", err)
	}
	if proposal.Goal != "弄清合成材料的定义" || proposal.QuestionType != "定义" || proposal.Scope != "只查项目资料" || proposal.ExpectedOutput != "候选资料与反例" {
		t.Fatalf("proposal lost a field: %+v", proposal)
	}
	if len(proposal.Queries) != 3 || proposal.Queries[2] != "synthetic material definition" {
		t.Fatalf("a synonym is not an extra query: %+v", proposal.Queries)
	}
	if len(proposal.Exclusions) != 1 || proposal.Exclusions[0] != "不含二手综述" {
		t.Fatalf("exclusions lost: %+v", proposal.Exclusions)
	}
}

func TestResearchProposalRefusesUnknownField(t *testing.T) {
	raw := json.RawMessage(`{"goal":"g","scope":"s","queries":["a","b"],"expected_output":"o","confidence":0.9}`)
	if _, err := DecodeResearchProposal(raw); err == nil {
		t.Fatal("unknown field accepted")
	}
}

func TestResearchProposalRefusesMissingRequiredField(t *testing.T) {
	for name, raw := range map[string]string{
		"no goal":            `{"scope":"s","queries":["a","b"],"expected_output":"o"}`,
		"no scope":           `{"goal":"g","queries":["a","b"],"expected_output":"o"}`,
		"no expected output": `{"goal":"g","scope":"s","queries":["a","b"]}`,
		"no queries":         `{"goal":"g","scope":"s","expected_output":"o"}`,
	} {
		if _, err := DecodeResearchProposal(json.RawMessage(raw)); err == nil {
			t.Fatalf("missing required field accepted: %s", name)
		}
	}
}

func TestResearchProposalRefusesFewerThanTwoQueries(t *testing.T) {
	for name, raw := range map[string]string{
		"one query":  `{"goal":"g","scope":"s","queries":["a"],"expected_output":"o"}`,
		"no query":   `{"goal":"g","scope":"s","queries":[],"expected_output":"o"}`,
		"blank line": `{"goal":"g","scope":"s","queries":["a","  "],"expected_output":"o"}`,
	} {
		if _, err := DecodeResearchProposal(json.RawMessage(raw)); err == nil {
			t.Fatalf("fewer than two usable queries accepted: %s", name)
		}
	}
}

func TestResearchProposalRefusesOversizedPayload(t *testing.T) {
	raw := json.RawMessage(`{"goal":"` + strings.Repeat("长", 70000) + `","scope":"s","queries":["a","b"],"expected_output":"o"}`)
	if _, err := DecodeResearchProposal(raw); err == nil {
		t.Fatal("oversized payload accepted")
	}
}

func TestResearchProposalStatesArePresentAbsentAndInvalid(t *testing.T) {
	legal := AgentTurn{Suggestions: json.RawMessage(`{"reply":"r","task_card":{"goal":"g","scope":"s","queries":["a","b"],"expected_output":"o"}}`)}
	state, err := ResearchProposalStateForTurn(legal)
	if err != nil || state != ResearchProposalStatePresent {
		t.Fatalf("legal proposal is not present: %q %v", state, err)
	}
	absent := AgentTurn{Suggestions: json.RawMessage(`{"reply":"r"}`)}
	state, err = ResearchProposalStateForTurn(absent)
	if err != nil || state != ResearchProposalStateAbsent {
		t.Fatalf("missing proposal is not absent: %q %v", state, err)
	}
	invalid := AgentTurn{Suggestions: json.RawMessage(`{"reply":"r","task_card":{"goal":"g","scope":"s","queries":["a"],"expected_output":"o"}}`)}
	state, err = ResearchProposalStateForTurn(invalid)
	if err != nil || state != ResearchProposalStateInvalid {
		t.Fatalf("broken proposal is not invalid: %q %v", state, err)
	}
}

// A1/A2: the shape has exactly one owner. The contract module declares it, the
// prompt renders it, and nothing else may spell it out again - a second copy
// is how the prompt and the contract drift apart.
func TestResearchProposalShapeHasOneOwner(t *testing.T) {
	contract := ResearchProposalFieldSet()
	if len(contract) == 0 {
		t.Fatal("the contract declares no field")
	}
	copies := proposalShapeCopies(t, contract)
	if len(copies) != 1 || copies[0] != "research_proposal.go:ResearchProposal" {
		t.Fatalf("the proposal shape is declared more than once: %v", copies)
	}
	adapter := filepath.Join("..", "workspace", "agent_model.go")
	for _, literal := range stringLiterals(t, adapter) {
		// One field name in a prompt is a word, not a shape: the rule
		// suggestion prompt says "scope" in its own vocabulary. Two or
		// more is the shape written down a second time.
		named := 0
		for _, name := range contract {
			if strings.Contains(literal, name) {
				named++
			}
		}
		if named >= 2 {
			t.Fatalf("the prompt spells the shape out again: %s", literal)
		}
		if strings.Contains(literal, ResearchProposalKey) {
			t.Fatalf("the prompt spells the proposal key out again: %s", literal)
		}
	}
	if calls := contractRendererCalls(t, adapter); calls < 2 {
		t.Fatalf("the prompt does not render the contract shape: %d renderings", calls)
	}
}

// ResearchProposalFieldSet is the field names the contract owns.
func ResearchProposalFieldSet() []string {
	return append([]string{}, ResearchProposalFields...)
}

// proposalShapeCopies finds every struct type in the server that declares
// exactly the proposal's field names. Only the contract module may.
func proposalShapeCopies(t *testing.T, contract []string) []string {
	t.Helper()
	found := []string{}
	err := filepath.WalkDir("..", func(path string, entry os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if entry.IsDir() || !strings.HasSuffix(path, ".go") || strings.HasSuffix(path, "_test.go") {
			return nil
		}
		file, parseErr := parser.ParseFile(token.NewFileSet(), path, nil, 0)
		if parseErr != nil {
			t.Fatalf("cannot parse %s: %v", path, parseErr)
		}
		for _, declaration := range file.Decls {
			generic, ok := declaration.(*ast.GenDecl)
			if !ok || generic.Tok != token.TYPE {
				continue
			}
			for _, specification := range generic.Specs {
				typeSpec, ok := specification.(*ast.TypeSpec)
				if !ok {
					continue
				}
				structure, ok := typeSpec.Type.(*ast.StructType)
				if !ok {
					continue
				}
				names := jsonFieldNames(structure)
				if len(names) != len(contract) {
					continue
				}
				same := true
				for _, name := range contract {
					if !names[name] {
						same = false
						break
					}
				}
				if same {
					found = append(found, filepath.Base(path)+":"+typeSpec.Name.Name)
				}
			}
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	return found
}

// jsonFieldNames reads the JSON field names a struct declares.
func jsonFieldNames(structure *ast.StructType) map[string]bool {
	names := map[string]bool{}
	for _, field := range structure.Fields.List {
		tag := ""
		if field.Tag != nil {
			tag = strings.Trim(field.Tag.Value, "`")
		}
		value := ""
		if index := strings.Index(tag, `json:"`); index >= 0 {
			rest := tag[index+len(`json:"`):]
			if end := strings.Index(rest, `"`); end >= 0 {
				value = rest[:end]
			}
		}
		if value == "" || value == "-" {
			continue
		}
		if comma := strings.Index(value, ","); comma >= 0 {
			value = value[:comma]
		}
		names[value] = true
	}
	return names
}

// stringLiterals reads every string literal of a Go source file.
func stringLiterals(t *testing.T, path string) []string {
	t.Helper()
	file, err := parser.ParseFile(token.NewFileSet(), path, nil, 0)
	if err != nil {
		t.Fatal(err)
	}
	literals := []string{}
	ast.Inspect(file, func(node ast.Node) bool {
		literal, ok := node.(*ast.BasicLit)
		if !ok || literal.Kind != token.STRING {
			return true
		}
		literals = append(literals, literal.Value)
		return true
	})
	return literals
}

// contractRendererCalls counts how often the adapter builds a prompt from the
// contract module instead of carrying its own copy of the shape.
func contractRendererCalls(t *testing.T, path string) int {
	t.Helper()
	file, err := parser.ParseFile(token.NewFileSet(), path, nil, 0)
	if err != nil {
		t.Fatal(err)
	}
	calls := 0
	ast.Inspect(file, func(node ast.Node) bool {
		call, ok := node.(*ast.CallExpr)
		if !ok {
			return true
		}
		selector, ok := call.Fun.(*ast.SelectorExpr)
		if ok && selector.Sel.Name == "ResearchProposalShapeText" {
			calls++
		}
		return true
	})
	return calls
}

// proposalModelStub is a fake model: it answers with the structured output the
// case needs, so the turn endpoint can be exercised without a provider.
type proposalModelStub struct {
	structured json.RawMessage
	reply      string
	calls      int
}

func (m *proposalModelStub) Reply(_ context.Context, _ Actor, _ ModelRequest) (ModelReply, error) {
	m.calls++
	return ModelReply{Content: m.reply, StructuredOutput: m.structured, ModelID: "synthetic-model", RunMode: "real_api_fake_model", InputTokens: 100, OutputTokens: 20, FinishReason: "stop"}, nil
}

const legalProposalPayload = `{"reply":"我按你的问题整理了一条检索计划，请核对后自行保存。","task_card":{"goal":"弄清合成材料的定义与限制","question_type":"定义","scope":"只查项目资料与公开综述","exclusions":["不含二手综述"],"queries":["合成材料 定义","合成材料 反例","synthetic material definition"],"expected_output":"候选资料、反例与证据缺口"}}`

func proposalTurn(t *testing.T, f agentFixture, agent, key string, stub *proposalModelStub) AgentTurn {
	t.Helper()
	f.svc.agentRuntime.Model = stub
	raw, _, _, err := f.svc.CreateAgentSession(context.Background(), f.owner, f.project.ID, agent, key+"-session", f.expectation(0))
	session := decodeAgentJSON[AgentSession](t, raw, err)
	input := AgentTurnInput{AgentContextInput: AgentContextInput{VersionExpectation: f.expectation(0), SourceIDs: []string{"source-1"}, SensitiveContextConfirmed: true}, UserInput: "帮我找合成材料的资料"}
	raw, _, _, err = f.svc.SendAgentTurn(context.Background(), f.owner, f.project.ID, session.ID, key+"-turn", input)
	return decodeAgentJSON[AgentTurn](t, raw, err)
}

func proposalOfTurn(t *testing.T, turn AgentTurn) ResearchProposal {
	t.Helper()
	raw, err := researchProposalFromSuggestions(turn.Suggestions)
	if err != nil {
		t.Fatal(err)
	}
	proposal, err := DecodeResearchProposal(raw)
	if err != nil {
		t.Fatal(err)
	}
	return proposal
}

func assertProposalCreatedNothing(t *testing.T, f agentFixture) {
	t.Helper()
	ctx := context.Background()
	tasks, err := f.svc.ListResearchTasks(ctx, f.owner, f.project.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(tasks) != 0 {
		t.Fatalf("the proposal became a research task: %+v", tasks)
	}
	imports, err := f.svc.ListImportCandidates(ctx, f.owner, f.project.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(imports) != 0 {
		t.Fatalf("the proposal became an import candidate: %+v", imports)
	}
	cards, err := f.svc.ListEvidenceCards(ctx, f.owner, f.project.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(cards) != 0 {
		t.Fatalf("the proposal became an evidence card: %+v", cards)
	}
	claims, err := f.svc.ListResearchClaims(ctx, f.owner, f.project.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(claims) != 0 {
		t.Fatalf("the proposal became a research claim: %+v", claims)
	}
}

// A4/A9/A10/A12/A15: a legal proposal travels with the turn it belongs to,
// carries the model and run mode it came from, and creates nothing by itself.
func TestResearchProposalReachesTheTurnAndCreatesNothing(t *testing.T) {
	f := newAgentFixture(t, false)
	stub := &proposalModelStub{structured: json.RawMessage(legalProposalPayload), reply: "我按你的问题整理了一条检索计划，请核对后自行保存。"}
	turn := proposalTurn(t, f, ResearchProposalAgent, "pf5-legal", stub)
	if turn.Status != "completed" {
		t.Fatalf("the turn did not complete: %+v", turn)
	}
	if turn.ModelID != "synthetic-model" || turn.RunMode != "real_api_fake_model" {
		t.Fatalf("the proposal lost its provenance: %+v", turn)
	}
	proposal := proposalOfTurn(t, turn)
	if proposal.Goal != "弄清合成材料的定义与限制" || proposal.Scope != "只查项目资料与公开综述" || proposal.ExpectedOutput != "候选资料、反例与证据缺口" {
		t.Fatalf("the proposal lost a field: %+v", proposal)
	}
	if len(proposal.Queries) != 3 || proposal.Queries[2] != "synthetic material definition" {
		t.Fatalf("a synonym is not an extra query: %+v", proposal.Queries)
	}
	if len(proposal.Exclusions) != 1 {
		t.Fatalf("exclusions lost: %+v", proposal.Exclusions)
	}
	assertProposalCreatedNothing(t, f)
}

// A6/A11: a proposal that does not decode never reaches the interface, and
// nothing is written on the way to saying so.
func TestInvalidResearchProposalFailsTheTurnWithoutWritingAnything(t *testing.T) {
	for name, payload := range map[string]string{
		"one query":     `{"reply":"我整理了一条检索计划。","task_card":{"goal":"弄清合成材料","scope":"只查项目资料","queries":["合成材料 定义"],"expected_output":"候选资料"}}`,
		"unknown field": `{"reply":"我整理了一条检索计划。","task_card":{"goal":"弄清合成材料","scope":"只查项目资料","queries":["a","b"],"expected_output":"候选资料","confidence":0.9}}`,
		"missing goal":  `{"reply":"我整理了一条检索计划。","task_card":{"scope":"只查项目资料","queries":["a","b"],"expected_output":"候选资料"}}`,
	} {
		f := newAgentFixture(t, false)
		stub := &proposalModelStub{structured: json.RawMessage(payload), reply: "我整理了一条检索计划。"}
		turn := proposalTurn(t, f, ResearchProposalAgent, "pf5-invalid", stub)
		if turn.Status != "failed" || turn.FailureCode != ResearchProposalFailureCode {
			t.Fatalf("%s: the broken proposal was not refused: %+v", name, turn)
		}
		if len(turn.Suggestions) != 0 || turn.Reply != "" {
			t.Fatalf("%s: the broken proposal still reached the interface: %+v", name, turn)
		}
		assertProposalCreatedNothing(t, f)
	}
}

// The assistant's editable task suggestion is a different card with its own
// rules: this contract does not change what the assistant may answer.
func TestAssistantTaskSuggestionIsNotJudgedByTheProposalContract(t *testing.T) {
	f := newAgentFixture(t, false)
	payload := `{"reply":"我给了一条建议。","task_card":{"goal":"弄清合成材料","scope":"只查项目资料","queries":["合成材料 定义"],"expected_output":"候选资料"}}`
	stub := &proposalModelStub{structured: json.RawMessage(payload), reply: "我给了一条建议。"}
	turn := proposalTurn(t, f, "task_assistant", "pf5-assistant", stub)
	if turn.Status != "completed" {
		t.Fatalf("the assistant's answer changed: %+v", turn)
	}
}
