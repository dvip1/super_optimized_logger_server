package handler

import (
	"encoding/hex"
	"net/http"
	"strconv"

	"dvip1/logger_server/database"
)

type createTokenRequest struct {
	Name string `json:"name"`
}

// CreateToken issues a registration token (32 hex chars) owned by the caller.
func CreateToken(w http.ResponseWriter, r *http.Request, email string) {
	var req createTokenRequest
	if err := readJSON(w, r, &req); err != nil || req.Name == "" {
		writeError(w, http.StatusBadRequest, "name required")
		return
	}

	token := randomBytes(16)
	res, err := database.DB.Exec(
		`INSERT INTO token_device (email, token, name) VALUES (?, ?, ?)`, email, token, req.Name,
	)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "database error")
		return
	}
	id, _ := res.LastInsertId()
	writeJSON(w, http.StatusCreated, map[string]string{
		"id":    strconv.FormatInt(id, 10),
		"token": hex.EncodeToString(token),
	})
}

// RevokeToken revokes one of the caller's tokens. Other users' tokens get 404.
func RevokeToken(w http.ResponseWriter, r *http.Request, email string) {
	res, err := database.DB.Exec(
		`UPDATE token_device SET revoked = 1 WHERE id = ? AND email = ?`, r.PathValue("id"), email,
	)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "database error")
		return
	}
	if n, _ := res.RowsAffected(); n == 0 {
		writeError(w, http.StatusNotFound, "token not found")
		return
	}
	// TODO: add the id to the in-memory revoked-token set once ingest exists.
	w.WriteHeader(http.StatusNoContent)
}
