package sessions

import (
	"bytes"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"
)

func TestIncrementalRefresh(t *testing.T) {
	store, home := isolatedStore(t)
	path := filepath.Join(home, ".codex", "sessions", "rollout-test.jsonl")
	src := source{agent: "codex", path: path}
	header := `{"type":"session_meta","payload":{"id":"cache","timestamp":"2026-01-01T10:00:00Z"}}` + "\n"
	first := `{"type":"event_msg","timestamp":"2026-01-01T10:00:01Z","payload":{"type":"user_message","message":"First fixture."}}` + "\n"
	second := `{"type":"event_msg","timestamp":"2026-01-01T10:00:02Z","payload":{"type":"agent_message","message":"Second fixture."}}`
	partial := second[:len(second)-3]
	putFile(t, path, []byte(header+first+"\n"+partial))

	before := scanClean(t, store, nil)[0]
	requireMessages(t, before, []int{2}, "First fixture.")
	cached := store.files[src]
	scanClean(t, store, nil)
	if store.files[src] != cached || cached.line != 3 || cached.offset != int64(len(header+first+"\n")) {
		t.Fatal("unchanged scan reread the file or committed an incomplete record")
	}

	appendFile(t, path, second[len(partial):])
	preview := scanClean(t, store, nil)[0]
	requireMessages(t, preview, []int{2, 4}, "First fixture.", "Second fixture.")
	if got := store.files[src]; got.line != 3 || &got.records[0] != &cached.records[0] {
		t.Fatal("refresh did not reuse the committed prefix")
	}

	appendFile(t, path, "\r\n")
	complete := scanClean(t, store, nil)[0]
	if !reflect.DeepEqual(preview, complete) || store.files[src].line != 4 ||
		before.Messages[0].ID != complete.Messages[0].ID {
		t.Fatal("adding a line terminator changed a message or its anchor")
	}

	// Rewrite the middle of the original prefix, while growing the file.
	rewritten := strings.Replace(first, "First", "Other", 1)
	data := []byte(header + rewritten + "\n" + second + "\r\n\n")
	putFile(t, path, data)
	changed := scanClean(t, store, nil)[0]
	requireMessages(t, changed, []int{2, 4}, "Other fixture.", "Second fixture.")

	// A smaller file may retain the same inode.
	if err := os.Truncate(path, int64(len(header+rewritten))); err != nil {
		t.Fatal(err)
	}
	truncated := scanClean(t, store, nil)[0]
	requireMessages(t, truncated, []int{2}, "Other fixture.")

	// An atomic replacement of equal length and mtime still has a new identity.
	replacement := path + ".tmp"
	putFile(t, replacement, []byte(header+first))
	stamp := store.files[src].info.ModTime()
	if err := os.Chtimes(replacement, stamp, stamp); err != nil {
		t.Fatal(err)
	}
	if err := os.Rename(replacement, path); err != nil {
		t.Fatal(err)
	}
	replaced := scanClean(t, store, nil)[0]
	requireMessages(t, replaced, []int{2}, "First fixture.")
	if replaced.ID != before.ID || replaced.Messages[0].ID != before.Messages[0].ID {
		t.Fatal("replacement changed stable IDs")
	}
	if err := os.Remove(path); err != nil {
		t.Fatal(err)
	}
	if len(scanClean(t, store, nil)) != 0 || len(store.files) != 0 {
		t.Fatal("deleted source remained in the result or cache")
	}
}

func TestRefreshRecoversFromMalformedAndLongLines(t *testing.T) {
	store, home := isolatedStore(t)
	path := filepath.Join(home, ".codex", "sessions", "rollout-fixture.jsonl")
	data := fixture(t, "codex", home)
	data = append([]byte("\xef\xbb\xbf"), data...)
	putFile(t, path, data)
	appendFile(t, path, "{synthetic-malformed-content}\n")
	longText := strings.Repeat("fixture ", 10_000) + "end"
	appendFile(t, path, `{"type":"event_msg","timestamp":"2026-01-01T10:00:03Z","payload":{"type":"agent_message","message":"`+longText+`"}}`+"\n")
	items, warns, _ := store.Scan(nil)
	if len(items) != 1 || len(warns) != 1 || strings.Contains(warns[0], "synthetic-malformed-content") {
		t.Fatalf("got %d sessions and warnings %v", len(items), warns)
	}
	requireMessages(t, items[0], []int{3, 6, 9}, "Inspect the sample project.", "The sample builds.", longText)

	before := store.files[source{agent: "codex", path: path}]
	appendFile(t, path, strings.Repeat(" ", maxLineBytes+1))
	items, warns, _ = store.Scan(nil)
	after := store.files[source{agent: "codex", path: path}]
	if len(items) != 1 || len(warns) != 1 || !strings.Contains(warns[0], "skipped 2") ||
		after.offset != before.offset || after.prefix != before.prefix {
		t.Fatal("oversized partial line changed the committed prefix")
	}
	last := `{"type":"event_msg","timestamp":"2026-01-01T10:00:04Z","payload":{"type":"agent_message","message":"After oversized line."}}`
	appendFile(t, path, "\n"+last+"\n")
	items, warns, _ = store.Scan(nil)
	if len(items) != 1 || len(warns) != 1 || !strings.Contains(warns[0], "skipped 2") {
		t.Fatal("failed to resume after the oversized line")
	}
	requireMessages(t, items[0], []int{3, 6, 9, 11}, "Inspect the sample project.", "The sample builds.", longText, "After oversized line.")
	got, err := os.ReadFile(path)
	if err != nil || !bytes.HasSuffix(got, []byte(last+"\n")) {
		t.Fatal("refresh modified the source file")
	}
}

func TestReadOnlyScanOfTimestampLessSource(t *testing.T) {
	store, home := isolatedStore(t)
	path := filepath.Join(home, "sessions-extra", "fixture.jsonl")
	data := []byte(`{"type":"session","id":"without-clock","cwd":"/synthetic"}`)
	putFile(t, path, data)
	stamp := time.Date(2026, time.January, 2, 3, 4, 5, 0, time.UTC)
	if err := os.Chtimes(path, stamp, stamp); err != nil {
		t.Fatal(err)
	}
	extra := map[string][]string{"pi": {filepath.Dir(path)}}
	items := scanClean(t, store, extra)
	if len(items) != 1 || items[0].Start != stamp.Format(time.RFC3339) ||
		items[0].Updated != items[0].Start || len(items[0].Messages) != 0 {
		t.Fatal("missing log timings did not fall back to the file's mtime")
	}
	if got, err := os.ReadFile(path); err != nil || !bytes.Equal(got, data) {
		t.Fatal("scan modified the source file")
	}
}

func TestDiskCachePersistsAcrossRestartsAndInvalidates(t *testing.T) {
	store, home := isolatedStore(t)
	cacheFile := filepath.Join(t.TempDir(), "sessions-cache.v1.zst")
	store.SetCachePath(cacheFile)

	path := filepath.Join(home, ".codex", "sessions", "rollout-disk.jsonl")
	header := `{"type":"session_meta","payload":{"id":"disk-cache","timestamp":"2026-01-01T10:00:00Z"}}` + "\n"
	first := `{"type":"event_msg","timestamp":"2026-01-01T10:00:01Z","payload":{"type":"user_message","message":"First cached turn."}}` + "\n"
	second := `{"type":"event_msg","timestamp":"2026-01-01T10:00:02Z","payload":{"type":"agent_message","message":"Second updated turn."}}` + "\n"
	putFile(t, path, []byte(header+first))

	initial := scanClean(t, store, nil)
	if len(initial) != 1 {
		t.Fatalf("expected 1 session, got %d", len(initial))
	}
	earlier := time.Unix(1_700_000_000, 0)
	if err := os.Chtimes(cacheFile, earlier, earlier); err != nil {
		t.Fatal(err)
	}
	original := cacheFileInfo(t, cacheFile)
	scanClean(t, store, nil)
	if current := cacheFileInfo(t, cacheFile); !os.SameFile(original, current) || !original.ModTime().Equal(current.ModTime()) {
		t.Fatal("unchanged warm scan rewrote the disk cache")
	}

	// Cold restart into a fresh Store should reuse the persisted disk cache.
	cold := New(home)
	cold.SetCachePath(cacheFile)
	reloaded := scanClean(t, cold, nil)
	if !reflect.DeepEqual(initial, reloaded) {
		t.Fatalf("cold disk-cache reload mismatch:\ngot:  %#v\nwant: %#v", reloaded, initial)
	}
	if current := cacheFileInfo(t, cacheFile); !os.SameFile(original, current) || !original.ModTime().Equal(current.ModTime()) {
		t.Fatal("unchanged cold scan rewrote the disk cache")
	}

	// Appending to the source file must invalidate the cached entry and update the disk cache.
	appendFile(t, path, second)
	updated := scanClean(t, cold, nil)
	if len(updated) != 1 {
		t.Fatalf("expected 1 updated session, got %d", len(updated))
	}
	requireMessages(t, updated[0], []int{2, 3}, "First cached turn.", "Second updated turn.")

	coldAfterUpdate := New(home)
	coldAfterUpdate.SetCachePath(cacheFile)
	reloadedUpdated := scanClean(t, coldAfterUpdate, nil)
	if !reflect.DeepEqual(updated, reloadedUpdated) {
		t.Fatalf("second cold reload did not reflect updated disk cache")
	}

	if err := os.Remove(path); err != nil {
		t.Fatal(err)
	}
	if items := scanClean(t, coldAfterUpdate, nil); len(items) != 0 {
		t.Fatalf("removed session remains in scan: %+v", items)
	}
	reloadedEmpty := New(home)
	reloadedEmpty.SetCachePath(cacheFile)
	reloadedEmpty.ensureDiskCacheLoaded()
	if len(reloadedEmpty.disk) != 0 {
		t.Fatalf("removed session remains in disk cache: %+v", reloadedEmpty.disk)
	}
}

func TestDiskCacheRetriesFailedWrite(t *testing.T) {
	store, home := isolatedStore(t)
	cacheFile := filepath.Join(t.TempDir(), "cache.zst")
	if err := os.Mkdir(cacheFile, 0700); err != nil {
		t.Fatal(err)
	}
	store.SetCachePath(cacheFile)
	putFile(t, filepath.Join(home, ".codex", "sessions", "rollout-write-retry.jsonl"), []byte(
		`{"type":"session_meta","payload":{"id":"write-retry"}}`+"\n"+
			`{"type":"event_msg","payload":{"type":"user_message","message":"First cached turn."}}`+"\n"))
	scanClean(t, store, nil)
	if err := os.Remove(cacheFile); err != nil {
		t.Fatal(err)
	}
	scanClean(t, store, nil)
	if info := cacheFileInfo(t, cacheFile); !info.Mode().IsRegular() {
		t.Fatal("cache write was not retried")
	}
}

func cacheFileInfo(t *testing.T, path string) os.FileInfo {
	t.Helper()
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	return info
}
