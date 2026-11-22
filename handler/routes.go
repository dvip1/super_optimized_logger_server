package handler

import "net/http"

const BasePath = "/api/v1"

func Routes() *http.ServeMux {
	api := http.NewServeMux()

	api.HandleFunc("GET /{$}", Hello)
	api.HandleFunc("POST /auth/signup", Register)
	api.HandleFunc("POST /auth/login", Login)
	api.HandleFunc("GET /logger/health", Health)
	api.HandleFunc("POST /logger/register", RegisterDevice)
	api.HandleFunc("POST /logger/data", IngestData)
	api.HandleFunc("POST /tokens", requireAuth(CreateToken))
	api.HandleFunc("DELETE /tokens/{id}", requireAuth(RevokeToken))

	mux := http.NewServeMux()
	mux.Handle(BasePath+"/", http.StripPrefix(BasePath, api))
	return mux
}
