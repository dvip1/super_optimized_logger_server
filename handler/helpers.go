package handler

import (
	"crypto/rand"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"net/http"

	"dvip1/logger_server/database"
)

// decodeHex decodes s and reports whether it was valid hex of exactly n bytes.
func decodeHex(s string, n int) ([]byte, bool) {
	b, err := hex.DecodeString(s)
	return b, err == nil && len(b) == n
}

// eachRow runs query and calls fn for every row. Rows are always closed, even
// on an early return, so no read transaction is left pinning the WAL.
func eachRow(query string, fn func(*sql.Rows) error, args ...any) error {
	rows, err := database.DB.Query(query, args...)
	if err != nil {
		return err
	}
	defer rows.Close()
	for rows.Next() {
		if err := fn(rows); err != nil {
			return err
		}
	}
	return rows.Err()
}

// writeRawJSON sends an already-encoded JSON body with status 200.
func writeRawJSON(w http.ResponseWriter, body []byte) {
	w.Header().Set("Content-Type", "application/json")
	w.Write(body)
}

// randomBytes returns n cryptographically random bytes (keys, tokens).
func randomBytes(n int) []byte {
	b := make([]byte, n)
	rand.Read(b) // never returns an error since Go 1.24
	return b
}

// readJSON decodes the request body into v. Rejects unknown fields and bodies over 1MB.
func readJSON(w http.ResponseWriter, r *http.Request, v any) error {
	r.Body = http.MaxBytesReader(w, r.Body, 1<<20)
	dec := json.NewDecoder(r.Body)
	dec.DisallowUnknownFields()
	return dec.Decode(v)
}

// writeJSON sends v as JSON with the given status code.
func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	json.NewEncoder(w).Encode(v)
}

// writeError sends {"error": msg} with the given status code.
func writeError(w http.ResponseWriter, status int, msg string) {
	writeJSON(w, status, map[string]string{"error": msg})
}
