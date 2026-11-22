package handler

import (
	"errors"
	"net/http"
	"os"
	"strings"
	"time"

	"github.com/golang-jwt/jwt/v5"
)

const tokenTTL = 7 * 24 * time.Hour // spec requires >= 24h

var jwtKey []byte

// InitAuth loads the JWT signing key from path, creating it on first start.
// Persisting it keeps issued tokens valid across restarts.
func InitAuth(path string) error {
	key, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		key = randomBytes(32)
		err = os.WriteFile(path, key, 0o600)
	}
	jwtKey = key
	return err
}

// issueToken returns a signed JWT whose subject is the user's email.
func issueToken(email string) (string, error) {
	return jwt.NewWithClaims(jwt.SigningMethodHS256, jwt.RegisteredClaims{
		Subject:   email,
		ExpiresAt: jwt.NewNumericDate(time.Now().Add(tokenTTL)),
	}).SignedString(jwtKey)
}

// authHandler is a handler that receives the authenticated user's email.
type authHandler func(w http.ResponseWriter, r *http.Request, email string)

// requireAuth rejects requests without a valid "Authorization: Bearer <jwt>".
func requireAuth(next authHandler) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		raw := strings.TrimPrefix(r.Header.Get("Authorization"), "Bearer ")
		var claims jwt.RegisteredClaims
		_, err := jwt.ParseWithClaims(raw, &claims,
			func(*jwt.Token) (any, error) { return jwtKey, nil },
			jwt.WithValidMethods([]string{jwt.SigningMethodHS256.Alg()}),
			jwt.WithExpirationRequired(),
		)
		if err != nil || claims.Subject == "" {
			writeError(w, http.StatusUnauthorized, "invalid or missing access token")
			return
		}
		next(w, r, claims.Subject)
	}
}
