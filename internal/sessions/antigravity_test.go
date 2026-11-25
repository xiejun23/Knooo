package sessions

import (
	"database/sql"
	"encoding/binary"
	"os"
	"path/filepath"
	"testing"
)

func TestScanAntigravitySQLite(t *testing.T) {
	for distribution, variant := range map[string]string{"antigravity-cli": "cli", "antigravity": "app"} {
		t.Run(distribution, func(t *testing.T) {
			store, home := isolatedStore(t)
			appData := filepath.Join(home, ".gemini", distribution)
			directory := filepath.Join(appData, "conversations")
			if err := os.MkdirAll(directory, 0700); err != nil {
				t.Fatal(err)
			}
			id := "b16c9983-4474-49bd-9da5-66e6d75c4bb1"
			path := filepath.Join(directory, id+".db")
			transcript := openTestAntigravityDB(t, path, `
				PRAGMA journal_mode=WAL;
				PRAGMA wal_autocheckpoint=0;
				CREATE TABLE trajectory_meta (cascade_id TEXT);
				CREATE TABLE trajectory_metadata_blob (id TEXT, data BLOB);
				CREATE TABLE steps (idx INTEGER PRIMARY KEY, step_type INTEGER, step_format INTEGER, step_payload BLOB);`)
			if _, err := transcript.Exec(`INSERT INTO trajectory_meta VALUES (?)`, id); err != nil {
				t.Fatal(err)
			}
			index := openTestAntigravityDB(t, filepath.Join(appData, "conversation_summaries.db"), `
				PRAGMA journal_mode=WAL;
				PRAGMA wal_autocheckpoint=0;
				CREATE TABLE conversation_summaries
					(conversation_id TEXT, title TEXT, workspace_uris TEXT, last_modified_time TEXT, parent_conversation_id TEXT);`)
			if _, err := index.Exec(`INSERT INTO conversation_summaries VALUES (?, ?, '["file:///workspace/client%20app"]', ?, '')`,
				id, "Native Antigravity title", "2026-11-23 13:49:01.112624151+00:00"); err != nil {
				t.Fatal(err)
			}

			stamp := antigravityTestBytes(5, antigravityTestBytes(1, binary.AppendUvarint([]byte{8}, 1795433400)))
			insert := func(position, kind int, payload []byte) {
				t.Helper()
				full := append(append([]byte{}, stamp...), payload...)
				if _, err := transcript.Exec(`INSERT INTO steps VALUES (?, ?, 0, ?)`, position, kind, full); err != nil {
					t.Fatal(err)
				}
			}
			insert(0, 14, antigravityTestBytes(19, antigravityTestBytes(2, []byte("Find the regression"))))
			insert(1, 15, antigravityTestBytes(20, antigravityTestBytes(3, []byte("Private reasoning"))))
			insert(2, 132, antigravityTestBytes(140, []byte("Tool output")))
			list, warnings, coverage := store.Scan(nil)
			if len(list) != 1 || len(warnings) != 0 {
				t.Fatalf("initial scan returned %d sessions and warnings %v", len(list), warnings)
			}
			if session := list[0]; session.Agent != "antigravity" || session.ID != "antigravity:"+id ||
				session.AgentVariant != variant || !session.MatchesAgent("antigravity") ||
				!session.MatchesAgent("antigravity:"+variant) || session.MatchesAgent("antigravity:other") ||
				session.Title != "Native Antigravity title" || session.Path != path || session.Cwd != "/workspace/client app" ||
				len(session.Messages) != 1 || session.Messages[0].Text != "Find the regression" ||
				session.Messages[0].Role != "user" || session.Messages[0].Timestamp == "" {
				t.Fatalf("unexpected Antigravity session: %+v", session)
			}
			for _, root := range coverage.Roots {
				if root.Directory == directory && root.AgentVariant != variant {
					t.Fatalf("unexpected variant for source directory: %+v", root)
				}
			}
			firstMessageID := list[0].Messages[0].ID
			insert(3, 15, antigravityTestBytes(20, antigravityTestBytes(1, []byte("The regression is fixed."))))
			list, warnings, _ = store.Scan(nil)
			if len(list) != 1 || len(warnings) != 0 || len(list[0].Messages) != 2 {
				t.Fatalf("refresh returned %+v, warnings %v", list, warnings)
			}
			if message := list[0].Messages[1]; message.Role != "assistant" || message.Line != 4 ||
				message.Text != "The regression is fixed." || list[0].Messages[0].ID != firstMessageID {
				t.Fatalf("unexpected messages after WAL update: %+v", list[0].Messages)
			}
		})
	}
}

func antigravityTestBytes(field uint64, value []byte) []byte {
	tag := binary.AppendUvarint(nil, field<<3|2)
	return append(binary.AppendUvarint(tag, uint64(len(value))), value...)
}

func openTestAntigravityDB(t *testing.T, path, schema string) *sql.DB {
	t.Helper()
	db, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	if _, err := db.Exec(schema); err != nil {
		t.Fatal(err)
	}
	return db
}
