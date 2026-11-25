package sessions

import (
	"encoding/json"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"strings"
)

var workBuddyScene = regexp.MustCompile(`^@[\w-]+:\S*$`)

func workBuddyVisible(text string) bool {
	text = strings.TrimSpace(text)
	return !workBuddyScene.MatchString(text) && !strings.HasPrefix(text, "<system-reminder>")
}

func workBuddyCwd(path string) string {
	file, err := os.Open(strings.TrimSuffix(path, filepath.Ext(path)) + ".meta.json")
	if err != nil {
		return ""
	}
	defer file.Close()
	var metadata struct{ Cwd string }
	if err := json.NewDecoder(io.LimitReader(file, 64<<10)).Decode(&metadata); err != nil {
		return ""
	}
	return metadata.Cwd
}
