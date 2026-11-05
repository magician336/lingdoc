package workspacecore

import (
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"strings"
	"unicode/utf8"
)

const WorkspaceAssistantAgent = "workspace_assistant"

type AgentDocumentRange struct {
	Anchor json.RawMessage `json:"anchor"`
	Focus  json.RawMessage `json:"focus"`
}

type AgentDocumentSelection struct {
	DocumentID       int64               `json:"document_id"`
	DocumentRevision int64               `json:"document_revision"`
	Text             string              `json:"text"`
	TextSHA256       string              `json:"text_sha256"`
	Locator          string              `json:"locator"`
	SelectionRange   *AgentDocumentRange `json:"selection_range,omitempty"`
}

func validateAgentDocumentRange(value *AgentDocumentRange) error {
	if value == nil {
		return nil
	}
	if len(value.Anchor) == 0 || len(value.Focus) == 0 || !json.Valid(value.Anchor) || !json.Valid(value.Focus) {
		return ErrInvalidRequest
	}
	return nil
}

func ValidateAgentDocumentSelection(value *AgentDocumentSelection) error {
	if value == nil {
		return nil
	}
	digest := fmt.Sprintf("%x", sha256.Sum256([]byte(value.Text)))
	if value.DocumentID <= 0 || value.DocumentRevision < 0 || strings.TrimSpace(value.Text) == "" ||
		utf8.RuneCountInString(value.Text) > 6000 || value.TextSHA256 != digest {
		return ErrInvalidRequest
	}
	if value.Locator == "" {
		value.Locator = "正文选区"
	}
	return validateAgentDocumentRange(value.SelectionRange)
}
