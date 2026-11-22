# Logger Platform Architecture

## 1. Environment & Constraints
- **Host:** Single AWS Lightsail instance (2 vCPU, 2 GB RAM, 60 GB SSD, Ubuntu 24.04).
- **State:** All persistent state is in `./data`:
  - `logger.db`: SQLite (users, tokens, devices, compacted reading blocks)
  - `seg-NNNNNNNN`: append-only segment files holding the newest readings
  - `jwt.key`: JWT signing secret
- **Durability Model:** `kill -9` safe through the Linux page cache. A reading is acknowledged only after its `write()` to a segment file returns. The kernel keeps that data even if the process dies. SQLite runs with `synchronous = OFF` in WAL mode for the same reason. Power loss is out of scope.

---

## 2. SQLite Configuration (PRAGMAs)
Applied on every database connection:
```sql
PRAGMA journal_mode = WAL;
PRAGMA synchronous = OFF;
PRAGMA foreign_keys = ON;
```

---

## 3. Storage Layout

**Hot tier: segment files** (`handler/segments.go`)
- Fixed 272-byte records: `srno (8) | ts (8) | sensors (256)`, appended in arrival order.
- No index and no B-tree. Writes are sequential.
- A segment is deleted once all of its readings are in `reading_blocks`.

**Cold tier: `reading_blocks` in SQLite**
- One row per device per epoch (about 60 readings): `srno, start_ts, end_ts, n, data`.
- Indexed on `(srno, end_ts)`.
- Rows are only inserted, never updated or deleted.

The rest of the schema is in `database/seed.go`.

---

## 4. In-Memory State
SQL queries are kept off the per-packet ingest path.

- **Device Cache:** `map[[8]byte]*device`
  - Under `mu`:
    - `cipher.AEAD` (pre-initialized AES-128-GCM cipher)
    - `tokenID` and `email` (owner)
    - `lastTs` (replay protection)
  - Under `bufMu`:
    - `buf`: readings not yet compacted, as `ts | sensors` records, oldest first
    - `wm`: the watermark. Readings with `ts <= wm` are in `reading_blocks`; newer ones are in `buf`.
  - `latest`: an atomic pointer to the newest stored reading, for `/latest`.
- **Revoked Tokens Set:** `map[int64]struct{}`, updated whenever `DELETE /api/v1/tokens/{id}` writes to SQLite.

---

## 5. Core Data Flows

### Device Registration (`POST /api/v1/logger/register`)
1. Validate the token (exists and not revoked).
2. Generate a random 16-byte AES-128 key.
3. Upsert the device in `devices`. This updates `email`, `token_id` and `encryption_key`. An ownership transfer keeps the history, which is keyed by `srno`.
4. Write to SQLite and update the in-memory cache while holding the device lock, so the two never disagree.
5. Return the base64-encoded key.

### Sensor Ingest (`POST /api/v1/logger/data`)
1. Validate the packet length (301 bytes) and version (`1`).
2. Look up `srno` in the Device Cache.
3. Under the device lock:
   1. Check that the device's token is not revoked.
   2. Decrypt in place (AAD = bytes `0..8`).
   3. Reject replays (`ts <= lastTs`).
   4. Set `lastTs`.
   5. Enqueue the reading. Enqueueing under the lock keeps each device's readings in timestamp order.
4. **Group write (commit-when-ready):**
   1. The writer goroutine waits for one queued reading, then takes everything else already queued.
   2. It appends the whole batch to the current segment with one `write()`.
   3. It appends each reading to its device's `buf` and `latest`.
   4. Only then does it release the waiting handlers, which reply `204`.

   There is no timer. Under load the batches grow by themselves.

### Compaction (segments → `reading_blocks`)
1. **Rotate** every 60 s, or after 5 s with no new readings, so the last partial epoch is compacted soon after a run ends.
   - Rotation runs on the writer goroutine.
   - It opens a new segment and records `n`, the number of buffered readings per device. All of them are in the older segments.
2. **Write blocks** in the background, in transactions of 500 devices. Each block holds the first `n` readings of a device's `buf`.
3. **After each commit,** remove those readings from `buf` and set `wm = end_ts`.
4. **Delete** every segment older than the new one, then run `PRAGMA wal_checkpoint(TRUNCATE)` so the WAL doesn't inflate `./data`.
5. **Encoding:** blocks are currently stored uncompressed (`encodeBlock` / `decodeBlock` in `handler/compact.go`). The planned codec, per sensor column:
   1. Find the smallest decimal scale that reproduces every value bit-exactly.
   2. Store zigzag deltas, bit-packed.
   3. Store the raw 32 bits for columns that don't fit (NaN, -0, …).

Compacting all devices together once per epoch is what removes the need for an index on the hot tier: readings never have to be deleted per device. A crash at any step is safe. Committed blocks are skipped on replay (`ts <= wm`), and uncommitted readings are still in the segments.

### Read Queries (`/latest`, `/readings`)
- Verify device ownership: the device's `email` must match the JWT's subject. Unknown devices and other users' devices both return `404`.
- **`/latest`:** comes from the in-memory `latest`. After a restart, before the device sends again, it falls back to the newest block.
- **`/readings`:**
  1. Copy `buf` and `wm` together under `bufMu`.
  2. Read blocks with `start_ts <= min(to, wm)`, then append the matching buffered readings.

  The watermark separates the two sources, so a compaction running during the query neither duplicates nor drops readings.

### Startup & Crash Recovery
1. Open SQLite in `./data` and apply the PRAGMAs.
2. Load `devices` and the revoked tokens into memory.
3. Set each device's `wm` and `lastTs` to `MAX(end_ts)` of its blocks. This query is served by the `(srno, end_ts)` index alone.
4. Replay all segment files in order:
   - Skip records with `ts <= wm`.
   - Stop at a partial last record, which was never acknowledged.
   - Rebuild `buf`, `lastTs` and `latest` from what remains.
5. Start the writer, then the HTTP server. `/logger/health` cannot answer before the replay has finished. The first compaction afterwards removes the old segments.

---

## 6. Why Segment Files Instead of a SQLite Hot Table
The first version inserted every reading into an SQLite `ingest_log` table, with an index on `(srno, ts)` for compaction and recovery. Each 20 ms commit touched about one index page per device in the batch, so SQLite wrote about 15× more pages than the data needed. strace measured 2.2 `pwrite64` per reading.

Comparison: simulated Lightsail (cgroup with 2 cores, 2 GB and 3000 IOPS), 15,000 devices, 5 minutes, crash test included.

| | SQLite `ingest_log` | Segment files |
|---|---|---|
| Verdict | FAIL (39% of readings accepted) | PASS |
| Ingest p99 | 4.9 s | 221 ms |
| Query p99 | 4.7 s | 32 ms |
| Restart until healthy | 51 s | 0.25 s |
| Disk write operations | 2,021,032 | 27,841 |
| Storage | 641 B/reading | 275 B/reading (blocks not yet compressed) |
