package main

import (
	"fmt"
	"log"
	"net/http"

	"dvip1/logger_server/database"
)

func helloHandler(w http.ResponseWriter, r *http.Request) {
	fmt.Fprintf(w, "Hello, world!")
}

func main() {
	if err := database.Init("logger.db"); err != nil {
		log.Fatal("Error initializing database:", err)
	}
	defer database.Close()

	http.HandleFunc("/", helloHandler)
	fmt.Println("Server listening on :3002")
	if err := http.ListenAndServe(":3002", nil); err != nil {
		fmt.Println("Error starting server:", err)
	}
}
