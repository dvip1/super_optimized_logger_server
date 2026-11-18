package main

import (
	"fmt"
	"log"
	"net/http"

	"dvip1/logger_server/database"
	"dvip1/logger_server/handler"
)

func main() {
	if err := database.Init("logger.db"); err != nil {
		log.Fatal("Error initializing database:", err)
	}
	defer database.Close()
	if err := database.Seed(); err != nil {
		log.Fatal("Error seeding database:", err)
	}

	fmt.Println("Server listening on :3002")
	if err := http.ListenAndServe(":3002", handler.Routes()); err != nil {
		fmt.Println("Error starting server:", err)
	}
}
