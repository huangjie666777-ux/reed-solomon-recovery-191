package main

import (
	"log"
	"net/http"
	"os"

	"reed-solomon-recovery/internal/httpapi"
)

func main() {
	address := os.Getenv("ADDR")
	if address == "" {
		address = ":8080"
	}
	log.Printf("listening on %s", address)
	if err := http.ListenAndServe(address, httpapi.Router()); err != nil {
		log.Fatal(err)
	}
}
