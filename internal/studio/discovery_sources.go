package studio

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"io"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"
)

type sourceConnection struct {
	source, ref, key string
	profile          modelConnection
}

const (
	discoveryConfigLimit = 2 << 20
	discoveryEntryLimit  = 100
	discoveryDBLimit     = 256 << 20
	discoveryRowLimit    = 256 << 10
)

func discoverSources(home, configDir string) []sourceConnection {
	var found []sourceConnection
	seen := make(map[string]bool)
	for _, dir := range []string{configDir, discoveryPath(home, ".config")} {
		path := discoveryPath(dir, "magpie", "providers.json")
		if path != "" && !seen[path] {
			found = append(found, discoverMagpie(path)...)
			seen[path] = true
		}
	}
	found = append(found, discoverCCSwitch(discoveryPath(home, ".cc-switch", "cc-switch.db"))...)
	claudeDir := discoveryPath(home, ".claude")
	if override := os.Getenv("CLAUDE_CONFIG_DIR"); filepath.IsAbs(override) {
		claudeDir = override
	}
	path := discoveryPath(claudeDir, "settings.json")
	var settings discoveryAgentSettings
	if readDiscoveryJSON(path, &settings) {
		found = appendAgentConnection(found, "claude-code", path, "Claude Code", "claude", "", settings)
	}
	return found
}

func discoveryPath(dir string, parts ...string) string {
	if dir == "" {
		return ""
	}
	return filepath.Join(append([]string{dir}, parts...)...)
}

func discoveryRegularFile(path string, limit int64) bool {
	info, err := os.Lstat(path)
	return err == nil && info.Mode().IsRegular() && info.Size() <= limit
}

func readDiscoveryJSON(path string, dst any) bool {
	if !discoveryRegularFile(path, discoveryConfigLimit) {
		return false
	}
	file, err := os.Open(path)
	if err != nil {
		return false
	}
	defer file.Close()
	data, err := io.ReadAll(io.LimitReader(file, discoveryConfigLimit+1))
	return err == nil && len(data) <= discoveryConfigLimit &&
		json.Unmarshal(bytes.TrimPrefix(data, []byte("\xef\xbb\xbf")), dst) == nil
}

func discoverMagpie(path string) []sourceConnection {
	var file struct {
		Providers []json.RawMessage `json:"providers"`
	}
	if !readDiscoveryJSON(path, &file) {
		return nil
	}
	var found []sourceConnection
	for _, raw := range file.Providers[:min(len(file.Providers), discoveryEntryLimit)] {
		var entry struct {
			ID, Name, Icon, Key, KeyProtocol, BaseAPI, AuthHeader string
			Chat, Responses, Anthropic, Gemini                    string
			Off, Disabled, Hidden, SwitchOnly, InsecureTLS        bool
			Enabled                                               *bool
			Account, Vertex                                       *json.RawMessage
			SessionToken, AccessKeyID, SecretAccessKey            string
			ClientCert, ClientKey, ServerCA, ServerPin, Proxy     string
			Headers                                               map[string]string
			Models                                                []string
		}
		if json.Unmarshal(raw, &entry) != nil || entry.ID == "" || entry.Off || entry.Disabled ||
			(entry.Enabled != nil && !*entry.Enabled) || entry.Hidden || entry.SwitchOnly ||
			entry.Account != nil || entry.Vertex != nil || entry.InsecureTLS ||
			entry.SessionToken != "" || entry.AccessKeyID != "" || entry.SecretAccessKey != "" ||
			entry.ClientCert != "" || entry.ClientKey != "" || entry.ServerCA != "" ||
			entry.ServerPin != "" || entry.Proxy != "" || len(entry.Headers) != 0 {
			continue
		}
		protocol := firstDiscoveryValue(entry.KeyProtocol, entry.BaseAPI)
		endpoints := map[string]string{
			"chat": entry.Chat, "responses": entry.Responses, "anthropic": entry.Anthropic, "gemini": entry.Gemini,
		}
		if protocol == "" {
			for _, candidate := range []string{"chat", "responses", "anthropic", "gemini"} {
				if endpoints[candidate] != "" {
					protocol = candidate
					break
				}
			}
		}
		if protocol == "anthropic" && entry.AuthHeader != "" && entry.AuthHeader != "both" && entry.AuthHeader != "x-api-key" {
			continue
		}
		icon := entry.Icon
		if !connectionIcon.MatchString(icon) {
			icon = "magpie"
		}
		var model string
		if len(entry.Models) > 0 {
			model = entry.Models[0]
		}
		found = appendDiscoveryConnection(found, "magpie", path+"#"+entry.ID,
			firstDiscoveryValue(entry.Name, entry.ID), icon, protocol, endpoints[protocol], entry.Key, model)
	}
	return found
}

func discoverCCSwitch(path string) []sourceConnection {
	if !discoveryRegularFile(path, discoveryDBLimit) {
		return nil
	}
	path, err := filepath.Abs(path)
	if err != nil {
		return nil
	}
	sqlitePath := filepath.ToSlash(path)
	if filepath.VolumeName(path) != "" && !strings.HasPrefix(sqlitePath, "/") {
		sqlitePath = "/" + sqlitePath
	}
	uri := url.URL{Scheme: "file", Path: sqlitePath, RawQuery: "mode=ro"}
	db, err := sql.Open("sqlite", uri.String())
	if err != nil {
		return nil
	}
	defer db.Close()
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	query := func(meta string) (*sql.Rows, error) {
		return db.QueryContext(ctx, `SELECT id, app_type, name, settings_config, COALESCE(category, ''), `+meta+`
			FROM providers WHERE length(CAST(settings_config AS BLOB)) <= ?
			AND app_type IN ('claude', 'claude-desktop', 'gemini', 'opencode', 'openclaw', 'pi')
			ORDER BY app_type, id LIMIT ?`, discoveryRowLimit, discoveryEntryLimit)
	}
	rows, err := query(`CASE WHEN length(CAST(COALESCE(meta, '') AS BLOB)) <= 65536 THEN COALESCE(NULLIF(meta, ''), '{}') ELSE '' END`)
	if err != nil && strings.Contains(err.Error(), "no such column: meta") {
		rows, err = query(`'{}'`)
	}
	if err != nil {
		return nil
	}
	defer rows.Close()
	var found []sourceConnection
	for rows.Next() {
		var id, app, name, raw, category, meta string
		if rows.Scan(&id, &app, &name, &raw, &category, &meta) != nil || id == "" {
			continue
		}
		category = strings.ToLower(category)
		if category == "official" || strings.Contains(category, "subscription") {
			continue
		}
		var settings discoveryAgentSettings
		var metadata struct {
			APIFormat, AuthMode, AuthHeader, ProviderType string
			Disabled                                      bool
			Enabled                                       *bool
		}
		if json.Unmarshal([]byte(raw), &settings) != nil || json.Unmarshal([]byte(meta), &metadata) != nil ||
			metadata.Disabled || (metadata.Enabled != nil && !*metadata.Enabled) ||
			strings.Contains(strings.ToLower(metadata.ProviderType), "subscription") ||
			(metadata.AuthMode != "" && metadata.AuthMode != "api_key") {
			continue
		}
		if metadata.AuthHeader != "" {
			settings.AuthHeader = metadata.AuthHeader
		}
		found = appendAgentConnection(found, "cc-switch", path+"#"+app+"/"+id,
			firstDiscoveryValue(name, id), app, metadata.APIFormat, settings)
	}
	return found
}

type discoveryAgentSettings struct {
	Env                              map[string]string
	Model, API, NPM, BaseURL, APIKey string
	AuthHeader, APIKeyHelper         string
	Off, Disabled                    bool
	Enabled                          *bool
	Options                          struct{ BaseURL, APIKey string }
	Models                           json.RawMessage
}

func appendAgentConnection(found []sourceConnection, source, ref, name, app, format string, s discoveryAgentSettings) []sourceConnection {
	if s.Disabled || s.Off || (s.Enabled != nil && !*s.Enabled) || s.APIKeyHelper != "" {
		return found
	}
	icon := "ccswitch"
	if source == "claude-code" {
		icon = "claude"
	}
	var protocol, base, key, model string
	switch app {
	case "claude", "claude-desktop":
		env := s.Env
		for _, flag := range []string{"CLAUDE_CODE_USE_BEDROCK", "CLAUDE_CODE_USE_VERTEX", "CLAUDE_CODE_USE_FOUNDRY"} {
			if value := strings.ToLower(strings.TrimSpace(env[flag])); value != "" && value != "0" && value != "false" {
				return found
			}
		}
		base, key = strings.TrimSpace(env["ANTHROPIC_BASE_URL"]), env["ANTHROPIC_API_KEY"]
		token := strings.TrimSpace(env["ANTHROPIC_AUTH_TOKEN"])
		switch format {
		case "", "anthropic":
			if token != "" || !discoveryAuthMatches(s.AuthHeader, "x-api-key") {
				return found
			}
			protocol = "anthropic"
			if base == "" && key != "" {
				base = "https://api.anthropic.com/v1"
			}
		case "openai_chat", "openai_responses":
			if !discoveryAuthMatches(s.AuthHeader, "bearer") {
				return found
			}
			protocol = strings.TrimPrefix(format, "openai_")
			key = firstDiscoveryValue(token, key)
		default:
			return found
		}
		model = firstDiscoveryValue(env["ANTHROPIC_MODEL"], s.Model, env["ANTHROPIC_DEFAULT_SONNET_MODEL"],
			env["ANTHROPIC_DEFAULT_OPUS_MODEL"], env["ANTHROPIC_DEFAULT_HAIKU_MODEL"], env["ANTHROPIC_SMALL_FAST_MODEL"])
	case "gemini":
		if !discoveryAuthMatches(s.AuthHeader, "x-goog-api-key") || (format != "" && format != "gemini") {
			return found
		}
		protocol = "gemini"
		base = firstDiscoveryValue(s.Env["GOOGLE_GEMINI_BASE_URL"], "https://generativelanguage.googleapis.com/v1beta")
		if u, err := url.Parse(base); err == nil && !strings.HasSuffix(strings.TrimRight(u.Path, "/"), "/v1beta") &&
			!strings.HasSuffix(strings.TrimRight(u.Path, "/"), "/v1") && u.RawQuery == "" && !u.ForceQuery && !strings.Contains(base, "#") {
			base = strings.TrimRight(base, "/") + "/v1beta"
		}
		key = firstDiscoveryValue(s.Env["GEMINI_API_KEY"], s.Env["GOOGLE_API_KEY"])
		model = firstDiscoveryValue(s.Env["GEMINI_MODEL"], s.Model)
	default:
		switch firstDiscoveryValue(format, s.API, s.NPM) {
		case "openai_chat", "openai-completions", "@ai-sdk/openai-compatible", "chat":
			protocol = "chat"
		case "openai_responses", "openai-responses", "responses":
			protocol = "responses"
		case "anthropic", "anthropic-messages", "@ai-sdk/anthropic":
			protocol = "anthropic"
		default:
			return found
		}
		header := "bearer"
		if protocol == "anthropic" {
			header = "x-api-key"
		}
		if !discoveryAuthMatches(s.AuthHeader, header) {
			return found
		}
		base = firstDiscoveryValue(s.BaseURL, s.Options.BaseURL)
		key = firstDiscoveryValue(s.APIKey, s.Options.APIKey)
		model = firstDiscoveryValue(s.Model, firstDiscoveryModel(s.Models))
	}
	return appendDiscoveryConnection(found, source, ref, name, icon, protocol, base, key, model)
}

func discoveryAuthMatches(configured, expected string) bool {
	return configured == "" || strings.EqualFold(strings.TrimSpace(configured), expected)
}

func firstDiscoveryValue(values ...string) string {
	for _, value := range values {
		if value = strings.TrimSpace(value); value != "" {
			return value
		}
	}
	return ""
}

func firstDiscoveryModel(raw json.RawMessage) string {
	var array []json.RawMessage
	if json.Unmarshal(raw, &array) == nil && len(array) > 0 {
		var id string
		if json.Unmarshal(array[0], &id) == nil {
			return id
		}
		var model struct{ ID string }
		if json.Unmarshal(array[0], &model) == nil {
			return model.ID
		}
	}
	var models map[string]json.RawMessage
	var first string
	if json.Unmarshal(raw, &models) == nil {
		for id := range models {
			if first == "" || id < first {
				first = id
			}
		}
	}
	return first
}

var discoveryTokenPrefix = regexp.MustCompile(`(?i)^(?:(?:sk-|eyJ[A-Za-z0-9_-]+\.|ya29\.|gh[pousr]_|github_pat_|AIza|xox[abprs]-)|[a-z0-9_~-]{32,}$)`)
var discoveryEnvPlaceholder = regexp.MustCompile(`(?i)^(?:[a-z][a-z0-9_]*_)?(?:api_key|auth_token|access_token|secret_key)$`)

func appendDiscoveryConnection(found []sourceConnection, source, ref, name, icon, protocol, base, key, model string) []sourceConnection {
	base, key = strings.TrimSpace(base), strings.TrimSpace(key)
	if base == "" || !discoveryAPIKey(key) {
		return found
	}
	u, err := url.Parse(base)
	if err != nil || discoveryURLHasToken(u, key) {
		return found
	}
	profile := modelConnection{Preset: "compatible", APIProtocol: protocol, Name: name, Icon: icon, BaseURL: base, Model: strings.TrimSpace(model)}
	switch protocol {
	case "chat", "responses", "anthropic", "gemini":
	default:
		return found
	}
	if u.Scheme == "http" && (protocol == "chat" || protocol == "responses") {
		profile.Preset, profile.GatewayAPI, profile.APIProtocol = "gateway", protocol, ""
	} else if key == "" {
		return found
	}
	config, err := profile.validatedConfig(key)
	if err != nil {
		return found
	}
	profile.BaseURL = config.BaseURL
	if protocol == "anthropic" && !strings.HasSuffix(profile.BaseURL, "/v1") {
		profile.BaseURL += "/v1"
	}
	return append(found, sourceConnection{source: source, ref: ref, key: key, profile: profile})
}

func discoveryAPIKey(key string) bool {
	if key == "" {
		return true
	}
	if len(key) > 4096 || strings.ContainsAny(key, "${}<>%\"'") || discoveryEnvPlaceholder.MatchString(key) ||
		strings.IndexFunc(key, func(r rune) bool { return r < '!' || r > '~' }) >= 0 {
		return false
	}
	lower := strings.ToLower(key)
	for _, prefix := range []string{"sk-ant-oat", "sk-ant-ort", "eyj", "ya29.", "gho_", "ghu_", "your-", "your_", "replace-", "replace_", "placeholder"} {
		if strings.HasPrefix(lower, prefix) {
			return false
		}
	}
	switch lower {
	case "none", "null", "dummy", "dummy-key", "dummy-token", "no-key", "test", "test-key", "changeme", "n/a":
		return false
	}
	return true
}

func discoveryURLHasToken(u *url.URL, key string) bool {
	host := strings.TrimSuffix(strings.ToLower(u.Hostname()), ".")
	if host == "chatgpt.com" || strings.HasSuffix(host, ".chatgpt.com") || host == "chat.openai.com" ||
		(key != "" && (strings.Contains(u.Path, key) || strings.Contains(u.Host, key))) {
		return true
	}
	for _, part := range strings.Split(u.Path, "/") {
		if discoveryTokenPrefix.MatchString(part) || strings.ContainsAny(part, "=$<>{}") {
			return true
		}
		switch strings.ToLower(part) {
		case "api_key", "apikey", "api-key", "token", "access-token", "access_token", "auth-token", "auth_token":
			return true
		}
	}
	return false
}
