package sessions

import (
	"bufio"
	"bytes"
	"crypto/sha256"
	"encoding"
	"errors"
	"fmt"
	"hash"
	"io"
	"os"
	"strings"

	"github.com/klauspost/compress/zstd"
)

const maxLineBytes = 32 << 20

type prefixHash interface {
	hash.Hash
	encoding.BinaryMarshaler
	encoding.BinaryUnmarshaler
}

type cachedFile struct {
	info     os.FileInfo
	wal      os.FileInfo
	offset   int64
	line     int
	prefix   [sha256.Size]byte
	records  []record // Parsed records through the last newline.
	badRows  int
	badLines int
	parsed   candidate

	messageRevision string
}

func (src source) refresh(old *cachedFile) (*cachedFile, error) {
	info, err := os.Stat(src.path)
	if err != nil {
		return nil, err
	}
	if !info.Mode().IsRegular() {
		return nil, fmt.Errorf("%s: expected a regular file", src.path)
	}
	if src.agent == "antigravity" {
		return src.refreshAntigravity(old, info)
	}
	if old != nil && sameSnapshot(old.info, info) {
		return old, nil
	}

	file, err := os.Open(src.path)
	if err != nil {
		return nil, err
	}
	defer file.Close()
	if info, err = file.Stat(); err != nil {
		return nil, err
	}
	if !info.Mode().IsRegular() {
		return nil, fmt.Errorf("%s: expected a regular file", src.path)
	}

	next := &cachedFile{info: info}
	if src.agent == "gemini" && strings.HasSuffix(strings.ToLower(src.path), ".json") {
		rows, err := geminiLegacy(file)
		if err != nil {
			return nil, fmt.Errorf("%s: read Gemini JSON: %w", src.path, err)
		}
		current, err := os.Stat(src.path)
		if err != nil || !sameSnapshot(info, current) {
			return nil, fmt.Errorf("%s: file changed during scan", src.path)
		}
		next.parsed = src.buildSession(geminiMessages(rows), info.ModTime())
		return next, nil
	}
	sum := sha256.New().(prefixHash)
	compressed := src.compressed()
	if !compressed && old != nil && old.offset > 0 && old.offset <= info.Size() && os.SameFile(old.info, info) {
		// A changed file may have been rewritten in place, even if it grew.
		// Verify the committed prefix before reusing its parsed records.
		if _, err := io.CopyN(sum, file, old.offset); err != nil {
			return nil, fmt.Errorf("%s: verify indexed prefix: %w", src.path, err)
		}
		if bytes.Equal(sum.Sum(nil), old.prefix[:]) {
			next.offset, next.line, next.records, next.badRows = old.offset, old.line, old.records, old.badRows
		} else {
			if _, err := file.Seek(0, io.SeekStart); err != nil {
				return nil, err
			}
			sum.Reset()
		}
	}

	remaining := &io.LimitedReader{R: file, N: info.Size() - next.offset}
	var content io.Reader = remaining
	if compressed {
		decoder, err := zstd.NewReader(remaining, zstd.WithDecoderConcurrency(1), zstd.WithDecoderMaxMemory(64<<20))
		if err != nil {
			return nil, fmt.Errorf("%s: decompress rollout: %w", src.path, err)
		}
		defer decoder.Close()
		content = decoder
	}
	reader := bufio.NewReaderSize(content, 64<<10)
	var tail record
	var badTail bool
	for {
		line, size, complete, err := readJSONLLine(reader, sum)
		if err != nil {
			return nil, fmt.Errorf("%s: read JSONL: %w", src.path, err)
		}
		if size == 0 {
			break
		}
		valid := size <= maxLineBytes
		var row record
		if valid {
			row, valid = parseRecord(src.agent, line, next.line+1)
		}
		if !complete {
			// A writer may still be completing this record. Preview valid JSON,
			// but always reread it if the file subsequently changes.
			tail, badTail = row, size > maxLineBytes
			break
		}
		next.offset += size
		next.line++
		if !valid {
			next.badRows++
		} else if row.kind != "" {
			next.records = append(next.records, row)
		}
	}
	current, err := os.Stat(src.path)
	if err != nil {
		return nil, err
	}
	if remaining.N != 0 || !sameSnapshot(info, current) {
		return nil, fmt.Errorf("%s: file changed during scan", src.path)
	}
	if next.offset > 0 {
		sum.Sum(next.prefix[:0])
	}
	next.badLines = next.badRows
	if badTail {
		next.badLines++
	}
	rows := next.records
	if tail.kind != "" {
		rows = append(rows, tail)
	}
	if src.agent == "gemini" {
		rows = geminiMessages(rows)
	}
	next.parsed = src.buildSession(rows, info.ModTime())
	return next, nil
}

func sameSnapshot(a, b os.FileInfo) bool {
	return a.Size() == b.Size() && a.ModTime().Equal(b.ModTime()) && os.SameFile(a, b)
}

// readJSONLLine hashes complete lines, including oversized lines, and leaves
// an unterminated suffix outside the committed prefix.
func readJSONLLine(r *bufio.Reader, sum prefixHash) (line []byte, size int64, complete bool, err error) {
	var beforeLargeLine []byte
	for {
		part, readErr := r.ReadSlice('\n')
		size += int64(len(part))
		switch {
		case size <= maxLineBytes:
			if len(line) == 0 && readErr != bufio.ErrBufferFull {
				line = part
			} else {
				line = append(line, part...)
			}
		default:
			if beforeLargeLine == nil {
				beforeLargeLine, err = sum.MarshalBinary()
				if err != nil {
					return nil, 0, false, err
				}
				sum.Write(line)
				line = nil
			}
			sum.Write(part)
		}
		switch {
		case readErr == nil:
			sum.Write(line)
			return line, size, true, nil
		case errors.Is(readErr, io.EOF):
			if beforeLargeLine != nil {
				err = sum.UnmarshalBinary(beforeLargeLine)
			}
			return line, size, false, err
		case readErr != bufio.ErrBufferFull:
			return nil, 0, false, readErr
		}
	}
}
