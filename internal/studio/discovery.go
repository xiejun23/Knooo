package studio

import (
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"net/http"
	"os"
	"strings"

	"github.com/xiejun23/Knooo/internal/distill"
)

type discoveredConnection struct {
	ID      string          `json:"id"`
	Source  string          `json:"source"`
	Profile modelConnection `json:"profile"`
}

type importCandidate struct {
	discoveredConnection
	key    string
	config distill.Config
}

func (a *app) importCandidates() []importCandidate {
	home, err := os.UserHomeDir()
	if err != nil {
		return nil
	}
	configDir, _ := os.UserConfigDir()
	return a.connectionsFromSources(discoverSources(home, configDir))
}

func (a *app) connectionsFromSources(sources []sourceConnection) []importCandidate {
	var candidates []importCandidate
	seen := map[[32]byte]bool{}
	for _, source := range sources {
		profile := source.profile.normalize()
		key := strings.TrimSpace(source.key)
		config, err := profile.validatedConfig(key)
		if err != nil || (key == "" && !config.IsGateway()) || len(key) > 4096 ||
			profile.Name == "" || len(profile.Name) > 200 || len(profile.Model) > 240 || len(profile.BaseURL) > 500 {
			continue
		}
		if key != "" && (strings.Contains(profile.Name, key) || strings.Contains(profile.Model, key)) {
			continue
		}
		profile.BaseURL = config.BaseURL
		profile.Ready = true
		identity := connectionFingerprint(config, key)
		if seen[identity] {
			continue
		}
		seen[identity] = true
		fingerprint := hmac.New(sha256.New, []byte(a.discoveryNonce))
		_ = json.NewEncoder(fingerprint).Encode([7]string{source.source, source.ref, config.Provider, config.BaseURL, profile.Name, profile.Model, key})
		candidates = append(candidates, importCandidate{
			discoveredConnection: discoveredConnection{ID: hex.EncodeToString(fingerprint.Sum(nil)), Source: source.source, Profile: profile},
			key:                  key, config: config,
		})
	}
	return candidates
}

func connectionFingerprint(config distill.Config, key string) [32]byte {
	protocol := config.Provider
	if protocol == "openai" {
		protocol = "compatible"
	}
	identity, _ := json.Marshal([3]string{protocol, config.BaseURL, key})
	return sha256.Sum256(identity)
}

// The caller holds a.keys.
func (a *app) savedConnectionFingerprints() (map[[32]byte]bool, error) {
	rows, err := a.db.Query(`SELECT settings.content, model_keys.secret FROM settings LEFT JOIN model_keys ON 'model:' || model_keys.id = settings.name WHERE settings.name LIKE 'model:%'`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	saved := map[[32]byte]bool{}
	for rows.Next() {
		var profile modelConnection
		var secret []byte
		var content string
		if err := rows.Scan(&content, &secret); err != nil {
			return nil, err
		}
		if err := json.Unmarshal([]byte(content), &profile); err != nil {
			return nil, err
		}
		if config, err := profile.validatedConfig(""); err == nil {
			key := a.keys.values[profile.ID]
			if key == "" {
				key = a.unlockModelKey(profile.normalize(), config, secret)
			}
			saved[connectionFingerprint(config, key)] = true
		}
	}
	return saved, rows.Err()
}

func (a *app) discoveredModelConnections(w http.ResponseWriter, _ *http.Request) {
	w.Header().Set("Cache-Control", "no-store")
	out := []discoveredConnection{}
	if !a.demo {
		candidates := a.importCandidates()
		a.keys.Lock()
		saved, err := a.savedConnectionFingerprints()
		a.keys.Unlock()
		if err != nil {
			http.Error(w, "无法读取已保存的模型连接", 500)
			return
		}
		for _, candidate := range candidates {
			if !saved[connectionFingerprint(candidate.config, candidate.key)] {
				out = append(out, candidate.discoveredConnection)
			}
		}
	}
	jsonReply(w, out)
}

func (a *app) importModelConnection(w http.ResponseWriter, r *http.Request) {
	var body struct {
		ID string `json:"id"`
	}
	if !decode(w, r, &body) {
		return
	}
	if a.demo {
		http.Error(w, "演示工作区无法导入模型连接", 400)
		return
	}
	for _, candidate := range a.importCandidates() {
		if candidate.ID != body.ID {
			continue
		}
		a.keys.Lock()
		defer a.keys.Unlock()
		saved, err := a.savedConnectionFingerprints()
		if err != nil {
			http.Error(w, "无法读取已保存的模型连接", 500)
			return
		}
		if saved[connectionFingerprint(candidate.config, candidate.key)] {
			http.Error(w, "此连接已保存，请刷新发现列表", 409)
			return
		}
		candidate.Profile.ID = rand.Text()
		profile, status, err := a.saveModelConnection(candidate.Profile, candidate.key, false, candidate.Source)
		if err != nil {
			http.Error(w, err.Error(), status)
			return
		}
		jsonReply(w, profile)
		return
	}
	http.Error(w, "本机配置已变化，请刷新发现列表后重新导入", 409)
}
