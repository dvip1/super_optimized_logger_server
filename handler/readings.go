package handler

import (
	"bytes"
	"database/sql"
	"encoding/binary"
	"errors"
	"math"
	"net/http"
	"strconv"

	"dvip1/logger_server/database"
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

// Latest serves the device's newest reading: from memory, or after a restart
// (before the device sends again) from its newest block.
func Latest(w http.ResponseWriter, r *http.Request, email string) {
	srno, d := ownedDevice(w, r, email)
	if d == nil {
		return
	}
	if latest := d.latest.Load(); latest != nil {
		writeRawJSON(w, appendReading(nil, latest.ts, latest.sensors[:]))
		return
	}
	var data []byte
	err := database.DB.QueryRow(
		`SELECT data FROM reading_blocks WHERE srno = ? ORDER BY end_ts DESC LIMIT 1`, srno,
	).Scan(&data)
	if errors.Is(err, sql.ErrNoRows) {
		writeError(w, http.StatusNotFound, "no readings yet")
		return
	}
	if err != nil {
		writeError(w, http.StatusInternalServerError, "database error")
		return
	}
	recs := decodeBlock(data)
	last := recs[len(recs)-recSize:]
	writeRawJSON(w, appendReading(nil, recTs(last), last[8:]))
}

// Readings serves readings with from <= timestamp <= to, oldest first, at most limit.
// Older readings come from reading_blocks, newer ones from the device's buffer;
// the watermark splits them, so none is returned twice or missed while a
// compaction moves readings from the buffer into a block.
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
	if errFrom != nil || errTo != nil || errLimit != nil || limit <= 0 {
		writeError(w, http.StatusBadRequest, "from and to (Unix seconds) required, limit must be positive")
		return
	}
	limit = min(limit, maxLimit)

	// Snapshot buffer + watermark together. A block committed after this
	// snapshot only holds readings > wm, which are in the snapshot already.
	d.bufMu.Lock()
	wm, recent := d.wm, bytes.Clone(d.buf)
	d.bufMu.Unlock()

	body := []byte(`{"readings":[`)
	n := int64(0)
	// add appends the records (ts | sensors) in [from, to] until limit is reached.
	add := func(recs []byte) {
		for i := 0; i < len(recs) && n < limit; i += recSize {
			if ts := recTs(recs[i:]); ts >= from && ts <= to {
				if n > 0 {
					body = append(body, ',')
				}
				body = appendReading(body, ts, recs[i+8:i+recSize])
				n++
			}
		}
	}
	if from <= wm {
		err := eachRow(
			`SELECT data FROM reading_blocks WHERE srno = ? AND end_ts >= ? AND start_ts <= ? ORDER BY end_ts`,
			func(rows *sql.Rows) error {
				var data []byte
				if err := rows.Scan(&data); err != nil {
					return err
				}
				add(decodeBlock(data))
				return nil
			},
			srno, from, min(to, wm), // srno as []byte: the column is a BLOB
		)
		if err != nil {
			writeError(w, http.StatusInternalServerError, "database error")
			return
		}
	}
	add(recent)
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
