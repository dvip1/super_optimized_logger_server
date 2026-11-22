package handler

import (
	"time"

	"dvip1/logger_server/database"
)

// flushInterval is how often queued readings are committed. Shorter = lower
// ingest latency; longer = bigger, cheaper transactions.
const flushInterval = 20 * time.Millisecond

// pendingRow is one decrypted reading waiting for the next commit.
type pendingRow struct {
	dev     *device    // whose `latest` to advance after the commit
	srno    []byte     // 8 bytes
	ts      int64      // Unix seconds
	sensors []byte     // 256 bytes: 64 little-endian float32
	done    chan error // receives the commit result; buffered so the writer never blocks
}

// writeQueue carries readings from the data handlers to the writer goroutine.
// When full, handlers block on send, which slows ingest instead of losing data.
var writeQueue = make(chan pendingRow, 1<<16)

// StartWriter starts the group-commit loop: one transaction per flushInterval
// for every reading queued since the last one.
func StartWriter() {
	go func() {
		var batch []pendingRow
		for range time.Tick(flushInterval) {
			batch = drainQueue(batch[:0])
			if len(batch) == 0 {
				continue
			}
			err := insertBatch(batch)
			for _, row := range batch {
				if err == nil {
					// Before replying: once the device sees 2xx, /latest
					// must already return this reading.
					row.dev.setLatest(row.ts, row.sensors)
				}
				row.done <- err // the handler answers 2xx only if err == nil
			}
		}
	}()
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

// insertBatch writes all rows in one transaction: all of them land, or none do.
func insertBatch(batch []pendingRow) error {
	tx, err := database.DB.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback() // no-op after a successful Commit

	stmt, err := tx.Prepare(`INSERT INTO ingest_log (srno, ts, sensors) VALUES (?, ?, ?)`)
	if err != nil {
		return err
	}
	for _, row := range batch {
		if _, err := stmt.Exec(row.srno, row.ts, row.sensors); err != nil {
			return err
		}
	}
	return tx.Commit()
}
