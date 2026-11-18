package handler

import (
	"database/sql"
	"errors"
	"net/http"

	"dvip1/logger_server/database"

	"golang.org/x/crypto/bcrypt"
)

type authRequest struct {
	Email    string `json:"email"`
	Password string `json:"password"`
}

// Register creates a user with a bcrypt-hashed password.
func Register(w http.ResponseWriter, r *http.Request) {
	var req authRequest
	if err := readJSON(w, r, &req); err != nil || req.Email == "" || req.Password == "" {
		writeError(w, http.StatusBadRequest, "email and password required")
		return
	}

	hash, err := bcrypt.GenerateFromPassword([]byte(req.Password), bcrypt.DefaultCost)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "could not hash password")
		return
	}

	_, err = database.DB.Exec("INSERT INTO users (email, password) VALUES (?, ?)", req.Email, string(hash))
	if err != nil {
		writeError(w, http.StatusConflict, "user already exists")
		return
	}
	writeJSON(w, http.StatusCreated, map[string]string{"email": req.Email})
}

// Login checks email and password against the users table.
func Login(w http.ResponseWriter, r *http.Request) {
	var req authRequest
	if err := readJSON(w, r, &req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid JSON body")
		return
	}

	var hash string
	err := database.DB.QueryRow("SELECT password FROM users WHERE email = ?", req.Email).Scan(&hash)
	if errors.Is(err, sql.ErrNoRows) || (err == nil && bcrypt.CompareHashAndPassword([]byte(hash), []byte(req.Password)) != nil) {
		writeError(w, http.StatusUnauthorized, "invalid email or password")
		return
	}
	if err != nil {
		writeError(w, http.StatusInternalServerError, "database error")
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"email": req.Email})
}
