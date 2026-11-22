package handler

import (
	"database/sql"
	"encoding/base64"
	"errors"
	"net/http"

	"dvip1/logger_server/database"
)

type registerDeviceRequest struct {
	Srno       string  `json:"srno"`  // 16 hex chars -> 8 bytes
	Token      string  `json:"token"` // 32 hex chars -> 16 bytes
	DeviceName string  `json:"device_name"`
	Latitude   float64 `json:"latitude"`  // decimal degrees, [-90, 90]
	Longitude  float64 `json:"longitude"` // decimal degrees, [-180, 180]
}

func RegisterDevice(w http.ResponseWriter, r *http.Request) {
	var req registerDeviceRequest
	if err := readJSON(w, r, &req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid JSON body")
		return
	}
	srno, okSrno := decodeHex(req.Srno, 8)
	token, okToken := decodeHex(req.Token, 16)
	if !okSrno || !okToken {
		writeError(w, http.StatusBadRequest, "invalid srno or token")
		return
	}

	var tokenID int64
	var email string
	err := database.DB.QueryRow(
		`SELECT id, email FROM token_device WHERE token = ? AND revoked = 0`, token,
	).Scan(&tokenID, &email)
	if errors.Is(err, sql.ErrNoRows) {
		writeError(w, http.StatusForbidden, "unknown or revoked token")
		return
	}
	if err != nil {
		writeError(w, http.StatusInternalServerError, "database error")
		return
	}

	// Hold the device lock across the DB write and the cache update, so the
	// key in SQLite and the key in memory can never disagree, and no packet is
	// checked against a half-updated device.
	d := getOrAddDevice([8]byte(srno))
	d.mu.Lock()
	defer d.mu.Unlock()

	key := randomBytes(16)
	_, err = database.DB.Exec(
		`INSERT INTO devices (srno, device_name, longitude, latitude, email, token_id, encryption_key)
		VALUES (?, ?, ?, ?, ?, ?, ?)
		ON CONFLICT(srno) DO UPDATE SET
			device_name    = excluded.device_name,
			longitude      = excluded.longitude,
			latitude       = excluded.latitude,
			email          = excluded.email,
			token_id       = excluded.token_id,
			encryption_key = excluded.encryption_key`,
		srno, req.DeviceName, req.Longitude, req.Latitude, email, tokenID, key,
	)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "database error")
		return
	}
	d.aead, d.tokenID = newAEAD(key), tokenID // the old key stops working here

	writeJSON(w, http.StatusOK, map[string]string{"key": base64.StdEncoding.EncodeToString(key)})
}
