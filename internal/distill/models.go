package distill

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/url"
	"slices"
	"sort"
	"strings"
)

type Model struct {
	ID              string `json:"id"`
	Name            string `json:"display_name,omitempty"`
	Owner           string `json:"owned_by,omitempty"`
	ContextWindow   int    `json:"context_window,omitempty"`
	MaxOutputTokens int    `json:"max_output_tokens,omitempty"`
}

// Models lists the provider's models and any limits exposed by its catalog.
func Models(ctx context.Context, config Config) ([]Model, error) {
	return listModels(ctx, config, nil)
}

func listModels(ctx context.Context, config Config, transport http.RoundTripper) ([]Model, error) {
	provider, base, headers, err := connection(config)
	if err != nil {
		return nil, err
	}
	endpoint := base + "/models"
	models := []Model{}
	seen := map[string]bool{}
	add := func(model Model) {
		if model.ID != "" && !seen[model.ID] {
			models = append(models, model)
			seen[model.ID] = true
		}
	}
	for range 10 {
		data, err := request(ctx, http.MethodGet, endpoint, headers, nil, transport)
		if err != nil {
			return nil, err
		}
		var page struct {
			Data   []Model `json:"data"`
			Models []struct {
				Slug        string   `json:"slug"`
				CodexName   string   `json:"display_name"`
				Context     int      `json:"context_window"`
				Name        string   `json:"name"`
				DisplayName string   `json:"displayName"`
				OutputLimit int      `json:"outputTokenLimit"`
				Methods     []string `json:"supportedGenerationMethods"`
			} `json:"models"`
			NextToken string `json:"nextPageToken"`
			HasMore   bool   `json:"has_more"`
			LastID    string `json:"last_id"`
		}
		if json.Unmarshal(data, &page) != nil ||
			(page.Data == nil && !(page.Models != nil && (config.IsGateway() || provider == "gemini"))) {
			return nil, errors.New("服务商返回了无法识别的模型目录，请检查 API 地址，或手动填写模型 ID 后测试连接")
		}
		for _, model := range page.Data {
			add(model)
		}
		for _, model := range page.Models {
			if config.IsGateway() && model.Slug != "" {
				add(Model{ID: model.Slug, Name: model.CodexName, ContextWindow: model.Context})
			} else if provider == "gemini" && model.Name != "" && slices.Contains(model.Methods, "generateContent") {
				add(Model{ID: strings.TrimPrefix(model.Name, "models/"), Name: model.DisplayName, MaxOutputTokens: model.OutputLimit})
			}
		}
		switch {
		case provider == "gemini" && page.NextToken != "":
			endpoint = base + "/models?pageToken=" + url.QueryEscape(page.NextToken)
		case provider == "anthropic" && page.HasMore && page.LastID != "":
			endpoint = base + "/models?after_id=" + url.QueryEscape(page.LastID)
		default:
			sort.Slice(models, func(i, j int) bool { return models[i].ID < models[j].ID })
			return models, nil
		}
	}
	return nil, errors.New("模型列表分页过多，可手动输入模型 ID")
}
