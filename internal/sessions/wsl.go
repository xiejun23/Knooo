package sessions

import (
	"bufio"
	"context"
	"encoding/binary"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
	"time"
	"unicode"
	"unicode/utf16"
)

type Environment struct {
	Platform string `json:"platform"`
	WSL      string `json:"wsl,omitempty"`
	Home     string `json:"home,omitempty"`
	State    string `json:"state"`
}

func wslEnvironments() []Environment {
	local := localWSLDistro()
	if runtime.GOOS != "windows" && local == "" {
		return nil
	}
	list := func(running bool) ([]string, error) {
		args := []string{"--list", "--quiet"}
		if running {
			args = append(args, "--running")
		}
		out, err := environmentCommand("wsl.exe", args...)
		if err != nil {
			return nil, err
		}
		return wslDistributions(out), nil
	}
	registered, listErr := list(false)
	running, runErr := list(true)
	if listErr != nil && runErr != nil {
		if local != "" {
			return nil
		}
		return []Environment{{Platform: "wsl", State: "unavailable"}}
	}
	var result []Environment
	for _, distro := range running {
		if local == "" {
			result = append(result, wslEnvironment(distro))
		} else if !strings.EqualFold(distro, local) {
			result = append(result, Environment{Platform: "wsl", WSL: distro, State: "unavailable"})
		}
	}
	for _, distro := range registered {
		if !strings.EqualFold(distro, local) && !slices.Contains(running, distro) {
			state := "stopped"
			if runErr != nil {
				state = "unavailable"
			}
			result = append(result, Environment{Platform: "wsl", WSL: distro, State: state})
		}
	}
	if len(result) == 0 && local == "" {
		state := "empty"
		if listErr != nil {
			state = "unavailable"
		}
		result = append(result, Environment{Platform: "wsl", State: state})
	}
	return result
}

func wslEnvironment(distro string) Environment {
	result := Environment{Platform: "wsl", WSL: distro, State: "unavailable"}
	out, err := environmentCommand("wsl.exe", "--distribution", distro, "--exec", "/bin/sh", "-c", `printf '%s' "$HOME"`)
	if err != nil {
		return result
	}
	home := strings.TrimRight(string(out), "\r\n")
	if !path.IsAbs(home) || home == "/" || path.Clean(home) != home || strings.ContainsAny(home, `\:`) || strings.IndexFunc(home, unicode.IsControl) >= 0 {
		return result
	}
	for _, host := range []string{`\\wsl.localhost\`, `\\wsl$\`} {
		dir := filepath.Join(host+distro, filepath.FromSlash(strings.TrimPrefix(home, "/")))
		if info, err := os.Stat(dir); err == nil && info.IsDir() {
			result.Home, result.State = dir, "scanned"
			break
		}
	}
	return result
}

func environmentCommand(name string, args ...string) ([]byte, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	command := exec.CommandContext(ctx, name, args...)
	command.WaitDelay = 250 * time.Millisecond
	return command.Output()
}

func windowsEnvironment() (Environment, map[string]string, error) {
	result := Environment{Platform: "windows", State: "unavailable"}
	out, err := environmentCommand("powershell.exe", "-NoLogo", "-NoProfile", "-NonInteractive", "-Command", `
$ErrorActionPreference = 'Stop'
[Console]::OutputEncoding = [System.Text.UTF8Encoding]::new($false)
$values = @{ UserProfile = [Environment]::GetFolderPath('UserProfile') }
foreach ($name in @('CODEX_HOME','CLAUDE_CONFIG_DIR','PI_CODING_AGENT_DIR','PI_CODING_AGENT_SESSION_DIR','WORKBUDDY_CONFIG_DIR','DSH_HOME','GEMINI_CLI_HOME','COPILOT_HOME','QWEN_HOME','QWEN_RUNTIME_DIR')) {
    $value = [Environment]::GetEnvironmentVariable($name, 'User')
    if (!$value) { $value = [Environment]::GetEnvironmentVariable($name, 'Machine') }
    if ($value) { $values[$name] = $value }
}
[Console]::WriteLine(($values | ConvertTo-Json -Compress))
`)
	if err != nil {
		return result, nil, fmt.Errorf("Windows: could not query the current user profile: %w", err)
	}
	var values map[string]string
	if json.Unmarshal(out, &values) != nil || windowsPath(values["UserProfile"]) == "" {
		return result, nil, fmt.Errorf("Windows: invalid current user profile")
	}
	out, err = environmentCommand("wslpath", "-u", values["UserProfile"])
	if err != nil {
		return result, nil, fmt.Errorf("Windows: could not map the current user profile through WSL: %w", err)
	}
	home := strings.TrimRight(string(out), "\r\n")
	if !filepath.IsAbs(home) || home == "/" || filepath.Clean(home) != home || strings.IndexFunc(home, unicode.IsControl) >= 0 {
		return result, nil, fmt.Errorf("Windows: invalid mapped user profile")
	}
	if info, err := os.Stat(home); err != nil || !info.IsDir() {
		return result, nil, fmt.Errorf("Windows: the mapped user profile is unavailable")
	}
	return Environment{Platform: "windows", Home: home, State: "scanned"}, values, nil
}

func localWSLDistro() string {
	if runtime.GOOS == "linux" {
		return os.Getenv("WSL_DISTRO_NAME")
	}
	return ""
}

func locationOf(file string) (platform, distro string) {
	if name, _ := wslPathParts(file); name != "" {
		return "wsl", name
	}
	if name := localWSLDistro(); name != "" {
		return "wsl", name
	}
	return runtime.GOOS, ""
}

type windowsMount struct {
	local  string
	native string
}

type windowsMounts []windowsMount

func wslWindowsMounts() windowsMounts {
	if localWSLDistro() == "" {
		return nil
	}
	file, err := os.Open("/proc/self/mountinfo")
	if err != nil {
		return nil
	}
	defer file.Close()
	return readWindowsMounts(bufio.NewScanner(file))
}

func readWindowsMounts(lines *bufio.Scanner) windowsMounts {
	unescape := strings.NewReplacer(`\040`, " ", `\011`, "\t", `\012`, "\n", `\134`, `\`)
	var mounts windowsMounts
	for lines.Scan() {
		before, after, _ := strings.Cut(lines.Text(), " - ")
		fields, filesystem := strings.Fields(before), strings.Fields(after)
		if len(fields) < 5 || len(filesystem) < 3 {
			continue
		}
		if filesystem[0] != "drvfs" && (filesystem[0] != "9p" ||
			!strings.Contains(","+filesystem[2], ",aname=drvfs;")) {
			continue
		}
		native := windowsPath(unescape.Replace(filesystem[1]))
		if native != "" {
			native = strings.TrimRight(native, "/") + strings.TrimRight(unescape.Replace(fields[3]), "/")
		}
		mounts = append(mounts, windowsMount{local: unescape.Replace(fields[4]), native: native})
	}
	return mounts
}

func windowsPath(value string) string {
	if strings.IndexFunc(value, unicode.IsControl) >= 0 {
		return ""
	}
	normalized := strings.ReplaceAll(value, `\`, "/")
	if len(normalized) >= 3 && normalized[1] == ':' && normalized[2] == '/' {
		drive := normalized[0] | 0x20
		if drive >= 'a' && drive <= 'z' {
			return strings.ToUpper(normalized[:1]) + ":" + path.Clean(normalized[2:])
		}
	}
	if strings.HasPrefix(normalized, "//") {
		server, rest, _ := strings.Cut(normalized[2:], "/")
		share, suffix, _ := strings.Cut(rest, "/")
		if server != "" && server != "." && server != "?" && share != "" && share != "." && share != ".." {
			return "//" + server + "/" + share + path.Clean("/"+suffix)
		}
	}
	return ""
}

func (mounts windowsMounts) localPath(native string) string {
	native = windowsPath(native)
	var best windowsMount
	for _, mount := range mounts {
		prefix := mount.native
		if len(prefix) > len(best.native) && len(native) >= len(prefix) &&
			strings.EqualFold(native[:len(prefix)], prefix) &&
			(len(native) == len(prefix) || native[len(prefix)] == '/') {
			best = mount
		}
	}
	if best.native == "" {
		return ""
	}
	return filepath.Join(best.local, native[len(best.native):])
}

func (mounts windowsMounts) containing(file string) windowsMount {
	var best windowsMount
	for _, mount := range mounts {
		if pathWithin(file, mount.local) && len(mount.local) > len(best.local) {
			best = mount
		}
	}
	return best
}

func pathWithin(file, dir string) bool {
	return dir != "" && (file == dir || strings.HasPrefix(file, strings.TrimRight(dir, "/")+"/"))
}

func (mounts windowsMounts) nativePath(file string) string {
	mount := mounts.containing(file)
	if mount.native == "" {
		return ""
	}
	suffix := strings.TrimPrefix(strings.TrimPrefix(file, mount.local), "/")
	if suffix != "" || strings.HasSuffix(mount.native, ":") {
		suffix = "/" + suffix
	}
	return strings.ReplaceAll(mount.native+suffix, "/", `\`)
}

func wslPathParts(file string) (distro, linuxPath string) {
	normalized := strings.ReplaceAll(file, `\`, "/")
	for _, host := range []string{"//wsl.localhost/", "//wsl$/"} {
		if strings.HasPrefix(strings.ToLower(normalized), host) {
			name, rest, _ := strings.Cut(normalized[len(host):], "/")
			if name != "" {
				return name, "/" + rest
			}
		}
	}
	return "", ""
}

func wslDistributions(out []byte) []string {
	text := string(out)
	if strings.ContainsRune(text, 0) || strings.HasPrefix(text, "\xff\xfe") {
		units := make([]uint16, 0, len(out)/2)
		for len(out) >= 2 {
			units = append(units, binary.LittleEndian.Uint16(out))
			out = out[2:]
		}
		text = string(utf16.Decode(units))
	}
	text = strings.TrimPrefix(text, "\ufeff")
	var names []string
	for _, line := range strings.Split(text, "\n") {
		name := strings.TrimSpace(line)
		if strings.Trim(name, ".") != "" && !strings.ContainsAny(name, `\/:`) && strings.IndexFunc(name, unicode.IsControl) == -1 {
			names = append(names, name)
		}
	}
	return names
}

func projectDirectory(cwd, sourcePath string) string {
	if runtime.GOOS != "windows" || !strings.HasPrefix(cwd, "/") || strings.HasPrefix(cwd, "//") {
		return cwd
	}
	volume := filepath.VolumeName(sourcePath)
	host := strings.ToLower(strings.ReplaceAll(volume, "/", `\`))
	if !strings.HasPrefix(host, `\\wsl.localhost\`) && !strings.HasPrefix(host, `\\wsl$\`) {
		return cwd
	}
	if strings.HasPrefix(cwd, "/mnt/") && len(cwd) >= 6 {
		drive := cwd[5]
		if drive >= 'a' && drive <= 'z' && (len(cwd) == 6 || cwd[6] == '/') {
			return strings.ToUpper(string(drive)) + `:\` + filepath.FromSlash(strings.TrimPrefix(cwd[6:], "/"))
		}
	}
	return filepath.Join(volume+`\`, filepath.FromSlash(strings.TrimPrefix(cwd, "/")))
}
