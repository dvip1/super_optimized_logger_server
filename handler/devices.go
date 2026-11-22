package handler

import (
	"crypto/aes"
	"crypto/cipher"
	"database/sql"
	"encoding/binary"
	"sync"
	"sync/atomic"
)

// device is the in-memory state of one registered logger. The data endpoint
// reads only this, so the per-packet path never queries SQLite.
type device struct {
	mu      sync.Mutex  // guards the four fields below
	aead    cipher.AEAD // AES-128-GCM with the device's current key; nil until registered
	tokenID int64       // token the device registered with (for revocation checks)
	email   string      // owner; only this user may read the device
	lastTs  int64       // newest accepted timestamp (replay protection)

	// Readings not yet compacted. Own lock, separate from mu: the data handler
	// holds mu while it enqueues, so the writer must never wait on mu.
	bufMu sync.Mutex
	buf   []byte // records ts (8) | sensors (256), oldest first, all with ts > wm
	wm    int64  // watermark: readings with ts <= wm are in reading_blocks

	// latest is the newest COMMITTED reading, served by /latest. Atomic so
	// readers never wait on mu (register holds mu during a SQLite write).
	latest atomic.Pointer[reading]
}

// recSize is one buffered reading: ts int64 | 64 x float32, little-endian.
const recSize = 8 + sensorBytes

func recTs(rec []byte) int64 { return int64(binary.LittleEndian.Uint64(rec)) }

// addReading appends one record (ts | sensors). Only the writer calls it.
func (d *device) addReading(rec []byte) {
	d.bufMu.Lock()
	d.buf = append(d.buf, rec...)
	d.bufMu.Unlock()
}

// trim drops the first n records once they are committed in a block ending at end.
func (d *device) trim(n int, end int64) {
	d.bufMu.Lock()
	k := copy(d.buf, d.buf[n*recSize:]) // shift in place, keeps the allocation
	d.buf = d.buf[:k]
	d.wm = end
	d.bufMu.Unlock()
}

// reading is one stored reading: timestamp + 64 raw little-endian float32.
type reading struct {
	ts      int64
	sensors [sensorBytes]byte
}

// setLatest records the reading as the device's newest if it is newer than the
// current one (packets of one device can commit out of order across batches).
// Only the writer goroutine calls it (and LoadDevices before the writer starts),
// so load-compare-store needs no CAS.
func (d *device) setLatest(ts int64, sensors []byte) {
	if cur := d.latest.Load(); cur != nil && cur.ts >= ts {
		return
	}
	r := &reading{ts: ts}
	copy(r.sensors[:], sensors)
	d.latest.Store(r)
}

var (
	cacheMu sync.RWMutex            // guards both maps (not the devices inside)
	devices = map[[8]byte]*device{} // srno -> state
	revoked = map[int64]struct{}{}  // ids of revoked registration tokens
)

// newAEAD builds the AES-128-GCM cipher for a 16-byte key.
func newAEAD(key []byte) cipher.AEAD {
	block, _ := aes.NewCipher(key)  // only fails for a bad key size; keys are always 16 bytes
	aead, _ := cipher.NewGCM(block) // only fails for a non-128-bit block cipher; AES is one
	return aead
}

// lookupDevice returns the cached device, or nil if the srno was never registered.
func lookupDevice(srno [8]byte) *device {
	cacheMu.RLock()
	defer cacheMu.RUnlock()
	return devices[srno]
}

// getOrAddDevice returns the cached device, creating an empty entry if needed.
func getOrAddDevice(srno [8]byte) *device {
	cacheMu.Lock()
	defer cacheMu.Unlock()
	d := devices[srno]
	if d == nil {
		d = &device{}
		devices[srno] = d
	}
	return d
}

func isRevoked(tokenID int64) bool {
	cacheMu.RLock()
	defer cacheMu.RUnlock()
	_, ok := revoked[tokenID]
	return ok
}

func markRevoked(tokenID int64) {
	cacheMu.Lock()
	defer cacheMu.Unlock()
	revoked[tokenID] = struct{}{}
}

// LoadDevices fills the cache from SQLite. Call once at startup, before serving.
// Runs single-threaded, so it touches the maps and devices without locks.
// Every query is one pass over a table or index (no per-device subqueries).
func LoadDevices() error {
	err := eachRow(`SELECT id FROM token_device WHERE revoked = 1`, func(rows *sql.Rows) error {
		var id int64
		err := rows.Scan(&id)
		revoked[id] = struct{}{}
		return err
	})
	if err != nil {
		return err
	}

	err = eachRow(`SELECT srno, encryption_key, token_id, email FROM devices`, func(rows *sql.Rows) error {
		var srno, key []byte
		d := &device{}
		if err := rows.Scan(&srno, &key, &d.tokenID, &d.email); err != nil {
			return err
		}
		d.aead = newAEAD(key)
		devices[[8]byte(srno)] = d
		return nil
	})
	if err != nil {
		return err
	}

	// Watermark = end of the newest block; replay then restores everything
	// newer from the segments. lastTs follows, so a replay of a pre-crash
	// packet is still rejected after a restart. Uses only the (srno, end_ts) index.
	err = eachRow(`SELECT srno, MAX(end_ts) FROM reading_blocks GROUP BY srno`, func(rows *sql.Rows) error {
		var srno []byte
		var ts int64
		if err := rows.Scan(&srno, &ts); err != nil {
			return err
		}
		if d := devices[[8]byte(srno)]; d != nil {
			d.wm, d.lastTs = ts, ts
		}
		return nil
	})
	if err != nil {
		return err
	}
	return replaySegments()
}
