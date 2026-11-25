package studio

import (
	"context"
	"crypto/rand"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/xiejun23/Knooo/internal/distill"
)

type modelConnection struct {
	ID           string `json:"id"`
	Preset       string `json:"preset"`
	Icon         string `json:"icon,omitempty"`
	GatewayAPI   string `json:"gatewayApi,omitempty"`
	APIProtocol  string `json:"apiProtocol,omitempty"`
	Name         string `json:"name"`
	BaseURL      string `json:"baseUrl"`
	Model           string `json:"model"`
	Ready           bool   `json:"ready"`
	HasKey          bool   `json:"hasKey,omitempty"`
	Disabled        bool   `json:"disabled,omitempty"`
	ReasoningEffort string `json:"reasoningEffort,omitempty"`
	InputBudget     int    `json:"inputBudget"`
	OutputBudget    int    `json:"outputBudget"`
}

type connectionPreset struct {
	ID       string `json:"id"`
	Name     string `json:"name"`
	BaseURL  string `json:"baseUrl"`
	Protocol string `json:"-"`
}

const magpieBase = "http://127.0.0.1:3425/v1"
const ccSwitchBase = "http://127.0.0.1:15721/v1"

var connectionPresets = []connectionPreset{
	{"gateway", "本地网关", "", "gateway"},
	{"openai", "OpenAI", "https://api.openai.com/v1", "openai"},
	{"anthropic", "Anthropic", "https://api.anthropic.com/v1", "anthropic"},
	{"gemini", "Google Gemini", "https://generativelanguage.googleapis.com/v1beta", "gemini"},
	{"deepseek", "DeepSeek", "https://api.deepseek.com", "compatible"},
	{"dashscope", "阿里云百炼", "https://dashscope.aliyuncs.com/compatible-mode/v1", "compatible"},
	{"moonshot", "Kimi", "https://api.moonshot.cn/v1", "compatible"},
	{"minimax", "MiniMax", "https://api.minimaxi.com/v1", "compatible"},
	{"zhipu", "智谱 GLM", "https://open.bigmodel.cn/api/paas/v4", "compatible"},
	{"openrouter", "OpenRouter", "https://openrouter.ai/api/v1", "compatible"},
	{"siliconflow", "硅基流动", "https://api.siliconflow.cn/v1", "compatible"},
	{"compatible", "OpenAI 兼容服务", "", "compatible"},
}

var connectionID = regexp.MustCompile(`^[A-Z2-7]{26}$`)
var connectionIcon = regexp.MustCompile(`^(generic|openai|claude|gemini|deepseek|qwen|kimi|minimax|zhipu|mistral|openrouter|grok|magpie|ccswitch|ollama|lmstudio|siliconflow)$`)
var validReasoningEffort = regexp.MustCompile(`^(|none|low|medium|high)$`)

type connectionKeys struct {
	sync.Mutex
	values map[string]string
}

func (profile modelConnection) config(key string) (distill.Config, bool) {
	profile = profile.normalize()
	for _, preset := range connectionPresets {
		if profile.Preset == preset.ID {
			base := profile.BaseURL
			if strings.TrimSpace(base) == "" {
				base = preset.BaseURL
			}
			protocol := preset.Protocol
			if preset.ID == "gateway" && profile.GatewayAPI == "responses" {
				protocol = "gateway-responses"
			}
			if preset.ID != "gateway" {
				switch profile.APIProtocol {
				case "":
				case "chat":
					if preset.Protocol != "openai" {
						protocol = "compatible"
					}
				case "responses", "anthropic", "gemini":
					protocol = profile.APIProtocol
				default:
					return distill.Config{}, false
				}
			}
			return distill.Config{
				Provider: protocol, Model: profile.Model, BaseURL: base, APIKey: key,
				ReasoningEffort: profile.ReasoningEffort,
				InputBudget: profile.InputBudget, OutputBudget: profile.OutputBudget,
			}, true
		}
	}
	return distill.Config{}, false
}

func (profile modelConnection) normalize() modelConnection {
	if profile.Preset == "magpie" {
		profile.Preset, profile.Icon = "gateway", "magpie"
		if profile.BaseURL == "" {
			profile.BaseURL = magpieBase
		}
	}
	if profile.Preset == "gateway" && profile.GatewayAPI == "" {
		profile.GatewayAPI = "chat"
	}
	return profile
}

func (profile modelConnection) validatedConfig(key string) (distill.Config, error) {
	profile = profile.normalize()
	if profile.Preset == "gateway" && profile.GatewayAPI != "chat" && profile.GatewayAPI != "responses" {
		return distill.Config{}, errors.New("请选择网关 API 协议")
	}
	if !validReasoningEffort.MatchString(profile.ReasoningEffort) {
		return distill.Config{}, errors.New("请选择有效的思考等级")
	}
	config, ok := profile.config(key)
	if !ok {
		return distill.Config{}, errors.New("未知模型服务商或 API 协议")
	}
	input, output, err := distill.Budgets(config.InputBudget, config.OutputBudget)
	if err != nil {
		return distill.Config{}, err
	}
	config.InputBudget, config.OutputBudget = input, output
	if key == "" && !config.IsGateway() {
		config.APIKey = "validate-settings"
	}
	base, err := distill.ConnectionBase(config)
	config.BaseURL, config.APIKey = base, key
	return config, err
}

func magpieDefaultBase() string {
	dir := os.Getenv("XDG_CONFIG_HOME")
	if dir == "" {
		home, err := os.UserHomeDir()
		if err != nil {
			return magpieBase
		}
		dir = filepath.Join(home, ".config")
	}
	var settings struct {
		Port int `json:"port"`
	}
	data, err := os.ReadFile(filepath.Join(dir, "magpie", "settings.json"))
	if err == nil && json.Unmarshal(data, &settings) == nil && settings.Port > 0 && settings.Port <= 65535 {
		return "http://127.0.0.1:" + strconv.Itoa(settings.Port) + "/v1"
	}
	return magpieBase
}

// reusableKey is called while the key store is locked.
func (a *app) reusableKey(id string, preset string, current distill.Config) string {
	var previous modelConnection
	var content string
	if a.db.QueryRow(`SELECT content FROM settings WHERE name=?`, "model:"+id).Scan(&content) != nil ||
		json.Unmarshal([]byte(content), &previous) != nil || previous.normalize().Preset != preset {
		return ""
	}
	config, err := previous.validatedConfig("")
	if err != nil || config.BaseURL != current.BaseURL || config.Provider != current.Provider {
		return ""
	}
	return a.savedModelKey(previous, config)
}

func (a *app) modelConnections(w http.ResponseWriter, _ *http.Request) {
	a.keys.Lock()
	defer a.keys.Unlock()
	rows, err := a.db.Query(`SELECT settings.content, model_keys.secret FROM settings LEFT JOIN model_keys ON 'model:' || model_keys.id = settings.name WHERE settings.name LIKE 'model:%' ORDER BY settings.rowid`)
	if err != nil {
		http.Error(w, "无法读取模型连接", 500)
		return
	}
	defer rows.Close()
	profiles := []modelConnection{}
	for rows.Next() {
		var profile modelConnection
		var content string
		var secret []byte
		if rows.Scan(&content, &secret) != nil || json.Unmarshal([]byte(content), &profile) != nil {
			http.Error(w, "无法读取模型连接", 500)
			return
		}
		profile = profile.normalize()
		config, configErr := profile.validatedConfig("")
		profile.HasKey = a.keys.values[profile.ID] != "" ||
			(configErr == nil && a.unlockModelKey(profile, config, secret) != "")
		profile.Ready = !profile.Disabled && (profile.Preset == "gateway" || profile.HasKey)
		profile.InputBudget, profile.OutputBudget, _ = distill.Budgets(profile.InputBudget, profile.OutputBudget)
		profiles = append(profiles, profile)
	}
	if rows.Err() != nil {
		http.Error(w, "无法读取模型连接", 500)
		return
	}
	jsonReply(w, map[string]any{
		"profiles": profiles, "presets": connectionPresets,
		"gatewayExamples": []map[string]string{
			{"name": "Ollama", "baseUrl": "http://127.0.0.1:11434/v1", "api": "chat", "icon": "ollama"},
			{"name": "LM Studio", "baseUrl": "http://127.0.0.1:1234/v1", "api": "chat", "icon": "lmstudio"},
			{"name": "Magpie", "baseUrl": magpieDefaultBase(), "api": "chat", "icon": "magpie"},
			{"name": "CC Switch", "baseUrl": ccSwitchBase, "api": "responses", "icon": "ccswitch"},
		},
	})
}

// The caller holds a.keys.
func (a *app) loadSavedModelConnection(id string) (modelConnection, bool) {
	if id == "" {
		return modelConnection{}, false
	}
	var profile modelConnection
	var content string
	if a.db.QueryRow(`SELECT content FROM settings WHERE name=?`, "model:"+id).Scan(&content) != nil ||
		json.Unmarshal([]byte(content), &profile) != nil {
		return modelConnection{}, false
	}
	return profile.normalize(), true
}

func (a *app) setModelConnection(w http.ResponseWriter, r *http.Request) {
	var body struct {
		modelConnection
		APIKey   string `json:"apiKey"`
		Remove   bool   `json:"remove"`
		ClearKey bool   `json:"clearKey"`
	}
	if !decode(w, r, &body) {
		return
	}
	if a.demo {
		http.Error(w, "演示工作区无法保存模型连接", 400)
		return
	}
	profile := body.modelConnection.normalize()
	if profile.ID != "" && !connectionID.MatchString(profile.ID) {
		http.Error(w, "模型连接 ID 无效", 400)
		return
	}
	a.keys.Lock()
	defer a.keys.Unlock()
	if profile.ID == "" {
		profile.ID = rand.Text()
	}
	if body.Remove {
		existing, found := a.loadSavedModelConnection(profile.ID)
		if err := a.writeModelConnection(profile, nil, true); err != nil {
			http.Error(w, "无法删除模型连接", 500)
			return
		}
		delete(a.keys.values, profile.ID)
		a.wakeReview(true)
		target := profile
		if found {
			target = existing
		}
		config, _ := target.validatedConfig("")
		a.recordEvent(platformEvent{
			Area:    "model",
			Level:   "info",
			Action:  "删除模型连接",
			Message: formatModelConnectionDetail(target, config, false, "操作：已从本机设置中移除该模型连接"),
		})
		jsonReply(w, map[string]bool{"ok": true})
		return
	}
	profile, status, err := a.saveModelConnection(profile, body.APIKey, body.ClearKey, "")
	if err != nil {
		http.Error(w, err.Error(), status)
		return
	}
	jsonReply(w, profile)
}

// The caller holds a.keys for a consistent profile and key update.
func (a *app) saveModelConnection(profile modelConnection, apiKey string, clearKey bool, importSource string) (modelConnection, int, error) {
	profile.Name = strings.TrimSpace(profile.Name)
	profile.Model = strings.TrimSpace(profile.Model)
	profile.BaseURL = strings.TrimRight(strings.TrimSpace(profile.BaseURL), "/")
	profile.ReasoningEffort = strings.ToLower(strings.TrimSpace(profile.ReasoningEffort))
	if profile.Preset != "gateway" {
		profile.GatewayAPI = ""
	} else {
		profile.APIProtocol = ""
	}
	if profile.Icon != "" && !connectionIcon.MatchString(profile.Icon) {
		return profile, 400, errors.New("请选择有效的连接图标")
	}
	if profile.Name == "" || len(profile.Name) > 200 || len(profile.Model) > 240 || len(profile.BaseURL) > 500 || len(apiKey) > 4096 {
		return profile, 400, errors.New("请检查连接名称、模型 ID、API 地址和密钥")
	}
	previous, existed := a.loadSavedModelConnection(profile.ID)
	keyInput := strings.TrimSpace(apiKey)
	key := keyInput
	config, err := profile.validatedConfig(key)
	if err != nil {
		a.recordEvent(platformEvent{
			Area:    "model",
			Level:   "error",
			Action:  "模型连接配置校验失败",
			Message: formatModelConnectionDetail(profile, config, key != "", "原因："+err.Error()),
		})
		return profile, 400, err
	}
	if clearKey {
		key = ""
	} else if key == "" {
		key = a.reusableKey(profile.ID, profile.Preset, config)
	}
	profile.BaseURL, profile.Ready, profile.HasKey = config.BaseURL, false, false
	profile.InputBudget, profile.OutputBudget = config.InputBudget, config.OutputBudget
	var secret []byte
	if a.data != "" && key != "" {
		secret, err = protectModelKey(a.data, []byte(key), modelKeyBinding(profile, config), false)
		if err != nil {
			return profile, 500, errors.New("无法加密保存 API 密钥")
		}
	}
	if err := a.writeModelConnection(profile, secret, false); err != nil {
		return profile, 500, errors.New("无法保存模型连接")
	}
	if a.data == "" {
		a.keys.values[profile.ID] = key
	}
	a.wakeReview(true)
	profile.HasKey = key != ""
	profile.Ready = !profile.Disabled && (profile.Preset == "gateway" || profile.HasKey)

	switch {
	case importSource != "":
		a.recordEvent(platformEvent{
			Area:    "model",
			Level:   "info",
			Action:  "导入本机模型连接",
			Message: formatModelConnectionDetail(profile, config, profile.HasKey, "导入来源："+importSource),
		})
	case !existed:
		a.recordEvent(platformEvent{
			Area:    "model",
			Level:   "info",
			Action:  "新建模型连接",
			Message: formatModelConnectionDetail(profile, config, profile.HasKey),
		})
	default:
		onlyToggle := previous.Disabled != profile.Disabled &&
			previous.Name == profile.Name &&
			previous.Preset == profile.Preset &&
			previous.APIProtocol == profile.APIProtocol &&
			previous.GatewayAPI == profile.GatewayAPI &&
			previous.BaseURL == profile.BaseURL &&
			previous.Model == profile.Model &&
			previous.InputBudget == profile.InputBudget &&
			previous.OutputBudget == profile.OutputBudget &&
			keyInput == "" && !clearKey
		action := "更新模型连接"
		extra := formatModelConnectionChanges(previous, profile, keyInput != "", clearKey)
		if onlyToggle {
			if profile.Disabled {
				action = "停用模型连接"
				extra = "操作：已停用该算力提供商"
			} else {
				action = "启用模型连接"
				extra = "操作：已重新启用该算力提供商"
			}
		}
		a.recordEvent(platformEvent{
			Area:    "model",
			Level:   "info",
			Action:  action,
			Message: formatModelConnectionDetail(profile, config, profile.HasKey, extra),
		})
	}
	return profile, 200, nil
}

func (a *app) connectionConfig(id string) (distill.Config, error) {
	if !connectionID.MatchString(id) {
		return distill.Config{}, errors.New("模型连接 ID 无效")
	}
	a.keys.Lock()
	defer a.keys.Unlock()
	var profile modelConnection
	var content string
	err := a.db.QueryRow(`SELECT content FROM settings WHERE name=?`, "model:"+id).Scan(&content)
	if errors.Is(err, sql.ErrNoRows) {
		return distill.Config{}, errors.New("找不到模型连接")
	}
	if err != nil || json.Unmarshal([]byte(content), &profile) != nil {
		return distill.Config{}, errors.New("无法读取模型连接")
	}
	profile = profile.normalize()
	if profile.Disabled {
		return distill.Config{}, errors.New("该算力提供商已停用，请先在「本机设置」中启用")
	}
	config, err := profile.validatedConfig("")
	if err != nil {
		return distill.Config{}, err
	}
	key := a.savedModelKey(profile, config)
	if key == "" && profile.Preset != "gateway" {
		return distill.Config{}, errors.New("请在模型连接中填入 API 密钥")
	}
	return profile.validatedConfig(key)
}

func selectedModel(ctx context.Context, config distill.Config) distill.Model {
	if config.IsGateway() {
		catalogCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
		defer cancel()
		models, _ := distill.Models(catalogCtx, config)
		for _, model := range models {
			if model.ID == config.Model {
				return model
			}
		}
	}
	return distill.Model{}
}

func (a *app) providerModels(w http.ResponseWriter, r *http.Request) {
	var body struct {
		modelConnection
		APIKey string `json:"apiKey"`
		Test   bool   `json:"test"`
	}
	if !decode(w, r, &body) {
		return
	}
	if a.demo {
		http.Error(w, "演示工作区无法请求服务商", 400)
		return
	}
	var config distill.Config
	var err error
	logProfile := body.modelConnection.normalize()
	switch {
	case body.Preset == "":
		a.keys.Lock()
		if saved, ok := a.loadSavedModelConnection(body.ID); ok {
			logProfile = saved
		}
		a.keys.Unlock()
		config, err = a.connectionConfig(body.ID)
	case body.ID != "" && !connectionID.MatchString(body.ID):
		err = errors.New("模型连接 ID 无效")
	case len(body.Model) > 240 || len(body.BaseURL) > 500 || len(body.APIKey) > 4096:
		err = errors.New("请检查模型 ID、API 地址和密钥")
	default:
		config, err = body.modelConnection.validatedConfig(strings.TrimSpace(body.APIKey))
		if err == nil && config.APIKey == "" {
			a.keys.Lock()
			config.APIKey = a.reusableKey(body.ID, body.modelConnection.normalize().Preset, config)
			if logProfile.Name == "" {
				if saved, ok := a.loadSavedModelConnection(body.ID); ok {
					logProfile.Name = saved.Name
				}
			}
			a.keys.Unlock()
			if config.APIKey == "" && !config.IsGateway() {
				err = errors.New("请为当前 API 地址填入密钥")
			}
		}
	}
	if err != nil {
		a.recordEvent(platformEvent{
			Area:    "model",
			Level:   "error",
			Action:  "模型连接配置校验失败",
			Message: formatModelConnectionDetail(logProfile, config, strings.TrimSpace(body.APIKey) != "" || config.APIKey != "", "原因："+err.Error()),
		})
		http.Error(w, err.Error(), 400)
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 30*time.Second)
	defer cancel()
	started := time.Now()
	if body.Test && strings.TrimSpace(config.Model) != "" {
		if err := distill.TestModel(ctx, config); err != nil {
			a.recordEvent(platformEvent{
				Area:    "model",
				Level:   "error",
				Action:  "模型连接测试失败",
				Message: formatModelConnectionDetail(logProfile, config, config.APIKey != "", "耗时："+formatDurationBrief(time.Since(started)), "原因："+err.Error()),
			})
			http.Error(w, err.Error(), 422)
			return
		}
		a.recordEvent(platformEvent{
			Area:    "model",
			Level:   "info",
			Action:  "模型连接测试通过",
			Message: formatModelConnectionDetail(logProfile, config, config.APIKey != "", "结果：目标模型响应正常 · 耗时 "+formatDurationBrief(time.Since(started))),
		})
		jsonReply(w, []distill.Model{})
		return
	}
	models, err := distill.Models(ctx, config)
	if err != nil {
		a.recordEvent(platformEvent{
			Area:    "model",
			Level:   "error",
			Action:  "读取模型列表失败",
			Message: formatModelConnectionDetail(logProfile, config, config.APIKey != "", "耗时："+formatDurationBrief(time.Since(started)), "原因："+err.Error()),
		})
		http.Error(w, err.Error(), 422)
		return
	}
	resultLine := fmt.Sprintf("结果：返回 %d 个可用模型 · 耗时 %s", len(models), formatDurationBrief(time.Since(started)))
	if len(models) > 0 {
		previewCount := min(len(models), 5)
		sample := make([]string, 0, previewCount)
		for i := range previewCount {
			sample = append(sample, models[i].ID)
		}
		suffix := ""
		if len(models) > previewCount {
			suffix = fmt.Sprintf(" 等 %d 个", len(models))
		}
		resultLine += "\n模型示例：" + strings.Join(sample, "、") + suffix
	}
	a.recordEvent(platformEvent{
		Area:    "model",
		Level:   "info",
		Action:  "读取模型列表",
		Message: formatModelConnectionDetail(logProfile, config, config.APIKey != "", resultLine),
	})
	jsonReply(w, models)
}
