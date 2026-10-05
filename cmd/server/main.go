// Command server runs the offline Reed-Solomon erasure-recovery HTTP service.
package main

import (
	"log"
	"net/http"
	"os"

	"reed-solomon-recovery/internal/httpapi"
)

func main() {
	addr := os.Getenv("ADDR")
	if addr == "" {
		addr = ":8080"
	}
	srv := &http.Server{Addr: addr, Handler: httpapi.NewRouter()}
	log.Printf("reed-solomon-recovery listening on %s", addr)
	if err := srv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
		log.Fatal(err)
	}
}
