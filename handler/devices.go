package handler

import (
	"crypto/aes"
	"crypto/cipher"
	"sync"

	"dvip1/logger_server/database"
)

// device is the in-memory state of one registered logger. The data endpoint
// reads only this, so the per-packet path never queries SQLite.
type device struct {
	mu      sync.Mutex  // guards the three fields below
	aead    cipher.AEAD // AES-128-GCM with the device's current key; nil until registered
	tokenID int64       // token the device registered with (for revocation checks)
	lastTs  int64       // newest accepted timestamp (replay protection)
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
func LoadDevices() error {
	rows, err := database.DB.Query(`SELECT id FROM token_device WHERE revoked = 1`)
	if err != nil {
		return err
	}
	for rows.Next() {
		var id int64
		if err := rows.Scan(&id); err != nil {
			return err
		}
		markRevoked(id)
	}
	if err := rows.Err(); err != nil {
		return err
	}

	// lastTs = newest reading we hold for the device, in either storage tier,
	// so a replay of a pre-crash packet is still caught after a restart.
	rows, err = database.DB.Query(`
		SELECT d.srno, d.encryption_key, d.token_id, MAX(
			COALESCE((SELECT MAX(ts) FROM ingest_log WHERE srno = d.srno), 0),
			COALESCE((SELECT end_ts FROM reading_blocks WHERE srno = d.srno
			          ORDER BY start_ts DESC LIMIT 1), 0))
		FROM devices d`)
	if err != nil {
		return err
	}
	defer rows.Close()
	for rows.Next() {
		var srno, key []byte
		d := &device{}
		if err := rows.Scan(&srno, &key, &d.tokenID, &d.lastTs); err != nil {
			return err
		}
		d.aead = newAEAD(key)
		devices[[8]byte(srno)] = d // single-threaded at startup, no lock needed
	}
	return rows.Err()
}
