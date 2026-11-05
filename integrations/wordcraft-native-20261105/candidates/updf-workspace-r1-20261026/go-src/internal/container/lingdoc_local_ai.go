package container

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"unicode"

	"github.com/Tencent/WeKnora/internal/lingdoc/workspace"
	core "github.com/Tencent/WeKnora/internal/lingdoc/workspacecore"
	"github.com/Tencent/WeKnora/internal/models/chat"
	"github.com/Tencent/WeKnora/internal/types"
	"github.com/Tencent/WeKnora/internal/types/interfaces"
	"github.com/Tencent/WeKnora/internal/utils"
	"github.com/google/uuid"
)

const (
	localAIModelName = "deepseek-flash"
	localAIProvider  = "deepseek"
	localAIBaseURL   = "https://api.deepseek.com/v1"
	localAIManagedBy = "lingdoc_local_ai"
)

// This projection is the only model configuration exposed by local bootstrap.
type LocalResearchAI struct {
	Configured bool   `json:"configured"`
	Provider   string `json:"provider"`
	Model      string `json:"model"`
}

// Never serialize or log this value. The file is an operator-owned, ignored
// local credential file, and is read only by this dedicated loopback entry.
type localAIConfiguration struct {
	Model        string `json:"model"`
	APIKey       string `json:"api_key"`
	SystemAESKey string `json:"system_aes_key"`
}

func prepareLocalAIConfiguration(path string) (*localAIConfiguration, error) {
	if path == "" {
		return nil, nil
	}
	if !filepath.IsAbs(path) {
		return nil, errors.New("local AI config requires an absolute file path")
	}
	file, err := os.Open(path)
	if err != nil {
		return nil, errors.New("local AI config could not be read")
	}
	defer file.Close()
	info, err := file.Stat()
	if err != nil || !info.Mode().IsRegular() || info.Size() <= 0 || info.Size() > 16<<10 {
		return nil, errors.New("local AI config must be a small regular JSON file")
	}
	var config localAIConfiguration
	decoder := json.NewDecoder(io.LimitReader(file, (16<<10)+1))
	decoder.DisallowUnknownFields()
	if err = decoder.Decode(&config); err != nil {
		return nil, errors.New("local AI config is invalid JSON")
	}
	var trailing any
	if err = decoder.Decode(&trailing); err != io.EOF {
		return nil, errors.New("local AI config contains trailing data")
	}
	if config.Model != localAIModelName {
		return nil, errors.New("local AI config requires the supported DeepSeek model")
	}
	if config.APIKey == "" || len(config.APIKey) > 512 || strings.IndexFunc(config.APIKey, func(r rune) bool {
		return unicode.IsSpace(r) || unicode.IsControl(r)
	}) >= 0 || strings.HasPrefix(config.APIKey, utils.EncPrefix) {
		return nil, errors.New("local AI API credential is missing or invalid")
	}
	if len([]byte(config.SystemAESKey)) != 32 {
		return nil, errors.New("local AI encryption key must contain exactly 32 bytes")
	}
	if current := os.Getenv("SYSTEM_AES_KEY"); current != "" && current != config.SystemAESKey {
		return nil, errors.New("local AI encryption key conflicts with the configured process key")
	}
	if err = os.Setenv("SYSTEM_AES_KEY", config.SystemAESKey); err != nil {
		return nil, errors.New("local AI encryption key could not be configured")
	}
	return &config, nil
}

func registerLocalAIModel(ctx context.Context, models interfaces.ModelService, config *localAIConfiguration) (string, LocalResearchAI, error) {
	if config == nil {
		return "", LocalResearchAI{}, nil
	}
	failed := func() (string, LocalResearchAI, error) {
		return "", LocalResearchAI{}, errors.New("local AI model configuration could not be stored")
	}
	if models == nil || utils.GetAESKey() == nil {
		return failed()
	}
	tenantID, ok := types.TenantIDFromContext(ctx)
	if !ok || tenantID == 0 {
		return failed()
	}
	// Tenant identity is part of the stable ID; neither a shared builtin nor a
	// different tenant's model can be silently selected or overwritten.
	id := uuid.NewSHA1(uuid.NameSpaceURL, []byte(fmt.Sprintf("lingdoc-local-ai:deepseek:%d", tenantID))).String()
	registered, err := models.ListModels(ctx)
	if err != nil {
		return failed()
	}
	var previous *types.Model
	for _, candidate := range registered {
		if candidate != nil && candidate.ID == id {
			if candidate.TenantID != tenantID || candidate.IsBuiltin || candidate.ManagedBy != localAIManagedBy {
				return failed()
			}
			if candidate.Parameters.APIKey == "" {
				// A rotated/wrong AES key must not silently replace an unreadable
				// stored credential or rewrite unrelated encrypted state.
				return "", LocalResearchAI{}, errors.New("local AI stored credential is unreadable; restore the original encryption key")
			}
			previous = candidate
			break
		}
	}
	status := LocalResearchAI{Configured: true, Provider: localAIProvider, Model: config.Model}
	if previous != nil && previous.Name == config.Model && previous.Type == types.ModelTypeKnowledgeQA &&
		previous.Source == types.ModelSourceRemote && previous.Status == types.ModelStatusActive &&
		previous.Parameters.APIKey == config.APIKey && previous.Parameters.Provider == localAIProvider &&
		previous.Parameters.BaseURL == localAIBaseURL && previous.Parameters.MaxOutputTokens == 2048 &&
		previous.Parameters.ExtraConfig[chat.ExtraConfigThinkingControl] == "thinking_type" {
		return id, status, nil
	}
	// Encrypt explicitly before passing through ModelService. Its normal
	// ModelParameters driver retains enc:v1 values. This also fails closed if
	// encryption fails, instead of allowing that driver's plaintext fallback.
	encrypted, err := utils.EncryptAESGCM(config.APIKey, utils.GetAESKey())
	if err != nil || !strings.HasPrefix(encrypted, utils.EncPrefix) {
		return failed()
	}
	model := &types.Model{
		ID: id, TenantID: tenantID, Name: config.Model, DisplayName: "DeepSeek 资料问答",
		Type: types.ModelTypeKnowledgeQA, Source: types.ModelSourceRemote,
		Status: types.ModelStatusActive, ManagedBy: localAIManagedBy,
		Parameters: types.ModelParameters{
			BaseURL: localAIBaseURL, Provider: localAIProvider, APIKey: encrypted,
			MaxOutputTokens: 2048,
			// Pin the wire field even for a model name newer than the adapter.
			ExtraConfig: map[string]string{chat.ExtraConfigThinkingControl: "thinking_type"},
		},
	}
	if previous == nil {
		err = models.CreateModel(ctx, model)
	} else {
		model.CreatedAt = previous.CreatedAt
		err = models.UpdateModel(ctx, model)
	}
	if err != nil {
		return failed()
	}
	return id, status, nil
}

func configureLocalResearchAI(ctx context.Context, deps localResearchDependencies, config *localAIConfiguration) (LocalResearchAI, error) {
	id, status, err := registerLocalAIModel(ctx, deps.Models, config)
	if err != nil {
		return LocalResearchAI{}, err
	}
	projects, ok := deps.Projects.(*core.Service)
	if !ok {
		return LocalResearchAI{}, errors.New("local research application is unavailable")
	}
	research := workspace.NewG2ResearchAdapter(deps.DB, projects, deps.Runtime, deps.KBShares, deps.KnowledgeObjects, deps.WebSearch)
	projects.ConfigureG2(core.G2Runtime{
		Sources: research, Research: research, Imports: research,
		Model:   workspace.NewG2LocalAgentModel(deps.Models, id),
		Actions: workspace.NewG2ActionExecutor(projects, deps.Runtime, nil),
	})
	return status, nil
}
