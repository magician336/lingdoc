package workspacecore

import (
	"encoding/json"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"testing"
)

func TestChangeSetStatusVocabulary(t *testing.T) {
	want := []string{"draft", "assessed", "applied", "stabilized", "rejected", "stale", "satisfied"}
	if !slices.Equal(ChangeSetStatuses, want) {
		t.Fatalf("ChangeSetStatuses = %v, want %v", ChangeSetStatuses, want)
	}
	for _, status := range want {
		if !IsChangeSetStatus(status) {
			t.Fatalf("IsChangeSetStatus(%q) = false", status)
		}
	}
	if IsChangeSetStatus("unknown") || IsChangeSetStatus("") {
		t.Fatal("IsChangeSetStatus accepted a status outside the vocabulary")
	}
}

func TestChangeSetClosedStatuses(t *testing.T) {
	// The closed loop is "applied" and "stabilized" only. A stale proposal has
	// lost its basis, a satisfied proposal never had work to do, and a rejected
	// proposal is a decision about the proposal — none of them closed the loop.
	want := []string{"applied", "stabilized"}
	if !slices.Equal(ChangeSetClosedStatuses, want) {
		t.Fatalf("ChangeSetClosedStatuses = %v, want %v", ChangeSetClosedStatuses, want)
	}
	for _, status := range []string{"stale", "satisfied", "rejected", "draft", "assessed"} {
		if IsClosedChangeSetStatus(status) {
			t.Fatalf("IsClosedChangeSetStatus(%q) = true, want false", status)
		}
	}
	for _, status := range want {
		if !IsClosedChangeSetStatus(status) {
			t.Fatalf("IsClosedChangeSetStatus(%q) = false, want true", status)
		}
	}
}

func TestChangeSetProposalStatuses(t *testing.T) {
	want := []string{"draft", "assessed"}
	if !slices.Equal(ChangeSetProposalStatuses, want) {
		t.Fatalf("ChangeSetProposalStatuses = %v, want %v", ChangeSetProposalStatuses, want)
	}
}

func TestChangeSetStatusTransitions(t *testing.T) {
	want := []ChangeSetStatusTransition{
		{From: "assessed", To: "draft"},
		{From: "draft", To: "assessed"},
		{From: "assessed", To: "applied"},
		{From: "assessed", To: "rejected"},
		{From: "assessed", To: "stale"},
		{From: "stale", To: "assessed"},
		{From: "stale", To: "satisfied"},
		{From: "applied", To: "stabilized"},
	}
	if len(ChangeSetStatusTransitions) != len(want) {
		t.Fatalf("ChangeSetStatusTransitions = %v, want %v", ChangeSetStatusTransitions, want)
	}
	for i, transition := range want {
		if ChangeSetStatusTransitions[i] != transition {
			t.Fatalf("ChangeSetStatusTransitions[%d] = %v, want %v", i, ChangeSetStatusTransitions[i], transition)
		}
	}
	for _, transition := range ChangeSetStatusTransitions {
		if !ChangeSetStatusTransitionAllowed(transition.From, transition.To) {
			t.Fatalf("declared transition %v is not allowed", transition)
		}
	}
}

func TestChangeSetStatusTransitionAllowedRejectsUnknownMoves(t *testing.T) {
	for _, move := range [][2]string{
		{"applied", "draft"},
		{"applied", "rejected"},
		{"rejected", "applied"},
		{"rejected", "draft"},
		{"satisfied", "assessed"},
		{"stabilized", "applied"},
		{"draft", "applied"},
		{"draft", "rejected"},
		{"unknown", "draft"},
		{"draft", "unknown"},
	} {
		if ChangeSetStatusTransitionAllowed(move[0], move[1]) {
			t.Fatalf("ChangeSetStatusTransitionAllowed(%q, %q) = true, want false", move[0], move[1])
		}
	}
	// Writing the same status again is a no-op and is allowed: several code
	// paths re-write the current status while adding a reason.
	for _, status := range ChangeSetStatuses {
		if !ChangeSetStatusTransitionAllowed(status, status) {
			t.Fatalf("ChangeSetStatusTransitionAllowed(%q, %q) = false, want true", status, status)
		}
	}
	if ChangeSetStatusTransitionAllowed("unknown", "unknown") {
		t.Fatal("a self-transition on an unknown status was allowed")
	}
}

// TestChangeSetStatusMatchesTheContract pins the Go vocabulary against the
// outward contract. The contract enum is what callers see, so the two must
// name the same states.
func TestChangeSetStatusMatchesTheContract(t *testing.T) {
	raw, err := os.ReadFile(filepath.Join("..", "..", "..", "docs", "08-本轮实施方案", "contracts", "openapi.json"))
	if err != nil {
		t.Fatalf("读取契约失败：%v", err)
	}
	var contract struct {
		Components struct {
			Schemas map[string]struct {
				Properties map[string]struct {
					Enum []any `json:"enum"`
				} `json:"properties"`
			} `json:"schemas"`
		} `json:"components"`
	}
	if err := json.Unmarshal(raw, &contract); err != nil {
		t.Fatalf("解析契约失败：%v", err)
	}
	schema, ok := contract.Components.Schemas["ChangeSet"]
	if !ok {
		t.Fatal("契约里没有 ChangeSet schema")
	}
	status, ok := schema.Properties["status"]
	if !ok {
		t.Fatal("契约的 ChangeSet 没有 status 属性")
	}
	var states []string
	for _, value := range status.Enum {
		text, isText := value.(string)
		if !isText {
			t.Fatalf("契约的 ChangeSet.status 枚举里出现了非字符串取值 %v", value)
		}
		states = append(states, text)
	}
	// The order is not part of the contract: the contract, the database
	// constraint and the Go vocabulary are compared as sets.
	if !sameStates(states, ChangeSetStatuses) {
		t.Fatalf("契约枚举 = %v，Go 词表 = %v，两者必须覆盖同一组状态", states, ChangeSetStatuses)
	}
}

// sameStates reports whether two state lists cover the same set of states.
func sameStates(left, right []string) bool {
	a := slices.Clone(left)
	b := slices.Clone(right)
	slices.Sort(a)
	slices.Sort(b)
	return slices.Equal(a, b)
}

// TestChangeSetStatusMatchesTheDatabaseConstraint pins the Go vocabulary
// against the last migration that sets the constraint, for both databases.
//
// The constraint moves across the migration chain: SQLite 000032 creates the
// table with four states, 000037 rebuilds it with six and 000043 rebuilds it
// with seven; PostgreSQL 000111 creates it with four, 000116 changes it to
// six and 000122 changes it to seven. Only the last step of each chain is the
// current constraint.
func TestChangeSetStatusMatchesTheDatabaseConstraint(t *testing.T) {
	cases := []string{
		filepath.Join("..", "..", "..", "migrations", "sqlite", "000043_lingdoc_satisfied_change_sets.up.sql"),
		filepath.Join("..", "..", "..", "migrations", "versioned", "000122_lingdoc_satisfied_change_sets.up.sql"),
	}
	pattern := regexp.MustCompile(`(?i)status\s+IN\s*\(([^)]*)\)`)
	for _, path := range cases {
		raw, err := os.ReadFile(path)
		if err != nil {
			t.Fatalf("读取迁移 %s 失败：%v", path, err)
		}
		match := pattern.FindSubmatch(raw)
		if match == nil {
			t.Fatalf("迁移 %s 里没有找到状态约束", path)
		}
		var states []string
		for _, value := range regexp.MustCompile(`'([^']*)'`).FindAllStringSubmatch(string(match[1]), -1) {
			states = append(states, value[1])
		}
		if !sameStates(states, ChangeSetStatuses) {
			t.Fatalf("%s 的约束 = %v，Go 词表 = %v，两者必须覆盖同一组状态", path, states, ChangeSetStatuses)
		}
	}
}
