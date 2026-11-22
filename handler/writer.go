package handler

import (
	"encoding/binary"
	"log"
	"time"
)

const (
	epoch   = 60 * time.Second // rotate + compact at least this often under load
	idleGap = 5 * time.Second  // ...or once ingest has been quiet this long
)

// pendingRow is one decrypted reading waiting to be written.
type pendingRow struct {
	dev     *device    // whose buffer and `latest` to update after the write
	srno    []byte     // 8 bytes
	ts      int64      // Unix seconds
	sensors []byte     // 256 bytes: 64 little-endian float32
	done    chan error // receives the write result; buffered so the writer never blocks
}

// writeQueue carries readings from the data handlers to the writer goroutine.
// When full, handlers block on send, which slows ingest instead of losing data.
var writeQueue = make(chan pendingRow, 1<<16)

// StartWriter opens a fresh segment and starts the writer goroutine.
func StartWriter() error {
	seg, err := openSegment()
	if err != nil {
		return err
	}
	go writeLoop(seg)
	return nil
}

// writeLoop is the only goroutine that appends to segments and device buffers.
// Commit-when-ready: it waits for one reading, takes everything else already
// queued, writes them with ONE write() and answers them all. Under load the
// batches grow by themselves; there is no timer to wait for.
func writeLoop(seg *segment) {
	var batch []pendingRow
	var out []byte
	dirty := true // compact once after start too: picks up replayed readings, removes old segments
	segStart, lastWrite := time.Now(), time.Now()
	tick := time.NewTicker(time.Second)

	for {
		select {
		case row := <-writeQueue:
			batch = drainQueue(append(batch[:0], row))
			out = out[:0]
			for _, r := range batch { // segment record: srno | ts | sensors
				out = append(out, r.srno...)
				out = binary.LittleEndian.AppendUint64(out, uint64(r.ts))
				out = append(out, r.sensors...)
			}
			err := seg.append(out)
			for i, r := range batch {
				if err == nil {
					// Before replying: once the device sees 2xx, /latest and
					// /readings must already return this reading.
					r.dev.addReading(out[i*segRecSize+8 : (i+1)*segRecSize])
					r.dev.setLatest(r.ts, r.sensors)
				}
				r.done <- err // the handler answers 2xx only if err == nil
			}
			dirty, lastWrite = true, time.Now()

		case now := <-tick.C:
			due := now.Sub(segStart) >= epoch || now.Sub(lastWrite) >= idleGap
			if dirty && due && !compacting.Load() {
				next, err := rotate(seg)
				if err != nil {
					log.Println("rotate:", err) // keep writing to the old segment, retry next tick
					continue
				}
				seg, segStart, dirty = next, now, false
			}
		}
	}
}

// drainQueue appends everything currently in writeQueue to batch, without waiting.
func drainQueue(batch []pendingRow) []pendingRow {
	for {
		select {
		case row := <-writeQueue:
			batch = append(batch, row)
		default:
			return batch
		}
	}
}
