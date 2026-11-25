package sessions

import (
	"bufio"
	"bytes"
	"database/sql"
	"encoding/json"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	_ "modernc.org/sqlite"
)

func codexNames(home string) map[string]string {
	names := make(map[string]string)
	path := filepath.Join(home, "session_index.jsonl")
	if info, err := os.Stat(path); err == nil && info.Mode().IsRegular() {
		if file, err := os.Open(path); err == nil {
			defer file.Close()
			scan := bufio.NewScanner(file)
			// shortcut: stop at a name-index row over 1 MiB, increase the limit if Codex stores larger names.
			scan.Buffer(make([]byte, 4096), 1<<20)
			for scan.Scan() {
				var row struct {
					ID   string  `json:"id"`
					Name *string `json:"thread_name"`
				}
				line := bytes.TrimPrefix(scan.Bytes(), []byte("\xef\xbb\xbf"))
				if json.Unmarshal(line, &row) == nil && row.ID != "" && row.Name != nil {
					names[row.ID] = strings.TrimSpace(*row.Name)
				}
			}
		}
	}
	readCodexStateNames(home, names)
	return names
}

func readCodexStateNames(home string, names map[string]string) {
	entries, _ := os.ReadDir(home)
	var path string
	version := -1
	for _, entry := range entries {
		filename := entry.Name()
		if !entry.Type().IsRegular() || !strings.HasPrefix(filename, "state_") || !strings.HasSuffix(filename, ".sqlite") {
			continue
		}
		number := strings.TrimSuffix(strings.TrimPrefix(filename, "state_"), ".sqlite")
		if n, err := strconv.Atoi(number); err == nil && n > version {
			version, path = n, filepath.Join(home, filename)
		}
	}
	if path == "" {
		return
	}
	path = filepath.ToSlash(path)
	if !strings.HasPrefix(path, "/") {
		path = "/" + path
	}
	// shortcut: read checkpointed state only so SQLite never writes WAL sidecars or locks on WSL shares. The index supplies live renames.
	uri := url.URL{Scheme: "file", Path: path, RawQuery: "mode=ro&immutable=1"}
	db, err := sql.Open("sqlite", uri.String())
	if err != nil {
		return
	}
	defer db.Close()
	rows, err := db.Query(`SELECT id, COALESCE(NULLIF(TRIM(name), ''), title, '') FROM threads`)
	if err != nil {
		rows, err = db.Query(`SELECT id, COALESCE(title, '') FROM threads`)
		if err != nil {
			return
		}
	}
	defer rows.Close()
	for rows.Next() {
		var id, title string
		if rows.Scan(&id, &title) == nil {
			if _, indexed := names[id]; !indexed && id != "" {
				names[id] = strings.TrimSpace(title)
			}
		}
	}
}
