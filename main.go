package main

import (
	"fmt"
	"log"
	"net/http"
	"os"
	"path/filepath"

	"dvip1/logger_server/database"
	"dvip1/logger_server/handler"
)

const addr = ":8080"

func main() {
	if err := os.MkdirAll(handler.DataDir, 0o700); err != nil {
		log.Fatal("Error creating data dir:", err)
	}
	if err := handler.InitAuth(filepath.Join(handler.DataDir, "jwt.key")); err != nil {
		log.Fatal("Error loading auth key:", err)
	}
	if err := database.Init(filepath.Join(handler.DataDir, "logger.db")); err != nil {
		log.Fatal("Error initializing database:", err)
	}
	defer database.Close()
	if err := database.Seed(); err != nil {
		log.Fatal("Error seeding database:", err)
	}
	// Replays the segment files: listen only after this, so /logger/health
	// can't report ready before every acknowledged reading is back.
	if err := handler.LoadDevices(); err != nil {
		log.Fatal("Error loading devices:", err)
	}
	if err := handler.StartWriter(); err != nil {
		log.Fatal("Error starting writer:", err)
	}

	fmt.Println("Server listening on", addr)
	if err := http.ListenAndServe(addr, handler.Routes()); err != nil {
		fmt.Println("Error starting server:", err)
	}
}
