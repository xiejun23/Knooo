package sessions

import (
	"encoding/gob"
	"io"
	"os"
	"path/filepath"
	"time"

	"github.com/klauspost/compress/zstd"
)

const (
	legacySessionCacheVersion = 2
	sessionCacheVersion       = 3
)

type diskCachePayload struct {
	Version    int
	Entries    []diskCacheEntry // Version 2 stored the entire cache as one gob value.
	EntryCount int
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
	decoder := gob.NewDecoder(dec)
	var payload diskCachePayload
	if err := decoder.Decode(&payload); err != nil {
		return
	}
	entries := make(map[source]diskCacheEntry, len(payload.Entries))
	switch payload.Version {
	case legacySessionCacheVersion:
		for _, entry := range payload.Entries {
			entries[source{agent: entry.Agent, path: entry.Path}] = entry
		}
	case sessionCacheVersion:
		if payload.EntryCount < 0 || len(payload.Entries) != 0 {
			return
		}
		for range payload.EntryCount {
			var entry diskCacheEntry
			if err := decoder.Decode(&entry); err != nil {
				return
			}
			entries[source{agent: entry.Agent, path: entry.Path}] = entry
		}
		if len(entries) != payload.EntryCount {
			return
		}
	default:
		return
	}
	if decoder.Decode(&struct{}{}) != io.EOF {
		return
	}
	s.disk = entries
	s.diskSize = len(entries)
	s.diskDirty = s.diskDirty || payload.Version == legacySessionCacheVersion
}

func (s *Store) saveDiskCache(files map[source]*cachedFile) bool {
	dir := filepath.Dir(s.cachePath)
	if err := os.MkdirAll(dir, 0700); err != nil {
		return false
	}
	tmp, err := os.CreateTemp(dir, ".sessions-cache-*.tmp")
	if err != nil {
		return false
	}
	tmpPath := tmp.Name()
	enc, err := zstd.NewWriter(tmp, zstd.WithEncoderLevel(zstd.SpeedFastest), zstd.WithEncoderConcurrency(1))
	if err != nil {
		tmp.Close()
		_ = os.Remove(tmpPath)
		return false
	}
	encodeErr := encodeDiskCache(gob.NewEncoder(enc), files)
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

func encodeDiskCache(encoder *gob.Encoder, files map[source]*cachedFile) error {
	// Write one gob value per source to bound temporary buffers by a single transcript.
	if err := encoder.Encode(diskCachePayload{Version: sessionCacheVersion, EntryCount: len(files)}); err != nil {
		return err
	}
	for src, file := range files {
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
		if err := encoder.Encode(&entry); err != nil {
			return err
		}
	}
	return nil
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
