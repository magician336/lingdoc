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

func TestChangeSetStatusMoves(t *testing.T) {
	want := map[string]ChangeSetStatusMove{
		"apply":     {From: []string{"assessed"}, To: "applied"},
		"reject":    {From: []string{"assessed"}, To: "rejected"},
		"markStale": {From: []string{"assessed"}, To: "stale"},
		"assess":    {From: []string{"draft", "assessed"}, To: "assessed"},
		"reassess":  {From: []string{"stale"}, To: "assessed"},
		"satisfy":   {From: []string{"stale"}, To: "satisfied"},
		"stabilize": {From: []string{"applied"}, To: "stabilized"},
		"editDraft": {From: []string{"draft", "assessed"}, To: "draft"},
	}
	if len(ChangeSetStatusMoves) != len(want) {
		t.Fatalf("ChangeSetStatusMoves has %d entries, want %d: %v", len(ChangeSetStatusMoves), len(want), ChangeSetStatusMoves)
	}
	for name, move := range want {
		declared, ok := ChangeSetStatusMoves[name]
		if !ok {
			t.Fatalf("ChangeSetStatusMoves has no %q entry", name)
		}
		if declared.To != move.To || !slices.Equal(declared.From, move.From) {
			t.Fatalf("ChangeSetStatusMoves[%q] = %+v, want %+v", name, declared, move)
		}
	}
}

// Every move the production code performs is declared, and the declaration is
// what the production code asks: a move that is not declared must be refused
// even when its statuses look plausible.
func TestChangeSetStatusCanMoveOnlyDeclaredMoves(t *testing.T) {
	legal := []struct {
		move string
		from string
	}{
		{"apply", "assessed"},
		{"reject", "assessed"},
		{"markStale", "assessed"},
		{"assess", "draft"},
		{"assess", "assessed"},
		{"reassess", "stale"},
		{"satisfy", "stale"},
		{"stabilize", "applied"},
		{"editDraft", "draft"},
		{"editDraft", "assessed"},
	}
	for _, item := range legal {
		if !ChangeSetStatusCanMove(item.move, item.from) {
			t.Fatalf("ChangeSetStatusCanMove(%q, %q) = false, want true", item.move, item.from)
		}
	}
	illegal := []struct {
		move string
		from string
	}{
		{"apply", "draft"},
		{"apply", "applied"},
		{"apply", "rejected"},
		{"reject", "draft"},
		{"reject", "rejected"},
		{"markStale", "stale"},
		{"assess", "stale"},
		{"assess", "applied"},
		{"reassess", "draft"},
		{"reassess", "assessed"},
		{"satisfy", "assessed"},
		{"stabilize", "draft"},
		{"stabilize", "stabilized"},
		{"editDraft", "stale"},
		{"editDraft", "applied"},
		{"unknown", "assessed"},
	}
	for _, item := range illegal {
		if ChangeSetStatusCanMove(item.move, item.from) {
			t.Fatalf("ChangeSetStatusCanMove(%q, %q) = true, want false", item.move, item.from)
		}
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
