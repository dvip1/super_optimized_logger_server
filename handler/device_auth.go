package handler

import (
	"database/sql"
	"errors"
	"net/http"

	"dvip1/logger_server/database"

	"golang.org/x/crypto/bcrypt"
)

type register_request struct {
	Srno string `json:"srno"`
	DeviceName string `json:"device_name"`
	Lat float32 `json:"longitude"`
	Long float32 `json:"latitude"`
	Token []byte `json:"token"`
}

type register_response struct {
	Key string `json:"key"`
}

func RegisterDevice(w http.ResponseWriter, r *http.Request){
	var req register_request
	
	if err := readJSON(w, r, &req); err!=nil {
		writeError(w, http.StatusBadRequest, "Invalid request body")
		return
	}

	if req.Srno== "" || req.DeviceName==""  || len(req.Token)!=16 {
		writeError(w, http.StatusBadRequest, "Required fields missing or invalid")
	}
	
	
}