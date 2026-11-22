package handler

import (
	"database/sql"
	"encoding/binary"
	"math"
	"net/http"
	"strconv"
)

const maxLimit = 1000 // largest `limit` the spec allows for /readings

// ownedDevice returns the srno and device named in the path if the caller owns
// it. Otherwise it writes 404 and returns nil: unknown and someone else's look
// the same, so nobody can probe which serial numbers exist.
func ownedDevice(w http.ResponseWriter, r *http.Request, email string) ([]byte, *device) {
	srno, ok := decodeHex(r.PathValue("srno"), 8)
	if ok {
		if d := lookupDevice([8]byte(srno)); d != nil {
			d.mu.Lock()
			owner := d.email
			d.mu.Unlock()
			if owner == email {
				return srno, d
			}
		}
	}
	writeError(w, http.StatusNotFound, "device not found")
	return nil, nil
}

// Latest serves the device's newest committed reading from memory.
func Latest(w http.ResponseWriter, r *http.Request, email string) {
	_, d := ownedDevice(w, r, email)
	if d == nil {
		return
	}
	latest := d.latest.Load()
	if latest == nil {
		writeError(w, http.StatusNotFound, "no readings yet")
		return
	}
	writeRawJSON(w, appendReading(nil, latest.ts, latest.sensors[:]))
}

// Readings serves readings with from <= timestamp <= to, oldest first, at most limit.
// TODO(compaction): read reading_blocks + the in-memory buffer instead of ingest_log.
func Readings(w http.ResponseWriter, r *http.Request, email string) {
	srno, d := ownedDevice(w, r, email)
	if d == nil {
		return
	}
	q := r.URL.Query()
	from, errFrom := strconv.ParseInt(q.Get("from"), 10, 64)
	to, errTo := strconv.ParseInt(q.Get("to"), 10, 64)
	limit, errLimit := int64(maxLimit), error(nil)
	if s := q.Get("limit"); s != "" {
		limit, errLimit = strconv.ParseInt(s, 10, 64)
	}
	// limit <= 0 must be rejected: SQLite treats a negative LIMIT as "no limit".
	if errFrom != nil || errTo != nil || errLimit != nil || limit <= 0 {
		writeError(w, http.StatusBadRequest, "from and to (Unix seconds) required, limit must be positive")
		return
	}
	limit = min(limit, maxLimit)

	body := []byte(`{"readings":[`)
	first := true
	err := eachRow(
		`SELECT ts, sensors FROM ingest_log WHERE srno = ? AND ts BETWEEN ? AND ? ORDER BY ts LIMIT ?`,
		func(rows *sql.Rows) error {
			var ts int64
			var sensors []byte
			if err := rows.Scan(&ts, &sensors); err != nil {
				return err
			}
			if !first {
				body = append(body, ',')
			}
			first = false
			body = appendReading(body, ts, sensors)
			return nil
		},
		srno, from, to, limit, // srno as []byte: the column is a BLOB; a string would never match
	)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "database error")
		return
	}
	writeRawJSON(w, append(body, "]}"...))
}

// appendReading appends {"timestamp":ts,"sensors":[...]} to b, decoding the 256
// raw bytes as 64 little-endian float32. Each value is written in its shortest
// form that parses back to the exact same float32. NaN/Inf have no JSON form
// and are written as null (encoding/json would fail mid-response instead).
func appendReading(b []byte, ts int64, sensors []byte) []byte {
	b = append(b, `{"timestamp":`...)
	b = strconv.AppendInt(b, ts, 10)
	b = append(b, `,"sensors":[`...)
	for i := 0; i < len(sensors); i += 4 {
		if i > 0 {
			b = append(b, ',')
		}
		f := float64(math.Float32frombits(binary.LittleEndian.Uint32(sensors[i:])))
		if math.IsNaN(f) || math.IsInf(f, 0) {
			b = append(b, "null"...)
		} else {
			b = strconv.AppendFloat(b, f, 'g', -1, 32)
		}
	}
	return append(b, "]}"...)
}
