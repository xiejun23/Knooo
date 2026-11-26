package update

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"time"

	"golang.org/x/mod/semver"
)

const (
	Repository = "https://github.com/xiejun23/Knooo"
	latestAPI  = "https://api.github.com/repos/xiejun23/Knooo/releases/latest"
	setupName  = "Knooo-Setup-windows-x64.exe"
	maxSetup   = 128 << 20
)

var stableTag = regexp.MustCompile(`^v(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)$`)

type release struct {
	Tag        string `json:"tag_name"`
	Draft      bool   `json:"draft"`
	Prerelease bool   `json:"prerelease"`
	Assets     []struct {
		Name   string `json:"name"`
		Size   int64  `json:"size"`
		Digest string `json:"digest"`
		State  string `json:"state"`
	} `json:"assets"`
}

type candidate struct {
	Version string `json:"version"`
	size    int64
	hash    string
}

type service struct {
	api, repository string
	current         string
	client          *http.Client
	install         func(string) error
	shutdown        func()
	mu              sync.Mutex
	started         bool
}

func Handler(version string, enabled, canInstall bool, shutdown func()) http.Handler {
	s := &service{
		api: latestAPI, repository: Repository, current: "v" + version, shutdown: shutdown,
		client: &http.Client{CheckRedirect: func(req *http.Request, via []*http.Request) error {
			host := req.URL.Host
			if len(via) >= 5 || req.URL.Scheme != "https" || req.URL.User != nil ||
				(host != "github.com" && host != "release-assets.githubusercontent.com" && host != "objects.githubusercontent.com") {
				return errors.New("unexpected release download redirect")
			}
			return nil
		}},
	}
	if enabled && canInstall && RegisteredExecutable() != "" {
		s.install = launchInstaller
	}
	mux := http.NewServeMux()
	mux.HandleFunc("GET /api/update", s.check)
	if enabled {
		mux.HandleFunc("POST /api/update", s.apply)
	} else {
		s.api = ""
	}
	return mux
}

func (s *service) check(w http.ResponseWriter, r *http.Request) {
	ctx, cancel := context.WithTimeout(r.Context(), 8*time.Second)
	defer cancel()
	newer, err := s.latest(ctx)
	if err != nil {
		log.Printf("检查 Knooo 更新：%v", err)
		http.Error(w, "无法检查更新，请稍后重试。", http.StatusBadGateway)
		return
	}
	reply(w, struct {
		Enabled    bool       `json:"enabled"`
		CanInstall bool       `json:"canInstall"`
		Available  *candidate `json:"available"`
	}{s.api != "", s.install != nil && newer != nil && newer.hash != "", newer})
}

func (s *service) apply(w http.ResponseWriter, r *http.Request) {
	if s.install == nil {
		http.Error(w, "请从 GitHub Releases 下载新版。", http.StatusConflict)
		return
	}
	var request struct{ Version string }
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 1024)).Decode(&request); err != nil || !stableTag.MatchString(request.Version) {
		http.Error(w, "更新版本无效。", http.StatusBadRequest)
		return
	}
	if !s.mu.TryLock() {
		http.Error(w, "正在准备更新。", http.StatusConflict)
		return
	}
	defer s.mu.Unlock()
	if s.started {
		http.Error(w, "安装器已启动。", http.StatusConflict)
		return
	}

	ctx, cancel := context.WithTimeout(r.Context(), 5*time.Minute)
	defer cancel()
	newer, err := s.latest(ctx)
	if err != nil {
		log.Printf("确认 Knooo 更新：%v", err)
		http.Error(w, "无法确认更新，请稍后重试。", http.StatusBadGateway)
		return
	}
	if newer == nil || newer.Version != request.Version || newer.hash == "" {
		http.Error(w, "可用版本已变化，请重新检查更新。", http.StatusConflict)
		return
	}
	if err := s.downloadAndLaunch(ctx, *newer); err != nil {
		log.Printf("安装 Knooo 更新 %s：%v", newer.Version, err)
		http.Error(w, "下载、校验或启动安装失败。可重试，或前往 GitHub Releases 下载。", http.StatusBadGateway)
		return
	}
	s.started = true
	reply(w, map[string]bool{"started": true})
	s.shutdown()
}

func (s *service) latest(ctx context.Context) (*candidate, error) {
	if s.api == "" || !semver.IsValid(s.current) {
		return nil, nil
	}
	response, err := s.get(ctx, s.api)
	if err != nil {
		return nil, err
	}
	defer response.Body.Close()
	if response.StatusCode == http.StatusNotFound {
		return nil, nil
	}
	if response.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("release API status %d", response.StatusCode)
	}
	var published release
	if err := json.NewDecoder(io.LimitReader(response.Body, 2<<20)).Decode(&published); err != nil {
		return nil, err
	}
	if published.Draft || published.Prerelease || !stableTag.MatchString(published.Tag) || semver.Compare(published.Tag, s.current) <= 0 {
		return nil, nil
	}
	newer := &candidate{Version: published.Tag}
	for _, asset := range published.Assets {
		digest, ok := strings.CutPrefix(asset.Digest, "sha256:")
		if asset.Name == setupName && asset.State == "uploaded" && asset.Size > 0 && asset.Size <= maxSetup && ok {
			if decoded, err := hex.DecodeString(digest); err == nil && len(decoded) == sha256.Size {
				newer.size, newer.hash = asset.Size, strings.ToLower(digest)
			}
		}
	}
	return newer, nil
}

func (s *service) downloadAndLaunch(ctx context.Context, newer candidate) error {
	cache, err := os.UserCacheDir()
	if err != nil {
		return err
	}
	root := filepath.Join(cache, "Knooo", "updates")
	if err := os.MkdirAll(root, 0700); err != nil {
		return err
	}
	// A launched installer may still hold its file when the new app opens.
	if entries, err := os.ReadDir(root); err == nil {
		for _, entry := range entries {
			if entry.IsDir() && strings.HasPrefix(entry.Name(), "setup-") {
				if info, err := entry.Info(); err == nil && time.Since(info.ModTime()) > 24*time.Hour {
					_ = os.RemoveAll(filepath.Join(root, entry.Name()))
				}
			}
		}
	}
	dir, err := os.MkdirTemp(root, "setup-")
	if err != nil {
		return err
	}
	launched := false
	defer func() {
		if !launched {
			_ = os.RemoveAll(dir)
		}
	}()
	path := filepath.Join(dir, setupName)
	target := s.repository + "/releases/download/" + url.PathEscape(newer.Version) + "/" + setupName
	response, err := s.get(ctx, target)
	if err != nil {
		return err
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		return fmt.Errorf("installer download status %d", response.StatusCode)
	}
	file, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0600)
	if err != nil {
		return err
	}
	sum := sha256.New()
	n, err := io.Copy(io.MultiWriter(file, sum), io.LimitReader(response.Body, newer.size+1))
	closeErr := file.Close()
	if err != nil || closeErr != nil {
		return errors.Join(err, closeErr)
	}
	if n != newer.size || hex.EncodeToString(sum.Sum(nil)) != newer.hash {
		return errors.New("installer size or SHA-256 mismatch")
	}
	if err := s.install(path); err != nil {
		return err
	}
	launched = true
	return nil
}

func (s *service) get(ctx context.Context, target string) (*http.Response, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, target, nil)
	if err != nil {
		return nil, err
	}
	if target == s.api {
		req.Header.Set("Accept", "application/vnd.github+json")
	}
	req.Header.Set("User-Agent", "Knooo")
	return s.client.Do(req)
}

func reply(w http.ResponseWriter, value any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	_ = json.NewEncoder(w).Encode(value)
}
