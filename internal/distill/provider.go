package distill

import (
	"bytes"
	"context"
	"crypto/tls"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"regexp"
	"strings"
	"time"
)

const (
	maxResponseBytes = 1 << 20
	requestTimeout   = 180 * time.Second
)

var geminiModelID = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._-]*$`)

// TestModel checks that the configured model can produce text without session data.
func TestModel(ctx context.Context, config Config) error {
	_, err := requestText(ctx, config, "Reply with OK.", "Connection test", 256, nil)
	return err
}

func requestText(ctx context.Context, config Config, instruction, input string, maxTokens int, transport http.RoundTripper) (string, error) {
	provider, base, headers, err := connection(config)
	if err != nil {
		return "", err
	}
	model := strings.TrimSpace(config.Model)
	if model == "" {
		return "", errors.New("请选择或填写模型 ID")
	}
	effort := strings.ToLower(strings.TrimSpace(config.ReasoningEffort))
	var endpoint string
	var payload map[string]any
	switch provider {
	case "openai", "compatible", "gateway":
		endpoint = base + "/chat/completions"
		payload = map[string]any{
			"model":                 model,
			"max_completion_tokens": maxTokens,
			"messages": []map[string]string{
				{"role": "system", "content": instruction},
				{"role": "user", "content": input},
			},
		}
		if provider != "openai" {
			delete(payload, "max_completion_tokens")
			payload["max_tokens"] = maxTokens
		}
		if effort != "" {
			payload["reasoning_effort"] = effort
		}
	case "responses", "gateway-responses":
		endpoint = base + "/responses"
		payload = map[string]any{
			"model": model, "instructions": instruction,
			"input":             []map[string]string{{"role": "user", "content": input}},
			"max_output_tokens": maxTokens, "store": false,
		}
		if effort != "" {
			payload["reasoning"] = map[string]string{"effort": effort}
		}
	case "anthropic":
		endpoint = base + "/messages"
		payload = map[string]any{
			"model":      model,
			"max_tokens": maxTokens,
			"system":     instruction,
			"messages":   []map[string]string{{"role": "user", "content": input}},
		}
		switch effort {
		case "none":
			payload["thinking"] = map[string]any{"type": "disabled"}
		case "low", "medium", "high":
			budget := thinkingBudgetTokens(effort)
			payload["max_tokens"] = maxTokens + budget
			payload["thinking"] = map[string]any{"type": "enabled", "budget_tokens": budget}
		}
	case "gemini":
		model = strings.TrimPrefix(model, "models/")
		if !geminiModelID.MatchString(model) {
			return "", errors.New("distill: invalid Gemini model ID")
		}
		endpoint = base + "/models/" + model + ":generateContent"
		genConfig := map[string]any{"maxOutputTokens": maxTokens}
		if effort != "" {
			genConfig["thinkingConfig"] = map[string]any{"thinkingBudget": thinkingBudgetTokens(effort)}
		}
		payload = map[string]any{
			"systemInstruction": map[string]any{
				"parts": []map[string]string{{"text": instruction}},
			},
			"contents": []map[string]any{{
				"role":  "user",
				"parts": []map[string]string{{"text": input}},
			}},
			"generationConfig": genConfig,
		}
	default:
		return "", errors.New("distill: unsupported provider")
	}

	body, err := json.Marshal(payload)
	if err != nil {
		return "", errors.New("distill: could not encode model request")
	}
	data, err := request(ctx, http.MethodPost, endpoint, headers, bytes.NewReader(body), transport)
	if err != nil {
		return "", err
	}
	return responseText(provider, data)
}

// ConnectionBase validates and returns the API base without sending a request.
func ConnectionBase(config Config) (string, error) {
	_, base, _, err := connection(config)
	return base, err
}

func thinkingBudgetTokens(effort string) int {
	switch effort {
	case "none":
		return 0
	case "low":
		return 1024
	case "medium":
		return 2048
	case "high":
		return 4096
	default:
		return 0
	}
}

func connection(config Config) (provider, base string, headers http.Header, err error) {
	provider = strings.ToLower(strings.TrimSpace(config.Provider))
	key := strings.TrimSpace(config.APIKey)
	config.Provider = provider
	if key == "" && !config.IsGateway() {
		return "", "", nil, errors.New("请填入 API 密钥")
	}
	if strings.ContainsAny(key, "\r\n") {
		return "", "", nil, errors.New("API 密钥中含有换行")
	}
	headers = http.Header{"Content-Type": {"application/json"}}
	base = strings.TrimSpace(config.BaseURL)
	switch provider {
	case "openai":
		if base == "" {
			base = "https://api.openai.com/v1"
		}
		headers.Set("Authorization", "Bearer "+key)
	case "anthropic":
		if base == "" {
			base = "https://api.anthropic.com/v1"
		}
		headers.Set("x-api-key", key)
		headers.Set("anthropic-version", "2023-06-01")
	case "gemini":
		if base == "" {
			base = "https://generativelanguage.googleapis.com/v1beta"
		}
		headers.Set("x-goog-api-key", key)
	case "compatible", "responses", "gateway", "gateway-responses":
		if key != "" {
			headers.Set("Authorization", "Bearer "+key)
		}
	default:
		return "", "", nil, errors.New("distill: unsupported provider")
	}
	u, parseErr := url.Parse(base)
	if parseErr != nil || u.Hostname() == "" || u.User != nil || u.RawQuery != "" || u.ForceQuery || strings.Contains(base, "#") {
		return "", "", nil, errors.New("请填写不带账号、查询参数或片段的 API 地址")
	}
	if config.IsGateway() {
		host := strings.ToLower(u.Hostname())
		if u.Scheme != "http" || (host != "127.0.0.1" && host != "::1" && host != "localhost") {
			return "", "", nil, errors.New("本地网关仅接受 HTTP 回环地址，例如 http://127.0.0.1:3425/v1")
		}
		if host == "localhost" {
			u.Host = "127.0.0.1" + strings.TrimPrefix(u.Host, u.Hostname())
		}
		headers.Set("User-Agent", "knooo")
	} else if u.Scheme != "https" {
		return "", "", nil, errors.New("请填写不带账号、查询参数或片段的 HTTPS API 地址")
	}
	base = strings.TrimRight(u.String(), "/")
	if config.IsGateway() && base == "http://"+u.Host {
		base += "/v1"
	}
	for _, suffix := range []string{"/chat/completions", "/models", "/responses", "/messages"} {
		if strings.HasSuffix(strings.TrimRight(u.EscapedPath(), "/"), suffix) {
			base = strings.TrimSuffix(base, suffix)
			break
		}
	}
	return provider, base, headers, nil
}

func request(ctx context.Context, method, endpoint string, headers http.Header, body io.Reader, transport http.RoundTripper) ([]byte, error) {
	req, err := http.NewRequestWithContext(ctx, method, endpoint, body)
	if err != nil {
		return nil, errors.New("distill: could not create model request")
	}
	req.Header = headers
	client := http.Client{
		Transport: transport,
		Timeout:   requestTimeout,
		CheckRedirect: func(*http.Request, []*http.Request) error {
			return http.ErrUseLastResponse
		},
	}
	response, err := client.Do(req)
	if err != nil {
		return nil, requestError(ctx, err, "model request failed")
	}
	defer response.Body.Close()
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		switch response.StatusCode {
		case 401:
			return nil, errors.New("服务商拒绝认证（HTTP 401），请检查 API 密钥")
		case 402:
			return nil, errors.New("服务商要求付费（HTTP 402），请检查余额或账单")
		case 403:
			return nil, errors.New("服务商拒绝访问（HTTP 403），请检查密钥权限或访问限制")
		case 429:
			return nil, errors.New("服务商限流或配额不足（HTTP 429）")
		}
		if response.StatusCode >= 300 && response.StatusCode < 400 {
			return nil, fmt.Errorf("API 地址返回重定向（HTTP %d），请使用服务商提供的实际 API 地址", response.StatusCode)
		}
		if response.StatusCode == 404 || response.StatusCode == 405 {
			if method == http.MethodGet {
				return nil, fmt.Errorf("模型目录不可用（HTTP %d），请检查 API 地址（部分服务需要 /v1），或手动填写模型 ID 后测试连接", response.StatusCode)
			}
			return nil, fmt.Errorf("模型调用地址或模型不可用（HTTP %d），请检查 API 地址和模型 ID", response.StatusCode)
		}
		return nil, fmt.Errorf("服务商请求失败（HTTP %d）", response.StatusCode)
	}
	if response.ContentLength > maxResponseBytes {
		return nil, errors.New("distill: model response exceeds size limit")
	}
	data, err := io.ReadAll(io.LimitReader(response.Body, maxResponseBytes+1))
	if err != nil {
		return nil, requestError(ctx, err, "could not read model response")
	}
	if len(data) > maxResponseBytes {
		return nil, errors.New("distill: model response exceeds size limit")
	}
	return data, nil
}

func requestError(ctx context.Context, err error, message string) error {
	var dns *net.DNSError
	var certificate *tls.CertificateVerificationError
	switch {
	case errors.Is(ctx.Err(), context.DeadlineExceeded), errors.Is(err, context.DeadlineExceeded):
		return errors.New("请求服务商超时，请检查网络后重试")
	case ctx.Err() != nil:
		return ctx.Err()
	case errors.As(err, &dns):
		return errors.New("无法解析 API 地址，请检查域名和网络")
	case errors.As(err, &certificate):
		return errors.New("API 地址的 HTTPS 证书校验失败")
	}
	return errors.New("distill: " + message)
}

func responseText(provider string, data []byte) (string, error) {
	var response struct {
		Choices []struct {
			FinishReason string `json:"finish_reason"`
			Message      struct {
				Content string `json:"content"`
				Refusal string `json:"refusal"`
			} `json:"message"`
		} `json:"choices"`
		StopReason string `json:"stop_reason"`
		Status     string `json:"status"`
		Incomplete struct {
			Reason string `json:"reason"`
		} `json:"incomplete_details"`
		Output []struct {
			Type    string `json:"type"`
			Content []struct {
				Type string `json:"type"`
				Text string `json:"text"`
			} `json:"content"`
		} `json:"output"`
		Content []struct {
			Type string `json:"type"`
			Text string `json:"text"`
		} `json:"content"`
		Candidates []struct {
			FinishReason string `json:"finishReason"`
			Content      struct {
				Parts []struct {
					Text    string `json:"text"`
					Thought bool   `json:"thought"`
				} `json:"parts"`
			} `json:"content"`
		} `json:"candidates"`
	}
	if err := json.Unmarshal(data, &response); err != nil {
		return "", errors.New("distill: invalid provider response")
	}

	var text strings.Builder
	incomplete := errors.New("distill: model did not complete a text response")
	switch provider {
	case "openai", "compatible", "gateway":
		if len(response.Choices) == 0 {
			return "", incomplete
		}
		choice := response.Choices[0]
		if choice.FinishReason == "length" {
			return "", errors.New("模型回复达到输出上限，请提高连接的提炼输出上限或换用其他模型")
		}
		if (choice.FinishReason != "" && choice.FinishReason != "stop") || choice.Message.Refusal != "" {
			return "", incomplete
		}
		text.WriteString(choice.Message.Content)
	case "responses", "gateway-responses":
		if response.Status == "incomplete" && response.Incomplete.Reason == "max_output_tokens" {
			return "", errors.New("模型回复达到输出上限，请提高连接的提炼输出上限或换用其他模型")
		}
		if response.Status != "completed" {
			return "", incomplete
		}
		for _, item := range response.Output {
			if item.Type != "message" {
				continue
			}
			for _, block := range item.Content {
				if block.Type == "output_text" {
					text.WriteString(block.Text)
				}
			}
		}
	case "anthropic":
		switch response.StopReason {
		case "", "end_turn", "stop_sequence":
		case "max_tokens":
			return "", errors.New("模型回复达到输出上限，请提高连接的提炼输出上限或换用其他模型")
		default:
			return "", incomplete
		}
		for _, block := range response.Content {
			if block.Type == "text" {
				text.WriteString(block.Text)
			}
		}
	case "gemini":
		if len(response.Candidates) == 0 {
			return "", incomplete
		}
		candidate := response.Candidates[0]
		if candidate.FinishReason == "MAX_TOKENS" {
			return "", errors.New("模型回复达到输出上限，请提高连接的提炼输出上限或换用其他模型")
		}
		if candidate.FinishReason != "" && candidate.FinishReason != "STOP" {
			return "", incomplete
		}
		for _, part := range candidate.Content.Parts {
			if !part.Thought {
				text.WriteString(part.Text)
			}
		}
	}
	if strings.TrimSpace(text.String()) == "" {
		return "", incomplete
	}
	return text.String(), nil
}
