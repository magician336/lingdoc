package workspace

import (
	"strings"

	"github.com/Tencent/WeKnora/internal/types"
)

// modelRunMode 说这次调用到模型面前的模式。
//
// 判据只有一条：provider 名里带 fake 或 mock ⇒ 真 API 假模型；否则是真模型。
// 找不到这次用的模型时用 fallback——调用方知道「找不到模型」在自己这条链路上
// 意味着什么（生成侧意味着「没有 provider」，改写侧意味着「按真模型算」）。
//
// 三档取值的词表归 candidateadoption 所有，这里只产出那个词表里的取值。
func modelRunMode(models []*types.Model, modelID, fallback string) string {
	for _, model := range models {
		if model != nil && model.ID == modelID {
			provider := strings.ToLower(strings.TrimSpace(model.Parameters.Provider))
			if strings.Contains(provider, "fake") || strings.Contains(provider, "mock") {
				return "real_api_fake_model"
			}
			return "real"
		}
	}
	return fallback
}
