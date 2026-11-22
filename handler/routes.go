package handler

import "net/http"

const BasePath = "/api/v1"

func Routes() *http.ServeMux {
	api := http.NewServeMux()

	api.HandleFunc("GET /{$}", Hello)
	api.HandleFunc("POST /register", Register)
	api.HandleFunc("POST /login", Login)
	api.HandleFunc("POST /logger/register", RegisterDevice)

	// api.HandleFunc("POST /devices/{srno}/logs", AddDeviceLog)

	mux := http.NewServeMux()
	mux.Handle(BasePath+"/", http.StripPrefix(BasePath, api))
	return mux
}
