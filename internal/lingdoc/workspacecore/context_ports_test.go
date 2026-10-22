package workspacecore

import (
	"go/ast"
	"go/parser"
	"go/token"
	"path/filepath"
	"sort"
	"strings"
	"testing"
)

// narrowPortMethods pins which methods each narrow port carries.
//
// The ports in context_ports.go are the contract that the transport
// layer consumes instead of the aggregate. A port is only allowed to be a
// *narrowing* of the aggregate: every method it names must exist on the
// aggregate with the very same signature, and the port must not quietly grow
// a method that no consumer asked for.
//
// The table below is the promise. Adding a method to a port means someone
// decided that the context owns another operation; that decision has to be
// written down here, and it has to come with the aggregate method it names.
var narrowPortMethods = map[string][]string{
	"AgentActionApplication": {
		"ApplyChangeSet",
		"CommitWorkingCopy",
		"ConfirmEvidenceCard",
		"CreateChangeDraft",
		"PrepareAgentChangeDraft",
		"SaveEvidenceCard",
		"SaveSpec",
	},
	"GovernanceReviewApplication": {
		"AuthorizeInTransaction",
		"ValidationIssueDispositionsInTransaction",
	},
	"AgentContextApplication": {
		"BuildAgentContext",
		"GetProjectAssetPolicy",
	},
	"ChangeSetDraftApplication": {
		"AssessChangeSet",
		"CreateChangeDraft",
		"EditChangeDraft",
		"ReverseChangeSet",
		"StabilizeRevision",
	},
	"ImpactApplication": {
		"AcknowledgeImpactPreflight",
		"ActOnImpactTask",
		"GetImpactAssessment",
		"ListContextRevisions",
		"ListImpactTasks",
		"ListRouteNodes",
		"ReadImpactComputationReceipt",
	},
	"DependencyApplication": {
		"CreateDependencyEdge",
		"DecideDependencyCandidate",
		"DecideDependencyEdge",
		"ListDependencyCandidates",
		"ListDependencyEdges",
		"SubmitDependencyCandidateBatch",
	},
	"RuntimeApplication": {
		"ActOnRuntimeAlertRule",
		"ControlRuntime",
		"GetRuntimeMetrics",
		"ListRuntimeAlertRules",
		"ListRuntimeAlerts",
		"ListRuntimeControls",
		"ListRuntimeEvents",
		"SaveRuntimeAlertRule",
	},
	"TrialApplication": {
		"ActOnTrialWindow",
		"CreateTrialDecision",
		"CreateTrialWindow",
		"GetTrialReport",
		"ListTrialDecisions",
		"ListTrialSamples",
		"ListTrialWindows",
		"RecordTrialSample",
		"ReviewTrialSample",
		"SignTrialDecision",
	},
	"RoadmapApplication": {
		"BeginRoadmapEdit",
		"CancelRoadmapEdit",
		"CommitRoadmapEdit",
		"CompareRoadmapNodeVersions",
		"GetRoadmapSession",
		"ListRoadmapSessions",
		"ProposeRoadmapEdge",
		"ProposeRoadmapNode",
		"ReadRoadmap",
		"ReadRoadmapNodeVersions",
		"ReadRouteEdgeVersions",
		"ReadRouteEdges",
	},
	"ResearchTaskApplication": {
		"ApproveResearchTask",
		"ControlResearchTask",
		"GetResearchRun",
		"InspectResearchResult",
		"ListResearchRuns",
		"ListResearchTasks",
		"RunResearchTask",
		"SaveResearchTask",
	},
	"ImportCandidateApplication": {
		"ConfirmImportCandidate",
		"CreateImportCandidate",
		"ListImportCandidates",
		"RefreshImportCandidate",
		"RejectImportCandidate",
	},
	"EvidenceCardApplication": {
		"ConfirmEvidenceCard",
		"ListEvidenceCards",
		"ReviewEvidenceCard",
		"SaveEvidenceCard",
	},
	"ResearchClaimApplication": {
		"ListResearchClaims",
		"SaveResearchClaim",
	},
	"SourceAnchorApplication": {
		"ListSourceAnchors",
		"SaveSourceAnchor",
	},
	"AgentSessionApplication": {
		"CreateAgentSession",
		"GetAgentTurns",
		"ListAgentSessions",
		"SendAgentTurn",
	},
	"ActionProposalApplication": {
		"ConfirmActionProposal",
		"CreateActionProposal",
		"ListActionProposals",
		"RejectActionProposal",
	},
	"ChapterReviewApplication": {
		"ChangeChapterReview",
		"GetChapterReview",
		"ListChapterComments",
		"SaveChapterComment",
	},
	"DeliveryGuardApplication": {
		"AuthorizeDeliveryExport",
		"PrepareDeliveryExportNativeBasis",
		"ValidateDeliveryExportNativeBasisInTransaction",
	},
	"SourceProvenanceApplication": {
		"PrepareFrozenProjectProvenance",
		"PrepareProjectProvenance",
		"ValidateFrozenProjectProvenanceInTransaction",
	},
	"ImpactGuardApplication": {
		"AssertRevisionStabilized",
		"ReadImpactComputationReceipt",
		"ReadPreflightBasis",
		"StoreImpactComputationReceipt",
	},
	"RuntimeGuardApplication": {
		"CheckRuntimeOperation",
		"ObserveDeliveryRuntime",
		"ObserveRuntime",
	}, "ProjectApplication": {
		"ActivateProjectResult",
		"ActivationDiff",
		"CreateDraftCandidateResult",
		"CreateProjectResult",
		"DiscardProjectResult",
		"GetProject",
		"ListAuditEvents",
		"ListDraftCandidates",
		"ListProjects",
		"RestoreProjectResult",
		"SaveSpecResult",
	},
	"ChapterApplication": {
		"ApplyRewriteResult",
		"CommitWorkingCopyResult",
		"GetWorkingCopy",
		"ListChapterVersions",
		"ListChapters",
		"RestoreWorkingCopyResult",
		"SaveChapterResult",
		"SaveWorkingCopyResult",
	},
	"TemplateApplication": {
		"BindTemplateCopyRuleSetResult",
		"ChangeTemplateResult",
		"GetTemplateCopy",
		"PreviewTemplateCopyEdit",
		"PreviewTemplateMigration",
		"SaveTemplateCopyResult",
	},
	"ChangeSetApplication": {
		"ApplyChangeSetResult",
		"CreateChangeSetResult",
		"GetChangeSet",
		"ListChangeSets",
		"ReassessChangeSetResult",
		"RejectChangeSetResult",
	},
	"ProjectLifecycleApplication": {
		"ListDiscardedProjects",
		"RecordActivationRead",
	},
	"CapabilityAuthorizer": {
		"Authorize",
	},
	"GenerationContextApplication": {
		"GenerationContext",
	},
	"ValidationIssueDispositionApplication": {
		"RecordValidationIssueDispositionResult",
		"ReplayValidationIssueDispositionResult",
		"ValidationIssueDispositions",
	},
	"RuntimeCompositionApplication": {
		"SetActionExecutor",
		"SetImpactPreflightChecker",
		"SetRuntimeRecoveryChecker",
	},
	"ProjectProvenanceApplication": {
		"AssertFrozenProjectProvenance",
		"PrepareProjectProvenance",
		"ValidateProjectProvenanceSnapshot",
	},
	"EvidenceSelectionApplication": {
		"SelectGenerationEvidence",
	},
	"EvidenceSelectionTransactionApplication": {
		"SelectGenerationEvidenceInTransaction",
	},
	"EvidenceProofPreparationApplication": {
		"PrepareCandidateGenerationEvidence",
	},
}

// A narrow port may only name operations that belong to one context. The
// list below is the set of operations that are known to cross context
// boundaries when they are put into a port: a port that carries one of them
// is not a context port, and the transport would be able to reach across
// again.
var crossContextOperations = map[string]string{
	"AuthorizeInTransaction":       "授权上下文，同时被治理与影响使用",
	"ReadImpactComputationReceipt": "影响上下文，同时被影响中心与守卫使用",
	"GetProjectAssetPolicy":        "项目资料策略，G2 与治理运行时共用",
	"Authorize":                    "授权上下文，同时被项目、资料、生成与调研使用",
}

// TestEveryNarrowPortMethodMatchesTheAggregateMethodVerbatim is the promise
// that a narrow port is a narrowing and nothing else.
//
// The aggregate owns the behaviour. The port owns only the question "which
// operations may this transport layer reach". If a port's signature drifts
// from the aggregate's, the port has started to describe a second truth, and
// the two will drift apart silently: the port would compile against a
// different contract than the one the aggregate actually offers.
//
// The check is textual, on the source, not on the compiled type: it compares
// the parameter and result text of the port's method against the aggregate
// method of the same name.
func TestEveryNarrowPortMethodMatchesTheAggregateMethodVerbatim(t *testing.T) {
	directory := packageDirectory(t)
	aggregate := aggregateMethodSignatures(t, directory)
	declared := portMethodSignatures(t, directory)

	for name, expected := range narrowPortMethods {
		methods, ok := declared[name]
		if !ok {
			t.Errorf("the port %s is not declared in the narrow port files", name)
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
			origin, ok := aggregate[method]
			if !ok {
				t.Errorf("%s.%s is not an aggregate operation any more; drop it from the port or move the operation back", name, method)
				continue
			}
			if signature != origin {
				t.Errorf("%s.%s is declared as %q, the aggregate declares %q; a narrow port may only narrow, never restate", name, method, signature, origin)
			}
		}
	}

	// A port that is not in the table is a port nobody decided on.
	for name := range declared {
		if _, ok := narrowPortMethods[name]; !ok {
			t.Errorf("the port %s is declared but not recorded in narrowPortMethods", name)
		}
	}
}

// TestTheNarrowPortsDoNotHandTheTransportTheWholeAggregate is the
// structural half of the promise.
//
// A port that carried every aggregate operation would be the old wide port
// under a new name: the transport could still reach anything. The ports
// together are allowed to cover the aggregate's operations - different
// contexts need different operations - but no single port may reach across
// a context boundary, and no port may be a renaming of the whole aggregate.
func TestTheNarrowPortsDoNotHandTheTransportTheWholeAggregate(t *testing.T) {
	directory := packageDirectory(t)
	declared := portMethodSignatures(t, directory)
	aggregate := aggregateMethodSignatures(t, directory)

	widest := 0
	for name, methods := range declared {
		if len(methods) > widest {
			widest = len(methods)
		}
		for method := range methods {
			origin, ok := aggregate[method]
			if !ok {
				t.Errorf("the port %s names %s, which is not an aggregate operation", name, method)
				continue
			}
			if signature, ok := methods[method]; ok && signature != origin {
				t.Errorf("the port %s restates %s", name, method)
			}
		}
	}

	// The aggregate has hundreds of operations. A port that reaches even a
	// tenth of them is the wide port again.
	if len(aggregate) > 100 && widest*10 > len(aggregate) {
		t.Errorf("the widest narrow port carries %d operations while the aggregate has %d; a narrow port must be a narrow one", widest, len(aggregate))
	}

	// The documented cross-context operations must be recorded, so that a
	// reader who finds one in a port knows it was noticed.
	for method, why := range crossContextOperations {
		holders := []string{}
		for name, methods := range declared {
			if _, ok := methods[method]; ok {
				holders = append(holders, name)
			}
		}
		if len(holders) == 0 {
			continue
		}
		sort.Strings(holders)
		t.Logf("the cross-context operation %s appears in %v (%s)", method, holders, why)
	}
}

// narrowPortFiles are the files that declare the context narrow ports.
//
// The package also holds the store and transaction ports the aggregate
// consumes; those are not narrowings of the aggregate, so they are not part
// of this promise.
var narrowPortFiles = []string{"context_ports.go", "migrated_context_ports.go"}

// aggregateMethodSignatures reads the aggregate's method signatures from the
// package source, so the comparison is against the code that actually
// exists rather than against a compiled type.
func aggregateMethodSignatures(t *testing.T, directory string) map[string]string {
	t.Helper()
	signatures := map[string]string{}
	entries, err := filepath.Glob(filepath.Join(directory, "*.go"))
	if err != nil {
		t.Fatalf("list the package sources: %v", err)
	}
	for _, path := range entries {
		if strings.HasSuffix(path, "_test.go") {
			continue
		}
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
			signatures[function.Name.Name] = normalizeSignature(function)
		}
	}
	return signatures
}

// portMethodSignatures reads the method sets of the declared ports.
// portMethodSignatures reads the method sets of the declared ports.
//
// The narrow ports live in more than one file: the ones written when the
// narrow ports were introduced, and the ones that arrived with a context that
// was cut out of the aggregate. So the files that declare them are listed
// explicitly rather than guessed.
func portMethodSignatures(t *testing.T, directory string) map[string]map[string]string {
	t.Helper()
	ports := map[string]map[string]string{}
	for _, name := range narrowPortFiles {
		file, err := parser.ParseFile(token.NewFileSet(), filepath.Join(directory, name), nil, 0)
		if err != nil {
			t.Fatalf("parse %s: %v", name, err)
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
	}
	return ports
}

// normalizeSignature renders a function's parameter and result text with the
// whitespace collapsed, so that a reformatting is not mistaken for a change.
func normalizeSignature(function *ast.FuncDecl) string {
	return normalizeFuncType(function.Type)
}

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
// names.
//
// The names are not part of the contract: Go satisfies an interface by the
// types, so a port may name its parameters differently from the aggregate.
// The ports that arrived with a cut-out context write unnamed parameters
// where the aggregate names them; comparing the names would report a
// difference that means nothing.
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
