package handler

import "net/http"

// Routes registers every endpoint and returns the router.
// Patterns use Go's built-in router: "METHOD /path/{param}".
func Routes() *http.ServeMux {
	mux := http.NewServeMux()

	mux.HandleFunc("GET /{$}", Hello)

	// Add your routes here, e.g.:
	// mux.HandleFunc("POST /users", CreateUser)
	// mux.HandleFunc("GET /devices/{srno}", GetDevice)
	// mux.HandleFunc("POST /devices/{srno}/logs", AddDeviceLog)

	return mux
}
