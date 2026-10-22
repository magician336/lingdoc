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
	"reflect"
	"strings"
	"testing"
)

// The research proposal contract is the seam the whole path reads: the model
// proposes, the server decodes against this one shape, and the interface only
// ever sees a proposal that decoded. The cases below pin the decoder at that
// seam - a payload that does not decode is refused whole, never reshaped.

func TestResearchProposalAcceptsLegalShapeAndKeepsSynonymAsExtraQuery(t *testing.T) {
	raw := json.RawMessage(`{"goal":"弄清合成材料的定义","question_type":"定义","scope":"只查项目资料","exclusions":["不含二手综述"],"queries":["合成材料 定义","合成材料 反例","synthetic material definition"],"expected_output":"候选资料与反例"}`)
	proposal, err := DecodeResearchProposal(raw, authorizedContext("source-1"))
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
	if _, err := DecodeResearchProposal(raw, authorizedContext("source-1")); err == nil {
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
		if _, err := DecodeResearchProposal(json.RawMessage(raw), authorizedContext("source-1")); err == nil {
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
		if _, err := DecodeResearchProposal(json.RawMessage(raw), authorizedContext("source-1")); err == nil {
			t.Fatalf("fewer than two usable queries accepted: %s", name)
		}
	}
}

func TestResearchProposalRefusesOversizedPayload(t *testing.T) {
	raw := json.RawMessage(`{"goal":"` + strings.Repeat("长", 70000) + `","scope":"s","queries":["a","b"],"expected_output":"o"}`)
	if _, err := DecodeResearchProposal(raw, authorizedContext("source-1")); err == nil {
		t.Fatal("oversized payload accepted")
	}
}

// authorizedContext is the context a turn was given: the sources the user
// selected for this turn, each with the model's permission to use it.
func authorizedContext(sourceIDs ...string) AgentContextPackage {
	refs := make([]AgentSourceRef, 0, len(sourceIDs))
	for _, id := range sourceIDs {
		refs = append(refs, AgentSourceRef{SourceID: id, ModelUseAllowed: true})
	}
	return AgentContextPackage{SourceRefs: refs, AuthorizationState: "allowed"}
}

// revokedContext is the context of a turn whose source lost the user's
// permission: the source is still in the context, but the model may not use
// it.
func revokedContext(sourceIDs ...string) AgentContextPackage {
	pack := authorizedContext(sourceIDs...)
	for i := range pack.SourceRefs {
		pack.SourceRefs[i].ModelUseAllowed = false
	}
	return pack
}

// A7: a candidate source the user could read is accepted, with its reason.
func TestResearchProposalAcceptsCandidateSourcesFromTheAuthorizedContext(t *testing.T) {
	raw := json.RawMessage(`{"goal":"g","scope":"s","queries":["a","b"],"expected_output":"o","candidate_sources":[{"source_id":"source-1","reason":"它写明了合成材料的定义"},{"source_id":"source-2","reason":"它给出了反例"}]}`)
	proposal, err := DecodeResearchProposal(raw, authorizedContext("source-1", "source-2"))
	if err != nil {
		t.Fatalf("legal candidate sources refused: %v", err)
	}
	if len(proposal.CandidateSources) != 2 {
		t.Fatalf("candidate sources lost: %+v", proposal.CandidateSources)
	}
	if proposal.CandidateSources[0].SourceID != "source-1" || proposal.CandidateSources[0].Reason != "它写明了合成材料的定义" {
		t.Fatalf("a candidate source lost its reason: %+v", proposal.CandidateSources[0])
	}
	if proposal.CandidateSources[1].SourceID != "source-2" {
		t.Fatalf("candidate sources are not kept apart: %+v", proposal.CandidateSources)
	}
}

// A7: a candidate source the turn was never given is refused, loudly. It is
// not dropped: the user must be able to see that the model asked for
// something nobody could read.
func TestResearchProposalRefusesCandidateSourceOutsideTheContext(t *testing.T) {
	for name, raw := range map[string]string{
		"never given":   `{"goal":"g","scope":"s","queries":["a","b"],"expected_output":"o","candidate_sources":[{"source_id":"source-2","reason":"它看起来相关"}]}`,
		"invented id":   `{"goal":"g","scope":"s","queries":["a","b"],"expected_output":"o","candidate_sources":[{"source_id":"made-up","reason":"它看起来相关"}]}`,
		"second of two": `{"goal":"g","scope":"s","queries":["a","b"],"expected_output":"o","candidate_sources":[{"source_id":"source-1","reason":"可读"},{"source_id":"source-9","reason":"不可读"}]}`,
	} {
		if _, err := DecodeResearchProposal(json.RawMessage(raw), authorizedContext("source-1")); err == nil {
			t.Fatalf("a candidate source outside the context was accepted: %s", name)
		}
	}
}

// A7: a source whose permission was taken away is refused even though it is
// still in the context.
func TestResearchProposalRefusesCandidateSourceWithoutModelUsePermission(t *testing.T) {
	raw := json.RawMessage(`{"goal":"g","scope":"s","queries":["a","b"],"expected_output":"o","candidate_sources":[{"source_id":"source-1","reason":"它写明了定义"}]}`)
	if _, err := DecodeResearchProposal(raw, revokedContext("source-1")); err == nil {
		t.Fatal("a revoked source was accepted as a candidate source")
	}
}

// A7: the shape of a candidate source is held to the same bounds as the rest
// of the proposal.
func TestResearchProposalRefusesBrokenCandidateSource(t *testing.T) {
	for name, raw := range map[string]string{
		"reason too long": `{"goal":"g","scope":"s","queries":["a","b"],"expected_output":"o","candidate_sources":[{"source_id":"source-1","reason":"` + strings.Repeat("长", 2000) + `"}]}`,
		"no reason":       `{"goal":"g","scope":"s","queries":["a","b"],"expected_output":"o","candidate_sources":[{"source_id":"source-1","reason":"  "}]}`,
		"no source id":    `{"goal":"g","scope":"s","queries":["a","b"],"expected_output":"o","candidate_sources":[{"source_id":"","reason":"它相关"}]}`,
		"unknown field":   `{"goal":"g","scope":"s","queries":["a","b"],"expected_output":"o","candidate_sources":[{"source_id":"source-1","reason":"它相关","locator":"编造的定位"}]}`,
	} {
		if _, err := DecodeResearchProposal(json.RawMessage(raw), authorizedContext("source-1")); err == nil {
			t.Fatalf("a broken candidate source was accepted: %s", name)
		}
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
	// The candidate source is part of the same shape, so it has one owner too.
	candidate := ResearchProposalCandidateSourceFieldSet()
	if len(candidate) == 0 {
		t.Fatal("the contract declares no candidate source field")
	}
	candidateCopies := proposalShapeCopies(t, candidate)
	if len(candidateCopies) != 1 || candidateCopies[0] != "research_proposal.go:ResearchProposalCandidateSource" {
		t.Fatalf("the candidate source shape is declared more than once: %v", candidateCopies)
	}
	// The rendering and the decoder must speak the same names: the prompt
	// asks for the fields the decoder reads.
	if names := jsonFieldNamesOf(ResearchProposalCandidateSource{}); len(names) != len(candidate) {
		t.Fatalf("the candidate source names drifted from the shape: %v", names)
	}
	for _, name := range candidate {
		if !jsonFieldNamesOf(ResearchProposalCandidateSource{})[name] {
			t.Fatalf("the candidate source shape does not declare %s", name)
		}
		if !strings.Contains(ResearchProposalShapeText(), name) {
			t.Fatalf("the rendered shape does not mention %s", name)
		}
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
		// The candidate sources are named in one place only: the contract.
		// Their own field names are shared with other contracts (the
		// assistant's change draft has a reason and a source id too), so
		// what may not appear here is the candidate sources list itself.
		if strings.Contains(literal, researchProposalCandidateSourcesField) {
			t.Fatalf("the prompt spells the candidate sources out again: %s", literal)
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

// ResearchProposalCandidateSourceFieldSet is the field names the candidate
// source shape owns.
func ResearchProposalCandidateSourceFieldSet() []string {
	return []string{researchProposalCandidateSourceIDField, researchProposalCandidateSourceReasonField}
}

// jsonFieldNamesOf reads the JSON field names a value's struct declares.
func jsonFieldNamesOf(value any) map[string]bool {
	names := map[string]bool{}
	shape := reflect.TypeOf(value)
	for i := 0; i < shape.NumField(); i++ {
		name, _, _ := strings.Cut(shape.Field(i).Tag.Get("json"), ",")
		if name != "" && name != "-" {
			names[name] = true
		}
	}
	return names
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
	proposal, err := DecodeResearchProposal(raw, turn.Context)
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

// A7: a proposal whose candidate sources all belong to the turn's own
// authorized context reaches the interface with each reason beside its
// source, and creates nothing by itself.
func TestResearchProposalCarriesItsCandidateSourcesToTheTurn(t *testing.T) {
	f := newAgentFixture(t, false)
	payload := `{"reply":"我按你的问题整理了一条检索计划。","task_card":{"goal":"弄清合成材料的定义","scope":"只查项目资料","queries":["合成材料 定义","合成材料 反例"],"expected_output":"候选资料与反例","candidate_sources":[{"source_id":"source-1","reason":"它写明了合成材料的定义"}]}}`
	stub := &proposalModelStub{structured: json.RawMessage(payload), reply: "我按你的问题整理了一条检索计划。"}
	turn := proposalTurn(t, f, ResearchProposalAgent, "pf5-sources", stub)
	if turn.Status != "completed" {
		t.Fatalf("the turn did not complete: %+v", turn)
	}
	proposal := proposalOfTurn(t, turn)
	if len(proposal.CandidateSources) != 1 {
		t.Fatalf("the candidate sources did not travel with the proposal: %+v", proposal)
	}
	if proposal.CandidateSources[0].SourceID != "source-1" || proposal.CandidateSources[0].Reason != "它写明了合成材料的定义" {
		t.Fatalf("a candidate source lost its reason on the way: %+v", proposal.CandidateSources[0])
	}
	assertProposalCreatedNothing(t, f)
}

// A7: a proposal that names a source this turn was never given is refused
// whole: the turn fails with a readable code, nothing is written, and the
// source never reaches the interface.
func TestResearchProposalCitingASourceOutsideTheTurnIsRefused(t *testing.T) {
	for name, payload := range map[string]string{
		"another source of the same project": `{"reply":"我整理了一条检索计划。","task_card":{"goal":"弄清合成材料","scope":"只查项目资料","queries":["a","b"],"expected_output":"候选资料","candidate_sources":[{"source_id":"source-2","reason":"它看起来相关"}]}}`,
		"an invented source":                 `{"reply":"我整理了一条检索计划。","task_card":{"goal":"弄清合成材料","scope":"只查项目资料","queries":["a","b"],"expected_output":"候选资料","candidate_sources":[{"source_id":"made-up","reason":"它看起来相关"}]}}`,
		"one good and one unreadable":        `{"reply":"我整理了一条检索计划。","task_card":{"goal":"弄清合成材料","scope":"只查项目资料","queries":["a","b"],"expected_output":"候选资料","candidate_sources":[{"source_id":"source-1","reason":"可读"},{"source_id":"source-3","reason":"不可读"}]}}`,
	} {
		f := newAgentFixture(t, false)
		stub := &proposalModelStub{structured: json.RawMessage(payload), reply: "我整理了一条检索计划。"}
		turn := proposalTurn(t, f, ResearchProposalAgent, "pf5-outside", stub)
		if turn.Status != "failed" || turn.FailureCode != ResearchProposalFailureCode {
			t.Fatalf("%s: the unreadable candidate source was not refused: %+v", name, turn)
		}
		if len(turn.Suggestions) != 0 || turn.Reply != "" {
			t.Fatalf("%s: the refused proposal still reached the interface: %+v", name, turn)
		}
		assertProposalCreatedNothing(t, f)
	}
}

// A8: a source whose permission was taken away never reaches the model, so
// the turn is refused before the model is asked - and a proposal that names it
// is refused as well.
func TestResearchProposalCitingARevokedSourceIsRefused(t *testing.T) {
	f := newAgentFixture(t, false)
	source := f.source.sources["source-1"]
	source.ModelUseAllowed = false
	f.source.sources["source-1"] = source
	stub := &proposalModelStub{structured: json.RawMessage(legalProposalPayload), reply: "我整理了一条检索计划。"}
	_, _, _, err := f.svc.SendAgentTurn(context.Background(), f.owner, f.project.ID, "pf5-revoked-session", "pf5-revoked-turn", AgentTurnInput{AgentContextInput: AgentContextInput{VersionExpectation: f.expectation(0), SourceIDs: []string{"source-1"}, SensitiveContextConfirmed: true}, UserInput: "帮我找合成材料的资料"})
	if !errors.Is(err, ErrConsentRequired) {
		t.Fatalf("a revoked source was accepted into the turn's context: %v", err)
	}
	if stub.calls != 0 {
		t.Fatalf("the model was asked about a revoked source: %d calls", stub.calls)
	}
}

// A8: another project's material is not readable here, so it cannot enter the
// context, and a proposal that names it is refused.
func TestResearchProposalCitingAnotherProjectsSourceIsRefused(t *testing.T) {
	f := newAgentFixture(t, false)
	raw, _, _, err := f.svc.CreateProject(context.Background(), f.owner, "pf5-other-project", CreateProjectInput{Name: "另一个课题", TemplateID: "template-demo"})
	if err != nil {
		t.Fatal(err)
	}
	otherProject := asProject(t, raw)
	scoped := &projectScopedSources{inner: f.source, owners: map[string]string{"source-2": otherProject.ID}}
	f.svc.agentRuntime.Sources = scoped
	// The material of the other project is not readable here: it cannot
	// enter the context, so the model never sees it.
	if _, err := f.svc.BuildAgentContext(context.Background(), f.owner, f.project.ID, AgentContextInput{VersionExpectation: f.expectation(0), SourceIDs: []string{"source-2"}, SensitiveContextConfirmed: true}); err == nil {
		t.Fatal("another project's material entered this project's context")
	}
	payload := `{"reply":"我整理了一条检索计划。","task_card":{"goal":"弄清合成材料","scope":"只查项目资料","queries":["a","b"],"expected_output":"候选资料","candidate_sources":[{"source_id":"source-2","reason":"它在另一个课题里"}]}}`
	stub := &proposalModelStub{structured: json.RawMessage(payload), reply: "我整理了一条检索计划。"}
	turn := proposalTurn(t, f, ResearchProposalAgent, "pf5-cross-project", stub)
	if turn.Status != "failed" || turn.FailureCode != ResearchProposalFailureCode {
		t.Fatalf("another project's source was accepted: %+v", turn)
	}
	if len(turn.Suggestions) != 0 || turn.Reply != "" {
		t.Fatalf("the refused proposal still reached the interface: %+v", turn)
	}
	assertProposalCreatedNothing(t, f)
}

// projectScopedSources serves a source only to the project that owns it. It
// is how another project's material behaves in the real reader: not readable
// here, so it can never enter this project's context.
type projectScopedSources struct {
	inner  *testSourceReader
	owners map[string]string
}

func (r *projectScopedSources) ReadResearchSource(ctx context.Context, actor Actor, projectID, id string) (ResearchSource, error) {
	if owner, known := r.owners[id]; known && owner != projectID {
		return ResearchSource{}, ErrSourceUnavailable
	}
	return r.inner.ReadResearchSource(ctx, actor, projectID, id)
}

func (r *projectScopedSources) CheckAssetScope(ctx context.Context, actor Actor, projectID string, ids []string) ([]ProjectAssetVersion, error) {
	return r.inner.CheckAssetScope(ctx, actor, projectID, ids)
}
