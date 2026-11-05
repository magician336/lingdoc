package container

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Tencent/WeKnora/internal/application/repository"
	"github.com/Tencent/WeKnora/internal/application/service"
	"github.com/Tencent/WeKnora/internal/models/chat"
	"github.com/Tencent/WeKnora/internal/types"
	"github.com/Tencent/WeKnora/internal/utils"
	"github.com/stretchr/testify/require"
	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
)

func localAITestConfig() localAIConfiguration {
	return localAIConfiguration{Model: localAIModelName, APIKey: "synthetic-secret-never-a-real-key", SystemAESKey: "01234567890123456789012345678901"}
}

func TestLocalAIConfigurationValidatesBeforeSettingEncryption(t *testing.T) {
	t.Setenv("SYSTEM_AES_KEY", "")
	path := filepath.Join(t.TempDir(), "fake-config.json")
	fixture := localAITestConfig()
	encoded, err := json.Marshal(fixture)
	require.NoError(t, err)
	require.NoError(t, os.WriteFile(path, encoded, 0600))
	config, err := prepareLocalAIConfiguration(path)
	require.NoError(t, err)
	require.Equal(t, localAIModelName, config.Model)
	require.Len(t, utils.GetAESKey(), 32)
	config, err = prepareLocalAIConfiguration("")
	require.NoError(t, err)
	require.Nil(t, config)
	require.Equal(t, fixture.SystemAESKey, os.Getenv("SYSTEM_AES_KEY"))
}

func TestLocalAIConfigurationRejectsInvalidAndDoesNotEchoSecrets(t *testing.T) {
	for name, payload := range map[string]string{
		"invalid JSON":  `{"api_key":"synthetic-secret-never-a-real-key",`,
		"unknown model": `{"model":"synthetic-secret-never-a-real-key","api_key":"x","system_aes_key":"01234567890123456789012345678901"}`,
		"short key":     `{"model":"deepseek-flash","api_key":"synthetic-secret-never-a-real-key","system_aes_key":"short"}`,
		"header chars":  `{"model":"deepseek-flash","api_key":"synthetic-secret-never-a-real-key\r\n","system_aes_key":"01234567890123456789012345678901"}`,
		"unknown field": `{"model":"deepseek-flash","api_key":"synthetic-secret-never-a-real-key","system_aes_key":"01234567890123456789012345678901","extra":"synthetic-secret-never-a-real-key"}`,
	} {
		t.Run(name, func(t *testing.T) {
			t.Setenv("SYSTEM_AES_KEY", "")
			path := filepath.Join(t.TempDir(), "config.json")
			require.NoError(t, os.WriteFile(path, []byte(payload), 0600))
			_, err := prepareLocalAIConfiguration(path)
			require.Error(t, err)
			require.NotContains(t, err.Error(), "synthetic-secret")
			require.Empty(t, os.Getenv("SYSTEM_AES_KEY"))
		})
	}
}

func TestLocalAIModelUsesExistingEncryptedStoreAndTenantIdentity(t *testing.T) {
	config := localAITestConfig()
	t.Setenv("SYSTEM_AES_KEY", config.SystemAESKey)
	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{})
	require.NoError(t, err)
	sqlDB, err := db.DB()
	require.NoError(t, err)
	sqlDB.SetMaxOpenConns(1)
	t.Cleanup(func() { sqlDB.Close() })
	require.NoError(t, db.AutoMigrate(&types.Model{}))
	models := service.NewModelService(repository.NewModelRepository(db), nil, nil, nil, nil, nil)
	ctx := localResearchContext(context.Background(), &types.User{ID: "owner"}, &types.Tenant{ID: 71})
	id, status, err := registerLocalAIModel(ctx, models, &config)
	require.NoError(t, err)
	require.True(t, status.Configured)
	require.Equal(t, localAIProvider, status.Provider)
	require.Equal(t, localAIModelName, status.Model)
	var stored string
	require.NoError(t, db.Raw("SELECT parameters FROM models WHERE id = ?", id).Scan(&stored).Error)
	require.Contains(t, stored, utils.EncPrefix)
	require.NotContains(t, stored, config.APIKey)
	require.NotContains(t, stored, config.SystemAESKey)
	loaded, err := models.GetModelByID(ctx, id)
	require.NoError(t, err)
	require.Equal(t, config.APIKey, loaded.Parameters.APIKey)
	require.False(t, loaded.IsBuiltin)
	require.False(t, loaded.IsDefault)
	require.Equal(t, "thinking_type", chat.EffectiveThinkingControl(&chat.ChatConfig{
		ModelName: loaded.Name, Provider: loaded.Parameters.Provider, BaseURL: loaded.Parameters.BaseURL, ExtraConfig: loaded.Parameters.ExtraConfig,
	}))
	again, _, err := registerLocalAIModel(ctx, models, &config)
	require.NoError(t, err)
	require.Equal(t, id, again)
	var count int64
	require.NoError(t, db.Model(&types.Model{}).Count(&count).Error)
	require.EqualValues(t, 1, count)
	otherCtx := localResearchContext(context.Background(), &types.User{ID: "other-owner"}, &types.Tenant{ID: 72})
	other, _, err := registerLocalAIModel(otherCtx, models, &config)
	require.NoError(t, err)
	require.NotEqual(t, id, other)
	bootstrap, err := json.Marshal(LocalResearchBootstrap{AI: status})
	require.NoError(t, err)
	require.NotContains(t, string(bootstrap), "api_key")
	require.NotContains(t, string(bootstrap), config.APIKey)
	require.NotContains(t, string(bootstrap), "system_aes_key")

	t.Setenv("SYSTEM_AES_KEY", strings.Repeat("z", 32))
	_, _, err = registerLocalAIModel(ctx, models, &config)
	require.Error(t, err, "wrong AES key must not replace an unreadable stored credential")
	require.NoError(t, db.Raw("SELECT parameters FROM models WHERE id = ?", id).Scan(&stored).Error)
	require.NotContains(t, stored, config.APIKey)
}
