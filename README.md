# Logger Platform Architecture

## 1. Environment & Constraints
- **Host:** Single AWS Lightsail instance (2 vCPU, 2 GB RAM, 60 GB SSD, Ubuntu 24.04).
- **State:** All persistent state stored in `./data` (SQLite database + auth signing secret if using JWT).
- **Durability Model:** `kill -9` safe via Linux kernel page cache (`synchronous = OFF` in WAL mode) paired with group commit.

---

## 2. SQLite Configuration (PRAGMAs)
Applied on every database connection:
```sql
PRAGMA journal_mode = WAL;
PRAGMA synchronous = OFF;
PRAGMA foreign_keys = ON;
```

---

## 3. Database Schema

checkout /database/seed.go

---

## 4. In-Memory State (Ingest Hot Path)
SQL queries are kept off the per-packet ingest path using in-memory structures synced with SQLite:

- **Device Cache:** `map[[8]byte]*DeviceState`
  - `cipher.AEAD` (pre-initialized AES-128-GCM cipher)
  - `token_id`, `email` (owner)
  - `last_ts` (for replay protection)
  - `buffer` (slice of up to ~60 recent plaintext readings)
- **Revoked Tokens Set:** `map[int64]struct{}`
  - Updated synchronously in memory whenever `DELETE /api/v1/tokens/{id}` writes to SQLite.

---

## 5. Core Data Flows

### Device Registration (`POST /api/v1/logger/register`)
1. Validate token (exists and not revoked).
2. Generate random 16-byte AES-128 key.
3. Upsert device in `devices` (updates `email`, `token_id`, and `encryption_key`; ownership transfer preserves history via `srno`).
4. **Commit transaction before responding.**
5. Update in-memory Device Cache with the new cipher and metadata.
6. Return base64-encoded key.

### Sensor Ingest (`POST /api/v1/logger/data`)
1. Validate packet length (301 bytes) and version (`1`).
2. Look up `srno` in the in-memory Device Cache and verify `token_id` is not in the Revoked Tokens Set.
3. Decrypt payload using cached `cipher.AEAD` (AAD = bytes `0..8`).
4. Check `ts <= last_ts` in memory; reject/drop replays immediately.
5. Update `last_ts`, append plaintext reading to the device's in-memory buffer, and enqueue the row for the Tier 1 writer.
6. **Group Commit:** Flush queued rows into `ingest_log` in a single transaction every **20–100 ms**. Hold HTTP `2xx` responses until their batch commits.

### Compaction (Tier 1 → Tier 2)
1. Triggered when a device's in-memory buffer reaches ~60 readings, or when a device has sent nothing for ~10 s (flushes partial buffers so no uncompressed rows linger in `ingest_log`).
   - Runs on the group-commit goroutine, and only blocks readings whose `ingest_log` batch has already committed. Otherwise a later batch could insert rows already inside a block, duplicating them after a crash replay.
2. Encode block:
   - Transpose 60 × 64 `float32` values into sensor-major order.
   - Apply Gorilla-style XOR encoding per sensor.
   - Delta-encode timestamps.
   - Compress with `zstd`.
3. In a single transaction: insert block into `reading_blocks` and delete that device's flushed rows from `ingest_log`.
4. Clear flushed readings from the device's in-memory buffer (keep the newest reading for `/latest`).
5. When ingest goes quiet, run `PRAGMA wal_checkpoint(TRUNCATE)` so the WAL file doesn't inflate `./data`.

### Read Queries (`/latest`, `/readings`)
- Verify device ownership (`devices.email` matches authenticated user).
- **`/latest`:** Serve directly from the device's in-memory buffer (fallback to latest `reading_blocks` row if empty).
- **`/readings`:** Query matching ranges from `reading_blocks`, decompress, and merge with the device's in-memory buffer to guarantee `< 5s` visibility.

### Startup & Crash Recovery
1. Open SQLite in `./data` and apply PRAGMAs.
2. Load `devices` and revoked `token_device` rows into memory.
3. Replay all remaining rows from `ingest_log` into per-device in-memory buffers.
4. Restore each device's `last_ts` as the max of its newest `ingest_log.ts` and its newest `reading_blocks.end_ts` (a device whose buffer was just compacted has no `ingest_log` rows left).
5. Start HTTP server and group-commit loop.