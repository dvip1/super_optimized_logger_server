package handler

import (
	"crypto/aes"
	"crypto/cipher"
	"database/sql"
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

	// latest is the newest COMMITTED reading, served by /latest. Atomic so
	// readers never wait on mu (register holds mu during a SQLite write).
	latest atomic.Pointer[reading]
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
// Every query is one pass over its table (no per-device subqueries), so restart
// time stays flat with 20k devices even without the ingest_log index.
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

	// lastTs = newest reading in either storage tier, so a replay of a
	// pre-crash packet is still rejected after a restart.
	// TODO(compaction): also restore `latest` from the newest block (or keep
	// each device's newest row in ingest_log when compacting).
	err = eachRow(`SELECT srno, MAX(end_ts) FROM reading_blocks GROUP BY srno`, func(rows *sql.Rows) error {
		var srno []byte
		var ts int64
		if err := rows.Scan(&srno, &ts); err != nil {
			return err
		}
		if d := devices[[8]byte(srno)]; d != nil {
			d.lastTs = max(d.lastTs, ts)
		}
		return nil
	})
	if err != nil {
		return err
	}

	// With MAX(), SQLite takes the other columns (sensors) from the max row,
	// so this restores lastTs and the latest reading in one pass.
	return eachRow(`SELECT srno, MAX(ts), sensors FROM ingest_log GROUP BY srno`, func(rows *sql.Rows) error {
		var srno, sensors []byte
		var ts int64
		if err := rows.Scan(&srno, &ts, &sensors); err != nil {
			return err
		}
		if d := devices[[8]byte(srno)]; d != nil {
			d.lastTs = max(d.lastTs, ts)
			d.setLatest(ts, sensors)
		}
		return nil
	})
}
