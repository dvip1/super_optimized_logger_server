package handler

import (
	"bufio"
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
)

// DataDir holds all persistent state (challenge rule 2).
const DataDir = "data"

// Segments are the hot tier: append-only files of fixed-size records,
// srno (8) | ts (8) | sensors (256). A write() lands in the kernel page cache,
// which survives kill -9, so a reading is acknowledged as soon as write()
// returns. Compaction moves the readings into reading_blocks and then deletes
// the segment files.
const segRecSize = 8 + recSize

var nextSeg = 1 // number of the next segment file; only LoadDevices and the writer touch it

type segment struct {
	f    *os.File
	size int64 // bytes of whole records in f
}

func segPath(n int) string { return filepath.Join(DataDir, fmt.Sprintf("seg-%08d", n)) }

// listSegments returns the segment files, oldest first (the fixed-width
// numbers make name order = creation order).
func listSegments() ([]string, error) {
	return filepath.Glob(filepath.Join(DataDir, "seg-*"))
}

func openSegment() (*segment, error) {
	// O_APPEND: every write goes to the current end of the file, also after a Truncate.
	f, err := os.OpenFile(segPath(nextSeg), os.O_CREATE|os.O_EXCL|os.O_WRONLY|os.O_APPEND, 0o600)
	if err != nil {
		return nil, err
	}
	nextSeg++
	return &segment{f: f}, nil
}

// append writes b (whole records) in one write(). On failure it cuts the file
// back, so a partial record can never shift the records written after it.
func (s *segment) append(b []byte) error {
	if _, err := s.f.Write(b); err != nil {
		s.f.Truncate(s.size)
		return err
	}
	s.size += int64(len(b))
	return nil
}

// replaySegments loads every segment record newer than its device's watermark
// back into the device's buffer. Runs at startup, single-threaded, after the
// watermarks are restored from reading_blocks. Within one device, records are
// in timestamp order (the data handler enqueues under the device lock).
func replaySegments() error {
	paths, err := listSegments()
	if err != nil {
		return err
	}
	var rec [segRecSize]byte
	for _, path := range paths {
		if _, err := fmt.Sscanf(filepath.Base(path), "seg-%d", &nextSeg); err != nil {
			return fmt.Errorf("bad segment name %s: %w", path, err)
		}
		nextSeg++
		f, err := os.Open(path)
		if err != nil {
			return err
		}
		r := bufio.NewReaderSize(f, 1<<20)
		for {
			_, err := io.ReadFull(r, rec[:])
			if errors.Is(err, io.EOF) || errors.Is(err, io.ErrUnexpectedEOF) {
				break // end of file; a partial last record was never acknowledged
			}
			if err != nil {
				f.Close()
				return err
			}
			d := devices[[8]byte(rec[:8])]
			ts := int64(binary.LittleEndian.Uint64(rec[8:]))
			if d == nil || ts <= d.wm {
				continue // already in reading_blocks
			}
			d.buf = append(d.buf, rec[8:]...)
			d.lastTs = max(d.lastTs, ts)
		}
		f.Close()
	}
	for _, d := range devices {
		if n := len(d.buf); n > 0 {
			last := d.buf[n-recSize:]
			d.setLatest(recTs(last), last[8:])
		}
	}
	return nil
}
