package sessions

import (
	"database/sql"
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"
	"unicode/utf8"
)

const (
	antigravityUserStep  = 14
	antigravityReplyStep = 15
)

var antigravityDBName = regexp.MustCompile(`(?i)^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}\.db$`)

func antigravityVariant(directory string) string {
	if filepath.Base(directory) == "conversations" {
		directory = filepath.Dir(directory)
	}
	switch strings.ToLower(filepath.Base(directory)) {
	case "antigravity-cli":
		return "cli"
	case "antigravity":
		return "app"
	default:
		return ""
	}
}

type antigravitySummary struct {
	title, cwd, parent string
	updated            time.Time
}

func antigravitySummaries(path string) (map[string]antigravitySummary, error) {
	snapshot, err := antigravityDBSnapshot(path)
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	summaries := make(map[string]antigravitySummary)
	err = snapshot.read(path, func(db *sql.DB) error {
		rows, err := db.Query(`SELECT conversation_id, coalesce(title, ''), coalesce(workspace_uris, ''),
			coalesce(CAST(last_modified_time AS TEXT), ''), coalesce(parent_conversation_id, '')
			FROM conversation_summaries`)
		if err != nil {
			return err
		}
		defer rows.Close()
		for rows.Next() {
			var id, title, workspaces, updated, parent string
			if err := rows.Scan(&id, &title, &workspaces, &updated, &parent); err != nil {
				return err
			}
			var uris []string
			if json.Unmarshal([]byte(workspaces), &uris) != nil {
				uris = []string{workspaces}
			}
			summary := antigravitySummary{title: strings.TrimSpace(title), parent: parent}
			for _, value := range uris {
				if summary.cwd = antigravityWorkspace(value); summary.cwd != "" {
					break
				}
			}
			for _, layout := range []string{time.RFC3339Nano, "2006-01-02 15:04:05Z07:00", time.DateTime} {
				if stamp, err := time.Parse(layout, updated); err == nil {
					summary.updated = stamp
					break
				}
			}
			summaries[id] = summary
		}
		return rows.Err()
	})
	return summaries, err
}

func antigravityWorkspace(value string) string {
	uri, err := url.Parse(value)
	if err != nil || uri.Scheme != "file" || !strings.HasPrefix(uri.Path, "/") || strings.ContainsRune(uri.Path, 0) {
		return ""
	}
	if uri.Host != "" && uri.Host != "localhost" {
		return "//" + uri.Host + uri.Path
	}
	if len(uri.Path) >= 4 && uri.Path[2:4] == ":/" {
		return uri.Path[1:]
	}
	return uri.Path
}

func (src source) refreshAntigravity(old *cachedFile, info os.FileInfo) (*cachedFile, error) {
	snapshot, err := antigravitySnapshotWithInfo(src.path, info)
	if err != nil {
		return nil, err
	}
	if old != nil && snapshot.same(antigravitySnapshot{old.info, old.wal}) {
		return old, nil
	}
	next := &cachedFile{info: info, wal: snapshot.wal}
	err = snapshot.read(src.path, func(db *sql.DB) error {
		var id string
		if err := db.QueryRow(`SELECT cascade_id FROM trajectory_meta`).Scan(&id); err != nil {
			return err
		}
		if id == "" {
			return errors.New("missing Antigravity conversation ID")
		}
		header := record{kind: "session", id: id}
		var metadata antigravityProto
		err := db.QueryRow(`SELECT data FROM trajectory_metadata_blob
			WHERE id = 'main' AND length(data) <= ?`, maxLineBytes).Scan(&metadata)
		if err != nil && !errors.Is(err, sql.ErrNoRows) {
			return err
		}
		workspace, _ := metadata.field(1)
		workspaceURI, _ := workspace.field(1)
		header.cwd = antigravityWorkspace(string(workspaceURI))
		started, _ := metadata.field(2)
		header.started = started.timestamp()
		records := []record{header}
		rows, err := db.Query(`SELECT idx, step_type, step_format,
			CASE WHEN length(step_payload) <= ? THEN step_payload END
			FROM steps WHERE step_type IN (?, ?) ORDER BY idx`, maxLineBytes, antigravityUserStep, antigravityReplyStep)
		if err != nil {
			return err
		}
		defer rows.Close()
		for rows.Next() {
			var index, kind, format int
			var payload []byte
			if err := rows.Scan(&index, &kind, &format, &payload); err != nil {
				return err
			}
			if index < 0 || index == math.MaxInt || format != 0 || len(payload) == 0 {
				next.badLines++
				continue
			}
			row, valid := antigravityMessage(index, kind, payload)
			if !valid {
				next.badLines++
			} else if row.message.Line != 0 {
				records = append(records, row)
			}
		}
		if err := rows.Err(); err != nil {
			return err
		}
		next.parsed = src.buildSession(records, info.ModTime())
		return nil
	})
	if err != nil {
		return nil, fmt.Errorf("%s: read Antigravity SQLite: %w", src.path, err)
	}
	return next, nil
}

func antigravityMessage(index, kind int, payload antigravityProto) (record, bool) {
	role, variantField, textField := "assistant", uint64(20), uint64(1)
	if kind == antigravityUserStep {
		role, variantField, textField = "user", 19, 2
	}
	variant, _ := payload.field(variantField)
	if variant == nil {
		return record{}, false
	}
	text, _ := variant.field(textField)
	if !utf8.Valid(text) {
		return record{}, false
	}
	if strings.TrimSpace(string(text)) == "" {
		return record{}, true
	}
	metadata, _ := payload.field(5)
	created, _ := metadata.field(1)
	at := created.timestamp()
	return record{kind: "message", at: at, message: Message{
		Role: role, Text: string(text), Timestamp: formatTime(at), Line: index + 1,
	}}, true
}

type antigravityProto []byte

func (data antigravityProto) field(number uint64) (antigravityProto, uint64) {
	for len(data) > 0 {
		key, width := binary.Uvarint(data)
		if width <= 0 || key>>3 == 0 {
			break
		}
		data = data[width:]
		var size, value uint64
		switch key & 7 {
		case 0:
			value, width = binary.Uvarint(data)
			if width <= 0 {
				return nil, 0
			}
			size = uint64(width)
		case 1:
			size = 8
		case 2:
			size, width = binary.Uvarint(data)
			if width <= 0 {
				return nil, 0
			}
			data = data[width:]
		case 5:
			size = 4
		default:
			return nil, 0
		}
		if size > uint64(len(data)) {
			break
		}
		if key>>3 == number {
			switch key & 7 {
			case 0:
				return nil, value
			case 2:
				return data[:size], 0
			}
		}
		data = data[size:]
	}
	return nil, 0
}

func (data antigravityProto) timestamp() time.Time {
	_, seconds := data.field(1)
	_, nanos := data.field(2)
	if seconds == 0 || seconds > 253402300799 || nanos >= 1e9 {
		return time.Time{}
	}
	return time.Unix(int64(seconds), int64(nanos))
}

type antigravitySnapshot struct {
	info, wal os.FileInfo
}

func antigravityDBSnapshot(path string) (antigravitySnapshot, error) {
	info, err := os.Stat(path)
	if err != nil {
		return antigravitySnapshot{}, err
	}
	return antigravitySnapshotWithInfo(path, info)
}

func antigravitySnapshotWithInfo(path string, info os.FileInfo) (antigravitySnapshot, error) {
	if !info.Mode().IsRegular() {
		return antigravitySnapshot{}, fmt.Errorf("%s: expected a regular database file", path)
	}
	wal, err := os.Lstat(path + "-wal")
	switch {
	case errors.Is(err, os.ErrNotExist):
		wal = nil
	case err != nil:
		return antigravitySnapshot{}, err
	case !wal.Mode().IsRegular():
		return antigravitySnapshot{}, fmt.Errorf("%s-wal: expected a regular WAL file", path)
	case wal.Size() == 0:
		wal = nil
	}
	return antigravitySnapshot{info, wal}, nil
}

func (snapshot antigravitySnapshot) same(other antigravitySnapshot) bool {
	return sameSnapshot(snapshot.info, other.info) && (snapshot.wal == nil && other.wal == nil ||
		snapshot.wal != nil && other.wal != nil && sameSnapshot(snapshot.wal, other.wal))
}

func (snapshot antigravitySnapshot) read(path string, query func(*sql.DB) error) error {
	dbPath, options := path, "mode=ro&immutable=1"
	if snapshot.wal != nil || strings.HasPrefix(filepath.ToSlash(path), "//") {
		dir, err := os.MkdirTemp("", "knooo-antigravity-")
		if err != nil {
			return err
		}
		defer os.RemoveAll(dir)
		dbPath, options = filepath.Join(dir, "conversation.db"), "mode=ro"
		if err := copyAntigravityFile(path, dbPath, snapshot.info.Size()); err != nil {
			return err
		}
		if snapshot.wal != nil {
			if err := copyAntigravityFile(path+"-wal", dbPath+"-wal", snapshot.wal.Size()); err != nil {
				return err
			}
		}
	}
	uriPath := filepath.ToSlash(dbPath)
	if !strings.HasPrefix(uriPath, "/") {
		uriPath = "/" + uriPath
	}
	uri := url.URL{Scheme: "file", Path: uriPath, RawQuery: options}
	db, err := sql.Open("sqlite", uri.String())
	if err != nil {
		return err
	}
	defer db.Close()
	if err := query(db); err != nil {
		return err
	}
	current, err := antigravityDBSnapshot(path)
	if err != nil {
		return err
	}
	if !snapshot.same(current) {
		return fmt.Errorf("%s: database changed during scan", path)
	}
	return nil
}

func copyAntigravityFile(path, destination string, size int64) error {
	source, err := os.Open(path)
	if err != nil {
		return err
	}
	defer source.Close()
	target, err := os.OpenFile(destination, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600)
	if err != nil {
		return err
	}
	_, copyErr := io.CopyN(target, source, size)
	return errors.Join(copyErr, target.Close())
}
