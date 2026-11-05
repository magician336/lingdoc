package workspacecore

import (
	"encoding/json"
	"unicode/utf8"
)

type AgentHistoryUsage struct {
	IncludedTurns  int            `json:"included_turns"`
	ExcludedTurns  int            `json:"excluded_turns"`
	TruncatedTurns int            `json:"truncated_turns"`
	Reasons        map[string]int `json:"reasons"`
}

func BuildWorkspaceAssistantHistory(turns []AgentTurn, current AgentContextPackage) ([]AgentMessage, AgentHistoryUsage) {
	usage := AgentHistoryUsage{Reasons: map[string]int{}}
	compatible := make([]AgentTurn, 0, len(turns))
	for _, turn := range turns {
		if turn.Status != "completed" {
			continue
		}
		reason := ""
		switch {
		case turn.Context.ContextRevision != current.ContextRevision || turn.Context.TaskVersion != current.TaskVersion ||
			turn.Context.ChapterID != current.ChapterID || turn.Context.TaskID != current.TaskID:
			reason = "context_changed"
		case turn.Context.DocumentSelection != nil && (current.DocumentSelection == nil ||
			turn.Context.DocumentSelection.TextSHA256 != current.DocumentSelection.TextSHA256):
			reason = "selection_changed"
		case !workspaceHistorySourcesIncluded(turn.Context.SourceRefs, current.SourceRefs):
			reason = "sources_changed"
		}
		if reason != "" {
			usage.ExcludedTurns++
			usage.Reasons[reason]++
			continue
		}
		compatible = append(compatible, turn)
	}

	start := len(compatible)
	characters := 0
	for i := len(compatible) - 1; i >= 0; i-- {
		size := utf8.RuneCountInString(compatible[i].UserInput) + utf8.RuneCountInString(compatible[i].Reply)
		if len(compatible)-i > 8 || characters+size > 12000 {
			break
		}
		start = i
		characters += size
	}
	usage.TruncatedTurns = start
	messages := []AgentMessage{}
	for _, turn := range compatible[start:] {
		messages = append(messages,
			AgentMessage{Role: "user", Content: turn.UserInput},
			AgentMessage{Role: "assistant", Content: turn.Reply})
	}
	usage.IncludedTurns = len(messages) / 2
	return messages, usage
}

func workspaceHistorySourcesIncluded(old, current []AgentSourceRef) bool {
	allowed := map[string]AgentSourceRef{}
	for _, ref := range current {
		allowed[ref.SourceID] = ref
	}
	for _, ref := range old {
		now, ok := allowed[ref.SourceID]
		if !ok || now.AssetRevision != ref.AssetRevision || now.QuotedTextHash != ref.QuotedTextHash || !now.ModelUseAllowed {
			return false
		}
	}
	return true
}

func WithAgentHistoryUsage(raw json.RawMessage, usage AgentHistoryUsage) json.RawMessage {
	var object map[string]json.RawMessage
	if json.Unmarshal(raw, &object) != nil || object == nil {
		object = map[string]json.RawMessage{}
	}
	encoded, _ := json.Marshal(usage)
	object["history_usage"] = encoded
	result, _ := json.Marshal(object)
	return result
}
