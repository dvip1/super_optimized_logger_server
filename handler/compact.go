package handler

import (
	"log"
	"os"
	"sync/atomic"

	"dvip1/logger_server/database"
)

const chunkDevices = 500 // devices per block transaction: bounds the size of one commit

// compacting is true while a compaction runs; the writer won't rotate again until it ends.
var compacting atomic.Bool

// pendingBlock is one device's readings to compact: the first n records of its buffer.
type pendingBlock struct {
	srno [8]byte
	d    *device
	n    int
}

// rotate switches the writer to a new segment and starts compacting everything
// buffered so far. It runs on the writer goroutine, the only one that appends,
// so every buffered reading is in the old segments at this moment: once those
// readings are in reading_blocks, the old segments can be deleted.
func rotate(old *segment) (*segment, error) {
	seg, err := openSegment()
	if err != nil {
		return nil, err
	}
	old.f.Close()

	var job []pendingBlock
	cacheMu.RLock()
	for srno, d := range devices {
		d.bufMu.Lock()
		if n := len(d.buf) / recSize; n > 0 {
			job = append(job, pendingBlock{srno, d, n})
		}
		d.bufMu.Unlock()
	}
	cacheMu.RUnlock()

	keep := segPath(nextSeg - 1) // the segment just opened
	compacting.Store(true)
	go func() {
		defer compacting.Store(false)
		if err := compact(job, keep); err != nil {
			log.Println("compact:", err) // segments stay; the next rotation retries
		}
	}()
	return seg, nil
}

// compact writes one block per device, chunk by chunk, then deletes every
// segment older than keep and shrinks the WAL back to zero.
// Crash-safe at every step: a committed block's readings are skipped on
// replay (ts <= watermark), uncommitted ones are replayed from the segments.
func compact(job []pendingBlock, keep string) error {
	for len(job) > 0 {
		chunk := job[:min(chunkDevices, len(job))]
		job = job[len(chunk):]
		if err := compactChunk(chunk); err != nil {
			return err
		}
	}
	paths, err := listSegments()
	if err != nil {
		return err
	}
	for _, p := range paths {
		if p < keep {
			if err := os.Remove(p); err != nil {
				return err
			}
		}
	}
	_, err = database.DB.Exec(`PRAGMA wal_checkpoint(TRUNCATE)`)
	return err
}

func compactChunk(chunk []pendingBlock) error {
	tx, err := database.DB.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback() // no-op after a successful Commit

	stmt, err := tx.Prepare(`INSERT INTO reading_blocks (srno, start_ts, end_ts, n, data) VALUES (?, ?, ?, ?, ?)`)
	if err != nil {
		return err
	}
	ends := make([]int64, len(chunk))
	for i, b := range chunk {
		// Safe to use after unlocking: the writer only appends past this
		// prefix, and only this goroutine removes it (in trim, below).
		b.d.bufMu.Lock()
		recs := b.d.buf[:b.n*recSize]
		b.d.bufMu.Unlock()

		start, end := recTs(recs), recTs(recs[len(recs)-recSize:])
		if _, err := stmt.Exec(b.srno[:], start, end, b.n, encodeBlock(recs)); err != nil {
			return err
		}
		ends[i] = end
	}
	if err := tx.Commit(); err != nil {
		return err
	}
	for i, b := range chunk {
		b.d.trim(b.n, ends[i])
	}
	return nil
}

// encodeBlock / decodeBlock convert a device's records (ts | sensors, oldest
// first) to the stored form and back. For now the stored form is the records
// themselves; the compression codec replaces only these two functions.
func encodeBlock(recs []byte) []byte { return recs }
func decodeBlock(data []byte) []byte { return data }
