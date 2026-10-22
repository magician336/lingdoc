package workspacecore

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"reflect"
	"strconv"
	"strings"
	"testing"

	"github.com/Tencent/WeKnora/internal/lingdoc/operationcore"
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
		// The retrieval levels grade what a search found. A candidate
		// source is a reference, not a graded product, so it cannot carry
		// one: the two vocabularies do not meet here.
		"retrieval level": `{"goal":"g","scope":"s","queries":["a","b"],"expected_output":"o","candidate_sources":[{"source_id":"source-1","reason":"它相关","level":"candidate_evidence"}]}`,
	} {
		if _, err := DecodeResearchProposal(json.RawMessage(raw), authorizedContext("source-1")); err == nil {
			t.Fatalf("a broken candidate source was accepted: %s", name)
		}
	}
}

// proposalWithExtras builds a legal proposal payload with the extra columns
// filled in, so a case only has to state what it changes.
func proposalWithExtras(extras string) string {
	return `{"goal":"g","scope":"s","queries":["a","b"],"expected_output":"o"` + extras + `}`
}

// A13: a proposal may say why it is shaped this way, and what it still
// misses. A gap carries two parts - what is missing and what would fill it.
func TestResearchProposalAcceptsReasonsAndGaps(t *testing.T) {
	raw := json.RawMessage(proposalWithExtras(`,"reasons":["目标定成「定义」是因为用户问的就是这个词本身","范围只划到项目资料是因为公开综述尚未授权"],"gaps":[{"description":"缺一篇给出反例的对照研究","expected_evidence":"一篇标注了反例的同行评议文章"}]`))
	proposal, err := DecodeResearchProposal(raw, authorizedContext("source-1"))
	if err != nil {
		t.Fatalf("a proposal carrying reasons and gaps was refused: %v", err)
	}
	if len(proposal.Reasons) != 2 || proposal.Reasons[0] != "目标定成「定义」是因为用户问的就是这个词本身" {
		t.Fatalf("the reasons did not survive: %+v", proposal.Reasons)
	}
	if len(proposal.Gaps) != 1 {
		t.Fatalf("the gaps did not survive: %+v", proposal.Gaps)
	}
	if proposal.Gaps[0].Description != "缺一篇给出反例的对照研究" || proposal.Gaps[0].ExpectedEvidence != "一篇标注了反例的同行评议文章" {
		t.Fatalf("a gap lost one of its two parts: %+v", proposal.Gaps[0])
	}
}

// A13: the reasons and the gaps are held to bounds like the rest of the
// shape. An over-long reason, an over-long gap and a list that grew past its
// count are refused whole - a proposal nobody can read is worse than none.
func TestResearchProposalRefusesOverlongReasonsAndGaps(t *testing.T) {
	manyReasons := strings.TrimSuffix(strings.Repeat(`"目标定成这样是因为用户问的就是这个词",`, 11), ",")
	manyGaps := strings.TrimSuffix(strings.Repeat(`{"description":"缺一篇反例","expected_evidence":"一篇标注了反例的同行评议文章"},`, 11), ",")
	for name, raw := range map[string]string{
		"reason too long":            proposalWithExtras(`,"reasons":["` + strings.Repeat("长", 2000) + `"]`),
		"blank reason":               proposalWithExtras(`,"reasons":["  "]`),
		"gap description too long":   proposalWithExtras(`,"gaps":[{"description":"` + strings.Repeat("长", 2000) + `","expected_evidence":"一篇标注了反例的同行评议文章"}]`),
		"gap expected evidence long": proposalWithExtras(`,"gaps":[{"description":"缺一篇反例","expected_evidence":"` + strings.Repeat("长", 2000) + `"}]`),
		"too many reasons":           proposalWithExtras(`,"reasons":[` + manyReasons + `]`),
		"too many gaps":              proposalWithExtras(`,"gaps":[` + manyGaps + `]`),
	} {
		if _, err := DecodeResearchProposal(json.RawMessage(raw), authorizedContext("source-1")); err == nil {
			t.Fatalf("an overlong reason or gap was accepted: %s", name)
		}
	}
}

// A13: a gap that does not say what would fill it is not a gap the user can
// act on. "没有来源" is the shape of that emptiness, and it is refused with
// the rest of the proposal.
func TestResearchProposalRefusesAGapThatDoesNotSayWhatWouldFillIt(t *testing.T) {
	for name, raw := range map[string]string{
		"no expected evidence":    proposalWithExtras(`,"gaps":[{"description":"缺一篇给出反例的对照研究"}]`),
		"blank expected evidence": proposalWithExtras(`,"gaps":[{"description":"缺一篇给出反例的对照研究","expected_evidence":"  "}]`),
		"no description":          proposalWithExtras(`,"gaps":[{"expected_evidence":"一篇标注了反例的同行评议文章"}]`),
		"blank description":       proposalWithExtras(`,"gaps":[{"description":"  ","expected_evidence":"一篇标注了反例的同行评议文章"}]`),
		"empty gap":               proposalWithExtras(`,"gaps":[{}]`),
	} {
		if _, err := DecodeResearchProposal(json.RawMessage(raw), authorizedContext("source-1")); err == nil {
			t.Fatalf("a gap that does not say what would fill it was accepted: %s", name)
		}
	}
}

// The retrieval levels (候选证据 / 背景线索 / 发现入口 / 未读检索摘要) belong
// to the search path: they grade what a search produced. The candidate
// sources of a proposal are references, checked against the authorization of
// the turn. Keeping the two vocabularies apart is what stops a reference
// from looking like evidence the user can trust.
func TestResearchProposalDoesNotBorrowTheRetrievalLevelVocabulary(t *testing.T) {
	for _, literal := range stringLiterals(t, "research_proposal.go") {
		for _, level := range []string{"candidate_evidence", "background_lead", "discovery_entry", "unread_search_summary"} {
			if strings.Contains(literal, level) {
				t.Fatalf("the proposal contract speaks the retrieval level vocabulary %s: %s", level, literal)
			}
		}
	}
}

// A5/A6: the three states are told apart by the turn's own fields, not by a
// second reading of the stored turn. A turn that carries a proposal, a turn
// that carries none and stays a completed answer, and a turn whose proposal
// the server refused - which says so in its failure code while the answer
// stays.
func TestAStoredTurnSaysItsProposalStateThroughItsOwnFields(t *testing.T) {
	f := newAgentFixture(t, false)
	present := proposalTurn(t, f, AgentResearch, "pf5-state-present", &proposalModelStub{structured: json.RawMessage(legalProposalPayload), reply: "我按你的问题整理了一条检索计划，请核对后自行保存。"})
	if present.FailureCode != "" || strings.TrimSpace(present.Reply) == "" {
		t.Fatalf("a present proposal is not a completed answer: %+v", present)
	}
	if !proposalTravelled(t, present) {
		t.Fatal("a present proposal did not travel with its turn")
	}
	absent := proposalTurn(t, f, AgentResearch, "pf5-state-absent", &proposalModelStub{structured: json.RawMessage(`{"reply":"我这一轮没有给建议。"}`), reply: "我这一轮没有给建议。"})
	if absent.FailureCode != "" || strings.TrimSpace(absent.Reply) == "" {
		t.Fatalf("an absent proposal is not a completed answer: %+v", absent)
	}
	if proposalTravelled(t, absent) {
		t.Fatal("an absent proposal travelled with its turn")
	}
	refused := proposalTurn(t, f, AgentResearch, "pf5-state-refused", &proposalModelStub{structured: json.RawMessage(`{"reply":"我给了一条建议。","task_card":{"goal":"弄清合成材料","scope":"只查项目资料","queries":["合成材料 定义"],"expected_output":"候选资料"}}`), reply: "我给了一条建议。"})
	if refused.FailureCode != ResearchProposalFailureCode {
		t.Fatalf("a refused proposal did not say so: %+v", refused)
	}
	if strings.TrimSpace(refused.Reply) == "" {
		t.Fatalf("a refused proposal cost the user the answer: %+v", refused)
	}
	if proposalTravelled(t, refused) {
		t.Fatal("a refused proposal travelled with its turn")
	}
}

// proposalTravelled reports whether the turn's suggestions carry the proposal.
func proposalTravelled(t *testing.T, turn AgentTurn) bool {
	t.Helper()
	raw, err := researchProposalFromSuggestions(turn.Suggestions)
	if err != nil {
		t.Fatal(err)
	}
	return !ResearchProposalAbsent(raw)
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
	// The gap is part of the same shape, so it has one owner too. Its own
	// field names are partly common words - "description" could turn up in
	// another contract - but the pair is what the guard looks for, and
	// "expected_evidence" appears nowhere else in the server, so the pair
	// cannot be mistaken for another contract's shape.
	gap := ResearchProposalGapFieldSet()
	if len(gap) == 0 {
		t.Fatal("the contract declares no gap field")
	}
	gapCopies := proposalShapeCopies(t, gap)
	if len(gapCopies) != 1 || gapCopies[0] != "research_proposal.go:ResearchProposalGap" {
		t.Fatalf("the gap shape is declared more than once: %v", gapCopies)
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
	if names := jsonFieldNamesOf(ResearchProposalGap{}); len(names) != len(gap) {
		t.Fatalf("the gap names drifted from the shape: %v", names)
	}
	for _, name := range gap {
		if !jsonFieldNamesOf(ResearchProposalGap{})[name] {
			t.Fatalf("the gap shape does not declare %s", name)
		}
		if !strings.Contains(ResearchProposalShapeText(), name) {
			t.Fatalf("the rendered shape does not mention %s", name)
		}
	}
	// The two renderings are complementary: the task shape is the six
	// fields that make a research task, and the proposal shape is those
	// six plus the proposal's three additions. The assistant's card asks
	// for the first one only, so a rendering that grew the second's columns
	// would change what the assistant answers.
	task := ResearchProposalTaskShapeText()
	for _, name := range researchProposalTaskFields {
		if !strings.Contains(task, name) {
			t.Fatalf("the rendered task shape does not mention %s", name)
		}
	}
	for _, name := range researchProposalAdditionFields {
		if strings.Contains(task, name) {
			t.Fatalf("the rendered task shape carries the proposal's %s", name)
		}
	}
	proposal := ResearchProposalShapeText()
	for _, name := range researchProposalFields {
		if !strings.Contains(proposal, name) {
			t.Fatalf("the rendered proposal shape does not mention %s", name)
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
	if renderings := contractRendererCalls(t, adapter); renderings["ResearchProposalTaskShapeText"] < 1 || renderings["ResearchProposalShapeText"] < 1 {
		t.Fatalf("the prompts do not both render the contract's shape: %v", renderings)
	}
}

// ResearchProposalFieldSet is the field names the contract owns.
func ResearchProposalFieldSet() []string {
	return researchProposalFieldSet()
}

// ResearchProposalCandidateSourceFieldSet is the field names the candidate
// source shape owns.
func ResearchProposalCandidateSourceFieldSet() []string {
	return []string{researchProposalCandidateSourceIDField, researchProposalCandidateSourceReasonField}
}

// ResearchProposalGapFieldSet is the field names the gap shape owns.
func ResearchProposalGapFieldSet() []string {
	return []string{researchProposalGapDescriptionField, researchProposalGapExpectedEvidenceField}
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
// contract module instead of carrying its own copy of the shape. Both
// renderings count: the task shape for the assistant's card, the proposal
// shape for the research agent's card.
func contractRendererCalls(t *testing.T, path string) map[string]int {
	t.Helper()
	file, err := parser.ParseFile(token.NewFileSet(), path, nil, 0)
	if err != nil {
		t.Fatal(err)
	}
	calls := map[string]int{}
	ast.Inspect(file, func(node ast.Node) bool {
		call, ok := node.(*ast.CallExpr)
		if !ok {
			return true
		}
		selector, ok := call.Fun.(*ast.SelectorExpr)
		if ok && (selector.Sel.Name == "ResearchProposalShapeText" || selector.Sel.Name == "ResearchProposalTaskShapeText") {
			calls[selector.Sel.Name]++
		}
		return true
	})
	return calls
}

// proposalModelStub is a fake model: it answers with the structured output the
// case needs, so the turn endpoint can be exercised without a provider. It
// remembers what it was asked for, so a case can see the purpose the server
// derived.
type proposalModelStub struct {
	structured json.RawMessage
	reply      string
	calls      int
	purposes   []string
	agents     []string
}

func (m *proposalModelStub) Reply(_ context.Context, _ Actor, request ModelRequest) (ModelReply, error) {
	m.calls++
	m.purposes = append(m.purposes, request.Purpose)
	m.agents = append(m.agents, request.Agent)
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
	turn := proposalTurn(t, f, AgentResearch, "pf5-legal", stub)
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
//
// What is refused is the proposal, not the answer. The turn still completes
// and the reply the model wrote stays: a broken suggestion must not cost the
// user their answer. The turn says in a readable code that this turn carries
// no proposal.
func TestInvalidResearchProposalKeepsTheReplyAndLosesTheProposal(t *testing.T) {
	for name, payload := range map[string]string{
		"one query":     `{"reply":"我整理了一条检索计划。","task_card":{"goal":"弄清合成材料","scope":"只查项目资料","queries":["合成材料 定义"],"expected_output":"候选资料"}}`,
		"unknown field": `{"reply":"我整理了一条检索计划。","task_card":{"goal":"弄清合成材料","scope":"只查项目资料","queries":["a","b"],"expected_output":"候选资料","confidence":0.9}}`,
		"missing goal":  `{"reply":"我整理了一条检索计划。","task_card":{"scope":"只查项目资料","queries":["a","b"],"expected_output":"候选资料"}}`,
	} {
		f := newAgentFixture(t, false)
		stub := &proposalModelStub{structured: json.RawMessage(payload), reply: "我整理了一条检索计划。"}
		turn := proposalTurn(t, f, AgentResearch, "pf5-invalid", stub)
		if turn.Status != "completed" {
			t.Fatalf("%s: the broken proposal failed the whole turn: %+v", name, turn)
		}
		if turn.Reply != "我整理了一条检索计划。" {
			t.Fatalf("%s: the answer was lost with the proposal: %+v", name, turn)
		}
		if turn.FailureCode != ResearchProposalFailureCode {
			t.Fatalf("%s: the refusal is not readable: %+v", name, turn)
		}
		raw, err := researchProposalFromSuggestions(turn.Suggestions)
		if err != nil || !ResearchProposalAbsent(raw) {
			t.Fatalf("%s: the broken proposal still reached the interface: %s %v", name, raw, err)
		}
		assertProposalCreatedNothing(t, f)
	}
}

// A5: a turn with no proposal still answers the question. The model proposed
// nothing, and that is not a failure: the reply stays, the turn completes,
// and the turn carries no proposal.
func TestAbsentResearchProposalKeepsTheReplyAndCarriesNoProposal(t *testing.T) {
	f := newAgentFixture(t, false)
	reply := "授权的资料里还没有合成材料的定义，我按你给的范围说明了缺口。"
	stub := &proposalModelStub{structured: json.RawMessage(`{"reply":"` + reply + `"}`), reply: reply}
	turn := proposalTurn(t, f, AgentResearch, "pf5-absent", stub)
	if turn.Status != "completed" || turn.FailureCode != "" {
		t.Fatalf("a turn without a proposal was treated as a failure: %+v", turn)
	}
	if turn.Reply != reply {
		t.Fatalf("the answer was lost: %+v", turn)
	}
	raw, err := researchProposalFromSuggestions(turn.Suggestions)
	if err != nil || !ResearchProposalAbsent(raw) {
		t.Fatalf("the turn carries a proposal it was never given: %s %v", raw, err)
	}
	assertProposalCreatedNothing(t, f)
}

// The assistant's editable task suggestion is a different card with its own
// rules: this contract does not change what the assistant may answer.
//
// A16: the assistant's answer still completes, still carries what the model
// said, and is not judged as a research proposal - the contract leaves the
// assistant's card alone, the way it did before the proposal arrived.
func TestAssistantTaskSuggestionIsNotJudgedByTheProposalContract(t *testing.T) {
	f := newAgentFixture(t, false)
	payload := `{"reply":"我给了一条建议。","task_card":{"goal":"弄清合成材料","scope":"只查项目资料","queries":["合成材料 定义"],"expected_output":"候选资料"}}`
	stub := &proposalModelStub{structured: json.RawMessage(payload), reply: "我给了一条建议。"}
	turn := proposalTurn(t, f, AgentTaskAssistant, "pf5-assistant", stub)
	if turn.Status != "completed" {
		t.Fatalf("the assistant's answer changed: %+v", turn)
	}
	if turn.FailureCode != "" {
		t.Fatalf("the assistant's answer was refused by the proposal contract: %+v", turn)
	}
	raw, err := researchProposalFromSuggestions(turn.Suggestions)
	if err != nil || ResearchProposalAbsent(raw) {
		t.Fatalf("the assistant's own suggestion was taken away: %s %v", raw, err)
	}
}

// A7: a proposal whose candidate sources all belong to the turn's own
// authorized context reaches the interface with each reason beside its
// source, and creates nothing by itself.
func TestResearchProposalCarriesItsCandidateSourcesToTheTurn(t *testing.T) {
	f := newAgentFixture(t, false)
	payload := `{"reply":"我按你的问题整理了一条检索计划。","task_card":{"goal":"弄清合成材料的定义","scope":"只查项目资料","queries":["合成材料 定义","合成材料 反例"],"expected_output":"候选资料与反例","candidate_sources":[{"source_id":"source-1","reason":"它写明了合成材料的定义"}]}}`
	stub := &proposalModelStub{structured: json.RawMessage(payload), reply: "我按你的问题整理了一条检索计划。"}
	turn := proposalTurn(t, f, AgentResearch, "pf5-sources", stub)
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

// A13: the reasons and the gaps travel with the turn they belong to, beside
// the editable draft - and they create nothing by themselves.
func TestResearchProposalCarriesItsReasonsAndGapsToTheTurn(t *testing.T) {
	f := newAgentFixture(t, false)
	payload := `{"reply":"我按你的问题整理了一条检索计划。","task_card":{"goal":"弄清合成材料的定义","scope":"只查项目资料","queries":["合成材料 定义","合成材料 反例"],"expected_output":"候选资料与反例","reasons":["目标定成「定义」是因为用户问的就是这个词本身","范围只划到项目资料是因为公开综述尚未授权"],"gaps":[{"description":"缺一篇给出反例的对照研究","expected_evidence":"一篇标注了反例的同行评议文章"}]}}`
	stub := &proposalModelStub{structured: json.RawMessage(payload), reply: "我按你的问题整理了一条检索计划。"}
	turn := proposalTurn(t, f, AgentResearch, "pf5-reasons", stub)
	if turn.Status != "completed" {
		t.Fatalf("the turn did not complete: %+v", turn)
	}
	proposal := proposalOfTurn(t, turn)
	if len(proposal.Reasons) != 2 || proposal.Reasons[1] != "范围只划到项目资料是因为公开综述尚未授权" {
		t.Fatalf("the reasons did not travel with the turn: %+v", proposal.Reasons)
	}
	if len(proposal.Gaps) != 1 {
		t.Fatalf("the gaps did not travel with the turn: %+v", proposal.Gaps)
	}
	if proposal.Gaps[0].Description != "缺一篇给出反例的对照研究" || proposal.Gaps[0].ExpectedEvidence != "一篇标注了反例的同行评议文章" {
		t.Fatalf("a gap lost one of its two parts on the way: %+v", proposal.Gaps[0])
	}
	assertProposalCreatedNothing(t, f)
}

// A13: a proposal whose reason or gap is overlong - or whose gap does not
// say what would fill it - never reaches the interface: the refusal is
// readable, nothing is written, and no proposal shows up beside the draft.
// The answer itself stays.
func TestResearchProposalWithOverlongReasonOrGapIsRefusedWithoutLosingTheAnswer(t *testing.T) {
	for name, payload := range map[string]string{
		"overlong reason":                `{"reply":"我整理了一条检索计划。","task_card":{"goal":"弄清合成材料","scope":"只查项目资料","queries":["a","b"],"expected_output":"候选资料","reasons":["` + strings.Repeat("长", 2000) + `"]}}`,
		"overlong gap":                   `{"reply":"我整理了一条检索计划。","task_card":{"goal":"弄清合成材料","scope":"只查项目资料","queries":["a","b"],"expected_output":"候选资料","gaps":[{"description":"缺一篇反例","expected_evidence":"` + strings.Repeat("长", 2000) + `"}]}}`,
		"gap without what would fill it": `{"reply":"我整理了一条检索计划。","task_card":{"goal":"弄清合成材料","scope":"只查项目资料","queries":["a","b"],"expected_output":"候选资料","gaps":[{"description":"没有来源"}]}}`,
	} {
		f := newAgentFixture(t, false)
		stub := &proposalModelStub{structured: json.RawMessage(payload), reply: "我整理了一条检索计划。"}
		turn := proposalTurn(t, f, AgentResearch, "pf5-overlong", stub)
		assertProposalRefused(t, name, turn, "我整理了一条检索计划。")
		assertProposalCreatedNothing(t, f)
	}
}

// A7: a proposal that names a source this turn was never given is refused
// whole: the refusal is readable, nothing is written, and the source never
// reaches the interface. The answer the model gave stays.
func TestResearchProposalCitingASourceOutsideTheTurnIsRefused(t *testing.T) {
	for name, payload := range map[string]string{
		"another source of the same project": `{"reply":"我整理了一条检索计划。","task_card":{"goal":"弄清合成材料","scope":"只查项目资料","queries":["a","b"],"expected_output":"候选资料","candidate_sources":[{"source_id":"source-2","reason":"它看起来相关"}]}}`,
		"an invented source":                 `{"reply":"我整理了一条检索计划。","task_card":{"goal":"弄清合成材料","scope":"只查项目资料","queries":["a","b"],"expected_output":"候选资料","candidate_sources":[{"source_id":"made-up","reason":"它看起来相关"}]}}`,
		"one good and one unreadable":        `{"reply":"我整理了一条检索计划。","task_card":{"goal":"弄清合成材料","scope":"只查项目资料","queries":["a","b"],"expected_output":"候选资料","candidate_sources":[{"source_id":"source-1","reason":"可读"},{"source_id":"source-3","reason":"不可读"}]}}`,
	} {
		f := newAgentFixture(t, false)
		stub := &proposalModelStub{structured: json.RawMessage(payload), reply: "我整理了一条检索计划。"}
		turn := proposalTurn(t, f, AgentResearch, "pf5-outside", stub)
		assertProposalRefused(t, name, turn, "我整理了一条检索计划。")
		assertProposalCreatedNothing(t, f)
	}
}

// A8: a source whose permission was taken away never reaches the model, so
// the turn is refused before the model is asked - and a proposal that names it
// is refused as well.
//
// The stub is installed as the turn's model, so "the model was not asked" is
// a measurement of this turn and not a tautology: a stub that was never
// plugged in would count zero calls whatever the server did.
func TestResearchProposalCitingARevokedSourceIsRefused(t *testing.T) {
	f := newAgentFixture(t, false)
	raw, _, _, err := f.svc.CreateAgentSession(context.Background(), f.owner, f.project.ID, AgentResearch, "pf5-revoked-session", f.expectation(0))
	if err != nil {
		t.Fatal(err)
	}
	session := decodeAgentJSON[AgentSession](t, raw, err)
	source := f.source.sources["source-1"]
	source.ModelUseAllowed = false
	f.source.sources["source-1"] = source
	stub := &proposalModelStub{structured: json.RawMessage(legalProposalPayload), reply: "我整理了一条检索计划。"}
	f.svc.agentRuntime.Model = stub
	if f.svc.agentRuntime.Model != stub {
		t.Fatal("the stub is not the model this turn would ask, so its call count would prove nothing")
	}
	// The session exists and only the revoked source stands between this turn
	// and the model: so "the model was not asked" is a measurement of the
	// consent check, not of some earlier failure.
	_, _, _, err = f.svc.SendAgentTurn(context.Background(), f.owner, f.project.ID, session.ID, "pf5-revoked-turn", AgentTurnInput{AgentContextInput: AgentContextInput{VersionExpectation: f.expectation(0), SourceIDs: []string{"source-1"}, SensitiveContextConfirmed: true}, UserInput: "帮我找合成材料的资料"})
	if stub.calls != 0 {
		t.Fatalf("the model was asked about a revoked source: %d calls", stub.calls)
	}
	if !errors.Is(err, ErrConsentRequired) {
		t.Fatalf("a revoked source was accepted into the turn's context: %v", err)
	}
}

// A8: revocation reaches a stored proposal where the material is actually
// used - the research task the proposal became. The run re-checks the
// material scope before every query round, so a source whose permission was
// taken away stops the run instead of being read.
//
// This is where revocation actually takes effect for a stored proposal. The
// stored turn keeps the proposal it was given; what stops being readable is
// the material, at the point where somebody wants to use it.
func TestRevokedSourceStopsTheRunOfAProposalThatBecameAResearchTask(t *testing.T) {
	f := newAgentFixture(t, false)
	seedNativeEvidence(t, f)
	raw, _, _, err := f.svc.SaveResearchTask(context.Background(), f.owner, f.project.ID, "pf5-revoked-task", SaveResearchTaskInput{VersionExpectation: f.expectation(0), Goal: "弄清合成材料", Scope: "只查项目资料", Queries: []string{"定义", "限制"}, ProviderID: "synthetic-provider", AssetIDs: []string{"asset-1"}})
	if err != nil {
		t.Fatal(err)
	}
	task := decodeAgentJSON[ResearchTask](t, raw, err)
	raw, _, _, err = f.svc.ApproveResearchTask(context.Background(), f.owner, f.project.ID, task.ID, "pf5-revoked-task-approve", ConfirmationInput{VersionExpectation: f.expectation(task.Version), Confirmed: true, Reason: "人工确认合成范围"})
	if err != nil {
		t.Fatal(err)
	}
	task = decodeAgentJSON[ResearchTask](t, raw, err)
	// The permission is taken away after the task was saved and approved.
	f.source.denied["source-1"] = true
	raw, _, _, err = f.svc.RunResearchTask(context.Background(), f.owner, f.project.ID, task.ID, "pf5-revoked-task-run", RunResearchTaskInput{VersionExpectation: f.expectation(task.Version)})
	if err != nil {
		t.Fatal(err)
	}
	run := decodeAgentJSON[ResearchRun](t, raw, err)
	if run.Status != "stale" || run.FailureCode != "asset_scope_changed" {
		t.Fatalf("a revoked source did not stop the run: %+v", run)
	}
	if f.search.calls != 0 {
		t.Fatalf("the search ran over a revoked source: %d calls", f.search.calls)
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
	turn := proposalTurn(t, f, AgentResearch, "pf5-cross-project", stub)
	assertProposalRefused(t, "another project's source", turn, "我整理了一条检索计划。")
	assertProposalCreatedNothing(t, f)
}

// assertProposalRefused checks the shape of a turn whose proposal the
// contract refused: the turn completes, the answer the model gave stays, the
// proposal does not reach the interface, and the refusal is readable.
func assertProposalRefused(t *testing.T, name string, turn AgentTurn, reply string) {
	t.Helper()
	if turn.Status != "completed" {
		t.Fatalf("%s: the broken proposal failed the whole turn: %+v", name, turn)
	}
	if turn.Reply != reply {
		t.Fatalf("%s: the answer was lost with the proposal: %+v", name, turn)
	}
	if turn.FailureCode != ResearchProposalFailureCode {
		t.Fatalf("%s: the refusal is not readable: %+v", name, turn)
	}
	raw, err := researchProposalFromSuggestions(turn.Suggestions)
	if err != nil || !ResearchProposalAbsent(raw) {
		t.Fatalf("%s: the refused proposal still reached the interface: %s %v", name, raw, err)
	}
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

// A3: the purpose of a turn is the server's to decide. The session declares
// its agent, and the shape of the answer follows from it: the research agent
// asks for a research proposal, the task assistant keeps the conversation it
// has always had. The client states no purpose, so it cannot ask for another
// shape.
func TestAgentPurposeIsDerivedFromTheSessionAgent(t *testing.T) {
	f := newAgentFixture(t, false)
	research := &proposalModelStub{structured: json.RawMessage(legalProposalPayload), reply: "我按你的问题整理了一条检索计划，请核对后自行保存。"}
	proposalTurn(t, f, AgentResearch, "pf5-purpose-research", research)
	if len(research.purposes) != 1 || research.purposes[0] != AgentPurposeResearch {
		t.Fatalf("the research agent's turn did not ask for a research proposal: %v", research.purposes)
	}
	if len(research.agents) != 1 || research.agents[0] != AgentResearch {
		t.Fatalf("the turn did not carry the session's agent: %v", research.agents)
	}
	assistant := &proposalModelStub{structured: json.RawMessage(`{"reply":"当前没有阻断。"}`), reply: "当前没有阻断。"}
	proposalTurn(t, f, AgentTaskAssistant, "pf5-purpose-assistant", assistant)
	if len(assistant.purposes) != 1 || assistant.purposes[0] != AgentPurposeConversation {
		t.Fatalf("the assistant's turn did not keep the conversation shape: %v", assistant.purposes)
	}
}

// A3: the request shape does not move, so the idempotency digest does not
// move either. A key issued before the purpose was derived still replays, and
// the replay does not ask the model again.
func TestAgentTurnRequestDigestIsUnchangedAndAnIssuedKeyStillReplays(t *testing.T) {
	input := AgentTurnInput{
		AgentContextInput: AgentContextInput{
			VersionExpectation:        VersionExpectation{ExpectedProjectVersion: 1, ExpectedContextRevision: 1, ExpectedVersion: 1},
			SourceIDs:                 []string{"source-1"},
			CardRefs:                  []VersionRef{},
			SensitiveContextConfirmed: true,
		},
		UserInput: "帮我找合成材料的资料",
	}
	encoded, err := json.Marshal(input)
	if err != nil {
		t.Fatal(err)
	}
	// The client states no purpose and no agent: the request shape is the
	// one the digest was computed over before the purpose was derived.
	if strings.Contains(string(encoded), "purpose") || strings.Contains(string(encoded), "agent") {
		t.Fatalf("the turn request grew a field: %s", encoded)
	}
	digest, err := operationcore.RequestHash(input)
	if err != nil {
		t.Fatal(err)
	}
	// The digest is pinned to the value the request had before the purpose
	// was derived: the struct the pipeline hashes is byte for byte the one
	// it hashed then, so a key issued under the old rule still matches.
	if digest != "481e6ee77587dbd8885b26da068a1bc0eb42d6b0602880a020b1f9465c0c930f" {
		t.Fatalf("the turn request digest moved: %s", digest)
	}

	f := newAgentFixture(t, false)
	stub := &proposalModelStub{structured: json.RawMessage(legalProposalPayload), reply: "我按你的问题整理了一条检索计划，请核对后自行保存。"}
	f.svc.agentRuntime.Model = stub
	raw, _, _, err := f.svc.CreateAgentSession(context.Background(), f.owner, f.project.ID, AgentResearch, "pf5-replay-session", f.expectation(0))
	session := decodeAgentJSON[AgentSession](t, raw, err)
	turnInput := AgentTurnInput{AgentContextInput: AgentContextInput{VersionExpectation: f.expectation(0), SourceIDs: []string{"source-1"}, SensitiveContextConfirmed: true}, UserInput: "帮我找合成材料的资料"}
	first, _, _, err := f.svc.SendAgentTurn(context.Background(), f.owner, f.project.ID, session.ID, "pf5-replay-turn", turnInput)
	if err != nil {
		t.Fatal(err)
	}
	second, _, replay, err := f.svc.SendAgentTurn(context.Background(), f.owner, f.project.ID, session.ID, "pf5-replay-turn", turnInput)
	if err != nil {
		t.Fatal(err)
	}
	if !replay {
		t.Fatal("an issued key did not replay")
	}
	firstTurn := decodeAgentJSON[AgentTurn](t, first, nil)
	secondTurn := decodeAgentJSON[AgentTurn](t, second, nil)
	if firstTurn.ID != secondTurn.ID || firstTurn.Version != secondTurn.Version {
		t.Fatalf("the replay is not the same turn: %+v %+v", firstTurn, secondTurn)
	}
	if stub.calls != 1 {
		t.Fatalf("the model was asked again on a replay: %d calls", stub.calls)
	}
	turns, err := f.svc.GetAgentTurns(context.Background(), f.owner, f.project.ID, session.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(turns) != 1 {
		t.Fatalf("the replay wrote a second turn: %d turns", len(turns))
	}
}

// The words the server owns: the agent a session declares and the shape an
// answer takes. They live in the server's constants, and nothing else spells
// them when it decides what an answer is.
var agentVocabularyWords = []string{
	AgentTaskAssistant,
	AgentResearch,
	AgentConstraint,
	AgentPurposeConversation,
	AgentPurposeRuleSuggestion,
	AgentPurposeResearch,
}

// A18: the agent and purpose vocabulary has one owner. The adapter and the
// service refer to the server's constants instead of comparing loose
// strings: a prompt that branches on a misspelled word loses its
// instructions without anybody noticing.
//
// The scan is narrow on purpose. It looks at comparisons and at assignments
// to an agent or a purpose, so a word that is common elsewhere - the import
// candidate's own "conversation" selection type, the provenance source type
// - is not mistaken for the vocabulary.
func TestAgentVocabularyHasOneOwner(t *testing.T) {
	for _, path := range vocabularyFiles(t) {
		for _, hit := range vocabularyComparisons(t, path) {
			t.Fatalf("the vocabulary is decided by a loose string: %s", hit)
		}
	}
}

// vocabularyFiles lists the server's own Go files. Test files are left out:
// a case may name the words it is about.
func vocabularyFiles(t *testing.T) []string {
	t.Helper()
	files := []string{}
	err := filepath.WalkDir("..", func(path string, entry os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if entry.IsDir() || !strings.HasSuffix(path, ".go") || strings.HasSuffix(path, "_test.go") {
			return nil
		}
		files = append(files, path)
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	return files
}

// vocabularyComparisons finds every place the server decides an agent or a
// purpose by comparing it against a loose string.
func vocabularyComparisons(t *testing.T, path string) []string {
	t.Helper()
	file, err := parser.ParseFile(token.NewFileSet(), path, nil, 0)
	if err != nil {
		t.Fatal(err)
	}
	hits := []string{}
	report := func(place string, other ast.Expr) {
		hits = append(hits, fmt.Sprintf("%s: %s %s", path, place, expressionName(other)))
	}
	ast.Inspect(file, func(node ast.Node) bool {
		switch typed := node.(type) {
		case *ast.BinaryExpr:
			if typed.Op != token.EQL && typed.Op != token.NEQ {
				return true
			}
			if vocabularyLiteral(typed.X) && mentionsAgentOrPurpose(typed.Y) {
				report("compares", typed.Y)
			}
			if vocabularyLiteral(typed.Y) && mentionsAgentOrPurpose(typed.X) {
				report("compares", typed.X)
			}
		case *ast.KeyValueExpr:
			if vocabularyLiteral(typed.Value) && mentionsAgentOrPurpose(typed.Key) {
				report("assigns", typed.Key)
			}
		case *ast.SwitchStmt:
			if !mentionsAgentOrPurpose(typed.Tag) {
				return true
			}
			for _, statement := range typed.Body.List {
				clause, ok := statement.(*ast.CaseClause)
				if !ok {
					continue
				}
				for _, option := range clause.List {
					if vocabularyLiteral(option) {
						report("switches on", typed.Tag)
					}
				}
			}
		}
		return true
	})
	return hits
}

// vocabularyLiteral reports whether an expression is one of the vocabulary
// words.
func vocabularyLiteral(node ast.Expr) bool {
	literal, ok := node.(*ast.BasicLit)
	if !ok || literal.Kind != token.STRING {
		return false
	}
	value, err := strconv.Unquote(literal.Value)
	if err != nil {
		return false
	}
	for _, word := range agentVocabularyWords {
		if value == word {
			return true
		}
	}
	return false
}

// mentionsAgentOrPurpose reports whether an expression names an agent or a
// purpose. The name is what the reader sees - the field or the variable.
func mentionsAgentOrPurpose(node ast.Expr) bool {
	name := strings.ToLower(expressionName(node))
	return strings.Contains(name, "agent") || strings.Contains(name, "purpose")
}

// expressionName names an expression the way a reader would: the identifier
// or the field it ends in.
func expressionName(node ast.Expr) string {
	switch typed := node.(type) {
	case *ast.Ident:
		return typed.Name
	case *ast.SelectorExpr:
		return typed.Sel.Name
	case *ast.CallExpr:
		return expressionName(typed.Fun)
	case *ast.IndexExpr:
		return expressionName(typed.X)
	case *ast.ParenExpr:
		return expressionName(typed.X)
	}
	return ""
}
