package main

import (
	"fmt"
	"log"
	"net/http"
	"os"

	"dvip1/logger_server/database"
	"dvip1/logger_server/handler"
)

const addr = ":8080"

func main() {
	// All persistent state lives in ./data (challenge rule 2).
	if err := os.MkdirAll("data", 0o700); err != nil {
		log.Fatal("Error creating data dir:", err)
	}
	if err := handler.InitAuth("data/jwt.key"); err != nil {
		log.Fatal("Error loading auth key:", err)
	}
	if err := database.Init("data/logger.db"); err != nil {
		log.Fatal("Error initializing database:", err)
	}
	defer database.Close()
	if err := database.Seed(); err != nil {
		log.Fatal("Error seeding database:", err)
	}
	if err := handler.LoadDevices(); err != nil {
		log.Fatal("Error loading devices:", err)
	}
	handler.StartWriter()

	fmt.Println("Server listening on", addr)
	if err := http.ListenAndServe(addr, handler.Routes()); err != nil {
		fmt.Println("Error starting server:", err)
	}
}
