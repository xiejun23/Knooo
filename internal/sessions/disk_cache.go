package sessions

import (
	"encoding/gob"
	"os"
	"path/filepath"
	"time"

	"github.com/klauspost/compress/zstd"
)

const sessionCacheVersion = 2

type diskCachePayload struct {
	Version int
	Entries []diskCacheEntry
}

type diskCacheEntry struct {
	Agent        string
	Path         string
	Size         int64
	MtimeUnix    int64
	HasWAL       bool
	WALSize      int64
	WALMtimeUnix int64
	BadLines     int
	Session      Session
	Updated      time.Time
	ParentRef    string
}

// SetCachePath configures a persistent disk cache file used to speed up cold restarts.
func (s *Store) SetCachePath(path string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.cachePath = path
	s.diskLoaded = false
	s.disk = nil
	s.diskSize = 0
	s.diskDirty = len(s.files) > 0
}

func (s *Store) ensureDiskCacheLoaded() {
	if s.diskLoaded || s.cachePath == "" {
		return
	}
	s.diskLoaded = true
	file, err := os.Open(s.cachePath)
	if err != nil {
		return
	}
	defer file.Close()
	dec, err := zstd.NewReader(file, zstd.WithDecoderConcurrency(1), zstd.WithDecoderMaxMemory(64<<20))
	if err != nil {
		return
	}
	defer dec.Close()
	var payload diskCachePayload
	if err := gob.NewDecoder(dec).Decode(&payload); err != nil || payload.Version != sessionCacheVersion {
		return
	}
	entries := make(map[source]diskCacheEntry, len(payload.Entries))
	for _, entry := range payload.Entries {
		entries[source{agent: entry.Agent, path: entry.Path}] = entry
	}
	s.disk = entries
	s.diskSize = len(entries)
}

func (s *Store) saveDiskCache(files map[source]*cachedFile) bool {
	entries := make([]diskCacheEntry, 0, len(files))
	for src, file := range files {
		if file == nil || file.info == nil {
			continue
		}
		entry := diskCacheEntry{
			Agent:     src.agent,
			Path:      src.path,
			Size:      file.info.Size(),
			MtimeUnix: file.info.ModTime().UnixNano(),
			BadLines:  file.badLines,
			Session:   file.parsed.session,
			Updated:   file.parsed.updated,
			ParentRef: file.parsed.parentRef,
		}
		if file.wal != nil {
			entry.HasWAL = true
			entry.WALSize = file.wal.Size()
			entry.WALMtimeUnix = file.wal.ModTime().UnixNano()
		}
		entries = append(entries, entry)
	}
	dir := filepath.Dir(s.cachePath)
	if err := os.MkdirAll(dir, 0700); err != nil {
		return false
	}
	tmp, err := os.CreateTemp(dir, ".sessions-cache-*.tmp")
	if err != nil {
		return false
	}
	tmpPath := tmp.Name()
	enc, err := zstd.NewWriter(tmp, zstd.WithEncoderLevel(zstd.SpeedFastest))
	if err != nil {
		tmp.Close()
		_ = os.Remove(tmpPath)
		return false
	}
	encodeErr := gob.NewEncoder(enc).Encode(diskCachePayload{Version: sessionCacheVersion, Entries: entries})
	closeEncErr := enc.Close()
	closeFileErr := tmp.Close()
	if encodeErr != nil || closeEncErr != nil || closeFileErr != nil {
		_ = os.Remove(tmpPath)
		return false
	}
	if err := os.Rename(tmpPath, s.cachePath); err != nil {
		_ = os.Remove(tmpPath)
		return false
	}
	return true
}

func (src source) refreshWithDisk(old *cachedFile, disk *diskCacheEntry) (*cachedFile, bool, error) {
	if old == nil && disk != nil {
		if info, err := os.Stat(src.path); err == nil && info.Mode().IsRegular() &&
			info.Size() == disk.Size && info.ModTime().UnixNano() == disk.MtimeUnix {
			var walInfo os.FileInfo
			matched := true
			if src.agent == "antigravity" {
				wi, werr := os.Stat(src.path + "-wal")
				switch {
				case werr == nil:
					matched = disk.HasWAL && wi.Size() == disk.WALSize && wi.ModTime().UnixNano() == disk.WALMtimeUnix
					walInfo = wi
				case os.IsNotExist(werr):
					matched = !disk.HasWAL
				default:
					matched = false
				}
			}
			if matched {
				return &cachedFile{
					info:     info,
					wal:      walInfo,
					badLines: disk.BadLines,
					parsed: candidate{
						session:   disk.Session,
						updated:   disk.Updated,
						parentRef: disk.ParentRef,
					},
				}, false, nil
			}
		}
	}
	file, err := src.refresh(old)
	changed := err == nil && file != nil && file != old
	return file, changed, err
}
