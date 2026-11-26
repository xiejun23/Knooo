// Package sessions discovers and reads local agent transcripts.
package sessions

import (
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
	"sync"
	"time"
)

type Session struct {
	ID                 string    `json:"id"`
	Agent              string    `json:"agent"`
	AgentVariant       string    `json:"agentVariant,omitempty"`
	Title              string    `json:"title"`
	Project            string    `json:"project"`
	ProjectPath        string    `json:"projectPath"`
	ProjectDisplayPath string    `json:"projectDisplayPath,omitempty"`
	Platform           string    `json:"platform"`
	WSL                string    `json:"wsl,omitempty"`
	Cwd                string    `json:"cwd"`
	Path               string    `json:"path"`
	SourceRoot         string    `json:"sourceRoot"`
	Start              string    `json:"start"`
	Updated            string    `json:"updated"`
	ParentID           string    `json:"parentId"`
	Messages           []Message `json:"messages"`
}

type ScanReport struct {
	Environments []Environment `json:"environments"`
	Roots        []ScanRoot    `json:"roots"`

	MessageRevisions    map[string]string `json:"-"`
	DiscoveryTime       time.Duration     `json:"-"`
	FileRefreshTime     time.Duration     `json:"-"`
	CacheWriteTime      time.Duration     `json:"-"`
	MessageRevisionTime time.Duration     `json:"-"`
}

type ScanRoot struct {
	Agent        string `json:"agent"`
	AgentVariant string `json:"agentVariant,omitempty"`
	Platform     string `json:"platform"`
	WSL          string `json:"wsl,omitempty"`
	Directory    string `json:"directory"`
	State        string `json:"state"`
	Sessions     int    `json:"sessions"`
}

type Message struct {
	ID        string `json:"id"`
	Role      string `json:"role"`
	Text      string `json:"text"`
	Timestamp string `json:"timestamp"`
	Line      int    `json:"line"` // One-based physical line in Session.Path.
}

type Store struct {
	home       string
	mu         sync.Mutex
	files      map[source]*cachedFile
	cachePath  string
	diskLoaded bool
	disk       map[source]diskCacheEntry
	diskSize   int
	diskDirty  bool
}

type source struct {
	agent string
	path  string
}

type discoveredSource struct {
	source
	codexHome string
	cwd       string
	root      int
}

var agents = []string{"codex", "claude", "pi", "workbuddy", "dsh", "antigravity", "gemini", "copilot", "qwen"}

func SupportedAgent(agent string) bool {
	return slices.Contains(agents, agent)
}

func (session Session) MatchesAgent(filters ...string) bool {
	hasFilter := false
	for _, filter := range filters {
		if filter == "" {
			continue
		}
		hasFilter = true
		if filter == session.Agent ||
			(session.AgentVariant != "" && filter == session.Agent+":"+session.AgentVariant) {
			return true
		}
	}
	return !hasFilter
}

func (src source) compressed() bool {
	return src.agent == "codex" && strings.HasSuffix(strings.ToLower(src.path), ".jsonl.zst") ||
		src.agent == "dsh" && strings.HasSuffix(src.path, ".jsonl.zstd")
}

// New creates a store with an in-memory cache. An empty home uses the OS user home.
func New(home string) *Store {
	if home == "" {
		home, _ = os.UserHomeDir()
	}
	return &Store{home: home, files: make(map[source]*cachedFile)}
}

// Scan reads default locations and any extra directories keyed by agent ID.
// It returns the current snapshot and non-fatal diagnostics.
func (s *Store) Scan(extra map[string][]string) ([]Session, []string, ScanReport) {
	s.mu.Lock()
	defer s.mu.Unlock()

	s.ensureDiskCacheLoaded()
	discoverStarted := time.Now()
	mounts := wslWindowsMounts()
	sources, warnings, report := s.discover(extra, mounts)
	report.DiscoveryTime = time.Since(discoverStarted)

	refreshStarted := time.Now()
	type refreshResult struct {
		file    *cachedFile
		changed bool
		err     error
	}
	results := make([]refreshResult, len(sources))
	workers := min(len(sources), min(max(runtime.GOMAXPROCS(0), 2), 6))
	if workers <= 1 {
		for i, discovered := range sources {
			src := discovered.source
			var diskPtr *diskCacheEntry
			if entry, ok := s.disk[src]; ok {
				diskPtr = &entry
			}
			file, changed, err := src.refreshWithDisk(s.files[src], diskPtr)
			results[i] = refreshResult{file: file, changed: changed, err: err}
		}
	} else {
		jobs := make(chan int, len(sources))
		for i := range sources {
			jobs <- i
		}
		close(jobs)
		var wg sync.WaitGroup
		wg.Add(workers)
		for range workers {
			go func() {
				defer wg.Done()
				for i := range jobs {
					src := sources[i].source
					var diskPtr *diskCacheEntry
					if entry, ok := s.disk[src]; ok {
						diskPtr = &entry
					}
					file, changed, err := src.refreshWithDisk(s.files[src], diskPtr)
					results[i] = refreshResult{file: file, changed: changed, err: err}
				}
			}()
		}
		wg.Wait()
	}
	report.FileRefreshTime = time.Since(refreshStarted)

	files := make(map[source]*cachedFile, len(sources))
	byID := make(map[string]candidate)
	piIDsByPath := make(map[string]string)
	projects := make(map[string]string)
	namesByHome := make(map[string]map[string]string)
	antigravityByIndex := make(map[string]map[string]antigravitySummary)
	report.MessageRevisions = make(map[string]string, len(sources))
	for i, discovered := range sources {
		src := discovered.source
		root := &report.Roots[discovered.root]
		cached := s.files[src]
		res := results[i]
		file, err := res.file, res.err
		if err != nil {
			root.State = "partial"
			warnings = append(warnings, fmt.Sprintf("%s: %v", src.agent, err))
			file = cached
		}
		s.diskDirty = s.diskDirty || res.changed
		if file == nil {
			continue
		}
		files[src] = file
		if file.badLines > 0 {
			root.State = "partial"
			unit := "JSONL line(s)"
			if src.agent == "antigravity" {
				unit = "conversation step(s)"
			}
			warnings = append(warnings, fmt.Sprintf("%s: %s: skipped %d invalid or oversized %s", src.agent, src.path, file.badLines, unit))
		}
		current := file.parsed
		if current.session.ID == "" {
			continue
		}
		current.session.SourceRoot = root.Directory
		current.session.AgentVariant = root.AgentVariant
		current.session.Platform, current.session.WSL = root.Platform, root.WSL
		if discovered.cwd != "" {
			current.session.Cwd = discovered.cwd
		}
		if src.agent == "workbuddy" && current.session.Cwd == "" {
			current.session.Cwd = workBuddyCwd(src.path)
		}
		current.root = discovered.root
		if src.agent == "pi" {
			piIDsByPath[src.path] = current.session.ID
		}
		if home := discovered.codexHome; home != "" {
			names, ok := namesByHome[home]
			if !ok {
				names = codexNames(home)
				namesByHome[home] = names
			}
			id := strings.TrimPrefix(current.session.ID, "codex:")
			current.session.Title = firstNonEmpty(names[id], current.session.Title)
		}
		if src.agent == "antigravity" {
			index := filepath.Join(filepath.Dir(src.path), "..", "conversation_summaries.db")
			summaries, ok := antigravityByIndex[index]
			if !ok {
				var err error
				summaries, err = antigravitySummaries(index)
				antigravityByIndex[index] = summaries
				if err != nil {
					root.State = "partial"
					warnings = append(warnings, fmt.Sprintf("%s: %s: %v", src.agent, index, err))
				}
			}
			id := strings.TrimPrefix(current.session.ID, "antigravity:")
			summary := summaries[id]
			current.session.Title = firstNonEmpty(summary.title, current.session.Title)
			current.session.Cwd = firstNonEmpty(summary.cwd, current.session.Cwd)
			if summary.parent != "" && summary.parent != id {
				current.session.ParentID = "antigravity:" + summary.parent
			}
			if summary.updated.After(current.updated) {
				current.updated = summary.updated
				current.session.Updated = formatTime(summary.updated)
			}
		}
		previous, exists := byID[current.session.ID]
		if !exists || current.preferredTo(previous) {
			if file.messageRevision == "" {
				revisionStarted := time.Now()
				file.messageRevision = MessageRevision(current.session.Messages)
				report.MessageRevisionTime += time.Since(revisionStarted)
			}
			report.MessageRevisions[current.session.ID] = file.messageRevision
			byID[current.session.ID] = current
		}
	}
	s.files = files
	if s.cachePath != "" && (s.diskDirty || len(files) != s.diskSize) {
		cacheStarted := time.Now()
		if s.saveDiskCache(files) {
			s.diskDirty = false
			s.diskSize = len(files)
		}
		report.CacheWriteTime = time.Since(cacheStarted)
	}
	s.disk = nil

	result := make([]Session, 0, len(byID))
	for _, entry := range byID {
		session := entry.session
		report.Roots[entry.root].Sessions++
		owner := session.ParentID
		switch {
		case session.Agent == "pi" && entry.parentRef != "":
			session.ParentID = piParentID(entry.parentRef, session.Path, piIDsByPath, mounts)
		case session.Agent == "claude" && owner != "":
			if subagentOwner, _ := claudeSubagent(session.Path); subagentOwner != "" {
				session.ParentID = claudeParentID(session.Path, owner)
			}
		}
		if session.ParentID == session.ID {
			session.ParentID = owner
		}
		if session.Cwd == "" && session.ParentID != "" {
			session.Cwd = firstNonEmpty(byID[session.ParentID].session.Cwd, byID[owner].session.Cwd)
		}
		if session.Cwd != "" {
			localPath := projectDirectory(session.Cwd, session.Path)
			native := ""
			if session.Platform == "windows" && localWSLDistro() != "" {
				native = windowsPath(session.Cwd)
				if native != "" {
					localPath = mounts.localPath(native)
					session.ProjectPath, session.Project = session.Cwd, filepath.Base(native)
				}
			}
			if localPath != "" {
				root, ok := projects[localPath]
				if !ok {
					root = gitRoot(localPath)
					projects[localPath] = root
				}
				session.ProjectPath = root
				session.Project = filepath.Base(root)
				_, session.ProjectDisplayPath = wslPathParts(root)
				if native != "" {
					session.ProjectDisplayPath = firstNonEmpty(mounts.nativePath(root), session.Cwd)
				}
			}
		}
		session.Messages = slices.Clone(session.Messages)
		result = append(result, session)
	}
	slices.SortFunc(result, func(a, b Session) int {
		if order := byID[b.ID].updated.Compare(byID[a.ID].updated); order != 0 {
			return order
		}
		return strings.Compare(a.ID, b.ID)
	})
	slices.Sort(warnings)
	return result, slices.Compact(warnings), report
}

type candidate struct {
	session   Session
	updated   time.Time
	parentRef string // Pi uses a file path for its parent session.
	root      int
}

func (c candidate) preferredTo(other candidate) bool {
	if order := c.updated.Compare(other.updated); order != 0 {
		return order > 0
	}
	if a, b := len(c.session.Messages), len(other.session.Messages); a != b {
		return a > b
	}
	if c.session.Agent == "codex" {
		archived := func(path string) bool {
			return slices.Contains(strings.Split(filepath.ToSlash(path), "/"), "archived_sessions")
		}
		if a, b := archived(c.session.Path), archived(other.session.Path); a != b {
			return !a
		}
	}
	return c.session.Path < other.session.Path
}

func piParentID(ref, sourcePath string, byPath map[string]string, mounts windowsMounts) string {
	if !strings.ContainsAny(ref, `/\`) && !strings.HasSuffix(ref, ".jsonl") {
		return "pi:" + ref
	}
	path := projectDirectory(ref, sourcePath)
	if mapped := mounts.localPath(path); mapped != "" {
		path = mapped
	}
	if !filepath.IsAbs(path) {
		path = filepath.Join(filepath.Dir(sourcePath), path)
	}
	path = filepath.Clean(path)
	if id := byPath[path]; id != "" {
		return id
	}
	if resolved, err := filepath.EvalSymlinks(path); err == nil && byPath[resolved] != "" {
		return byPath[resolved]
	}
	name := filepath.Base(strings.ReplaceAll(ref, `\`, "/"))
	stem := strings.TrimSuffix(name, ".jsonl")
	if i := strings.LastIndexByte(stem, '_'); i >= 0 && i+1 < len(stem) {
		return "pi:" + stem[i+1:]
	}
	return ""
}

func (s *Store) discover(extra map[string][]string, mounts windowsMounts) ([]discoveredSource, []string, ScanReport) {
	roots := make(map[string]map[string]bool)
	for _, agent := range agents {
		roots[agent] = make(map[string]bool)
	}
	add := func(agent, dir string, configured bool) {
		if dir != "" {
			roots[agent][dir] = roots[agent][dir] || configured
		}
	}
	folders := map[string][]string{"codex": {"sessions", "archived_sessions"}, "claude": {"projects"}}
	addProfiles := func(agent, home, configured string) {
		for _, folder := range folders[agent] {
			add(agent, filepath.Join(home, "."+agent, folder), false)
			if configured != "" {
				add(agent, filepath.Join(configured, folder), false)
			}
		}
		hasHistory := func(dir string) bool {
			found := false
			for _, folder := range folders[agent] {
				path := filepath.Join(dir, folder)
				if info, err := os.Stat(path); err == nil && info.IsDir() {
					add(agent, path, false)
					found = true
				}
			}
			return found
		}
		// Magpie also looks for adjacent account homes and one level under
		// a profile container such as ~/.codex-profiles.
		parents := []string{home}
		if parent := filepath.Dir(configured); configured != "" && parent != home {
			parents = append(parents, parent)
		}
		for _, parent := range parents {
			entries, _ := os.ReadDir(parent)
			for _, entry := range entries {
				if !strings.HasPrefix(strings.TrimPrefix(strings.ToLower(entry.Name()), "."), agent) {
					continue
				}
				dir := filepath.Join(parent, entry.Name())
				if hasHistory(dir) {
					continue
				}
				children, _ := os.ReadDir(dir)
				for _, child := range children {
					if child.IsDir() || child.Type()&os.ModeSymlink != 0 {
						hasHistory(filepath.Join(dir, child.Name()))
					}
				}
			}
		}
	}
	addPi := func(home, agentDir, sessionDir string, remoteWindows bool) {
		add("pi", filepath.Join(agentDir, "sessions"), false)
		if sessionDir == "" {
			var settings struct {
				SessionDir string `json:"sessionDir"`
			}
			if raw, err := os.ReadFile(filepath.Join(agentDir, "settings.json")); err == nil {
				if json.Unmarshal(raw, &settings) == nil {
					sessionDir = settings.SessionDir
				}
			}
		}
		if sessionDir == "~" {
			sessionDir = home
		} else if strings.HasPrefix(sessionDir, "~/") || strings.HasPrefix(sessionDir, `~\`) {
			sessionDir = sessionDir[2:]
			if remoteWindows {
				sessionDir = strings.ReplaceAll(sessionDir, `\`, "/")
			}
			sessionDir = filepath.Join(home, sessionDir)
		} else if remoteWindows {
			sessionDir = mounts.localPath(sessionDir)
		}
		if filepath.IsAbs(sessionDir) {
			add("pi", sessionDir, false)
		} else if sessionDir != "" && strings.HasPrefix(sessionDir, "/") {
			add("pi", projectDirectory(sessionDir, agentDir), false)
		}
	}
	addHome := func(home string, getenv func(string) string, remoteWindows bool) {
		if home == "" {
			return
		}
		configPath := func(name string) string {
			value := getenv(name)
			if remoteWindows {
				return mounts.localPath(value)
			}
			return value
		}
		var codex, claude, pi, piSessions string
		if getenv != nil {
			codex, claude = configPath("CODEX_HOME"), configPath("CLAUDE_CONFIG_DIR")
			pi, piSessions = configPath("PI_CODING_AGENT_DIR"), getenv("PI_CODING_AGENT_SESSION_DIR")
		}
		addProfiles("codex", home, codex)
		addProfiles("claude", home, claude)
		add("gemini", filepath.Join(home, ".gemini", "tmp"), false)
		add("antigravity", filepath.Join(home, ".gemini", "antigravity", "conversations"), false)
		add("antigravity", filepath.Join(home, ".gemini", "antigravity-cli", "conversations"), false)
		add("copilot", filepath.Join(home, ".copilot", "session-state"), false)
		add("qwen", filepath.Join(home, ".qwen", "projects"), false)
		add("workbuddy", filepath.Join(home, ".workbuddy", "projects"), false)
		add("workbuddy", filepath.Join(home, ".workbuddy-ai", "projects"), false)
		add("dsh", filepath.Join(home, ".dsh", "sessions"), false)
		if getenv != nil {
			if dir := strings.TrimSpace(configPath("WORKBUDDY_CONFIG_DIR")); dir != "" {
				add("workbuddy", filepath.Join(projectDirectory(dir, home), "projects"), false)
			}
			if dir := strings.TrimSpace(configPath("DSH_HOME")); dir != "" {
				add("dsh", filepath.Join(projectDirectory(dir, home), "sessions"), false)
			}
			if dir := configPath("GEMINI_CLI_HOME"); filepath.IsAbs(dir) {
				add("gemini", filepath.Join(dir, ".gemini", "tmp"), false)
			}
			if dir := configPath("COPILOT_HOME"); filepath.IsAbs(dir) {
				add("copilot", filepath.Join(dir, "session-state"), false)
			}
			for _, name := range []string{"QWEN_HOME", "QWEN_RUNTIME_DIR"} {
				if dir := configPath(name); filepath.IsAbs(dir) {
					add("qwen", filepath.Join(dir, "projects"), false)
				}
			}
		}
		piDefault := filepath.Join(home, ".pi", "agent")
		if pi != "" && filepath.Clean(pi) != piDefault {
			addPi(home, piDefault, "", remoteWindows)
			addPi(home, pi, piSessions, remoteWindows)
		} else {
			addPi(home, piDefault, piSessions, remoteWindows)
		}
	}
	addHome(s.home, os.Getenv, false)
	platform, distro := locationOf(s.home)
	report := ScanReport{Environments: []Environment{{Platform: platform, WSL: distro, Home: s.home, State: "scanned"}}, Roots: []ScanRoot{}}
	var warnings []string
	var windowsHome string
	if home, err := os.UserHomeDir(); err == nil && filepath.Clean(home) == filepath.Clean(s.home) {
		if localWSLDistro() != "" {
			environment, variables, err := windowsEnvironment()
			report.Environments = append(report.Environments, environment)
			if err != nil {
				warnings = append(warnings, err.Error())
			} else {
				windowsHome = environment.Home
				addHome(windowsHome, func(name string) string { return variables[name] }, true)
			}
		}
		for _, environment := range wslEnvironments() {
			report.Environments = append(report.Environments, environment)
			addHome(environment.Home, nil, false)
		}
	}

	for agent, dirs := range extra {
		if _, ok := roots[agent]; !ok {
			warnings = append(warnings, fmt.Sprintf("unknown session agent %q", agent))
			continue
		}
		for _, dir := range dirs {
			if mapped := mounts.localPath(dir); mapped != "" {
				dir = mapped
			}
			add(agent, dir, true)
		}
	}

	paths := make(map[source]discoveredSource)
	dshFiles := make(map[string]dshFile)
	for _, agent := range agents {
		dirs := roots[agent]
		directories := make([]string, 0, len(dirs))
		for dir := range dirs {
			directories = append(directories, dir)
		}
		slices.Sort(directories)
		for _, dir := range directories {
			root, err := filepath.Abs(dir)
			if err != nil {
				root = dir
			}
			platform, distro := locationOf(root)
			index := len(report.Roots)
			report.Roots = append(report.Roots, ScanRoot{Agent: agent, Platform: platform, WSL: distro, Directory: root, State: "scanned"})
			status := &report.Roots[index]
			if agent == "antigravity" {
				status.AgentVariant = antigravityVariant(root)
			}
			var codexHome string
			if agent == "codex" && (filepath.Base(root) == "sessions" || filepath.Base(root) == "archived_sessions") {
				codexHome = filepath.Dir(root)
			}
			if err == nil {
				// Some WSL UNC ancestors cannot be resolved, but Stat still succeeds.
				if resolved, resolveErr := filepath.EvalSymlinks(root); resolveErr == nil {
					root = resolved
					if agent == "antigravity" && status.AgentVariant == "" {
						status.AgentVariant = antigravityVariant(root)
					}
				}
				var info fs.FileInfo
				info, err = os.Stat(root)
				if err == nil && !info.IsDir() {
					err = fmt.Errorf("%s: expected a session directory", root)
				}
			}
			if mounts.containing(status.Directory).local != "" || mounts.containing(root).local != "" || pathWithin(status.Directory, windowsHome) {
				status.Platform, status.WSL = "windows", ""
			}
			if err != nil {
				status.State = "unavailable"
				if errors.Is(err, fs.ErrNotExist) {
					status.State = "missing"
				}
				if dirs[dir] || !errors.Is(err, fs.ErrNotExist) {
					warnings = append(warnings, fmt.Sprintf("%s: %v", agent, err))
				}
				continue
			}
			var geminiProjects map[string]string
			if agent == "gemini" {
				geminiProjects = geminiProjectPaths(root)
			}
			err = filepath.WalkDir(root, func(path string, entry fs.DirEntry, err error) error {
				if err != nil {
					status.State = "partial"
					warnings = append(warnings, fmt.Sprintf("%s: %v", agent, err))
					return nil
				}
				if !entry.Type().IsRegular() {
					return nil
				}
				src := source{agent: agent, path: path}
				if agent == "dsh" {
					generation, ok := dshGeneration(entry.Name())
					if !ok {
						return nil
					}
					info, err := entry.Info()
					if err != nil {
						status.State = "partial"
						warnings = append(warnings, fmt.Sprintf("%s: %v", agent, err))
						return nil
					}
					candidate := dshFile{discoveredSource{source: src, root: index}, generation, info.ModTime()}
					sessionDir := filepath.Dir(path)
					previous, exists := dshFiles[sessionDir]
					if !exists || candidate.preferredTo(previous) {
						dshFiles[sessionDir] = candidate
					}
					return nil
				}
				if src.isTranscript() {
					if previous, exists := paths[src]; !exists || (previous.codexHome == "" && codexHome != "") {
						var cwd string
						if geminiProjects != nil {
							cwd = geminiProjects[geminiProjectDir(path)]
						}
						paths[src] = discoveredSource{source: src, codexHome: codexHome, cwd: cwd, root: index}
					}
				}
				return nil
			})
			if err != nil {
				status.State = "partial"
				warnings = append(warnings, fmt.Sprintf("%s: %v", agent, err))
			}
		}
	}
	out := make([]discoveredSource, 0, len(paths)+len(dshFiles))
	for _, file := range dshFiles {
		out = append(out, file.discoveredSource)
	}
	for src, discovered := range paths {
		if src.compressed() {
			if _, exists := paths[source{agent: src.agent, path: src.path[:len(src.path)-len(".zst")]}]; exists {
				continue
			}
		}
		out = append(out, discovered)
	}
	slices.SortFunc(out, func(a, b discoveredSource) int {
		if order := strings.Compare(a.agent, b.agent); order != 0 {
			return order
		}
		return strings.Compare(a.path, b.path)
	})
	return out, warnings, report
}

func gitRoot(cwd string) string {
	cwd = filepath.Clean(cwd)
	if !filepath.IsAbs(cwd) {
		return cwd
	}
	if resolved, err := filepath.EvalSymlinks(cwd); err == nil {
		cwd = resolved
	}
	for dir := cwd; ; dir = filepath.Dir(dir) {
		if marker, err := os.Stat(filepath.Join(dir, ".git")); err == nil && (marker.IsDir() || marker.Mode().IsRegular()) {
			return dir
		}
		if dir == filepath.Dir(dir) {
			return cwd
		}
	}
}
