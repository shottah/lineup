package main

import (
	"log"

	"github.com/shottah/lineup/api/internal/httpserver"
)

func main() {
	srv := httpserver.New(httpserver.Deps{})
	log.Fatal(srv.ListenAndServe())
}
