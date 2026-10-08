package main

import (
	"log"
	"os"

	"github.com/purarue/movie_list"
)

func main() {
	movie_list.InitializeClient()
	favicon := os.Getenv("MOVIE_LIST_FAVICON")
	if favicon == "" {
		favicon = "/favicon.ico"
	}
	err := movie_list.Server(9006, favicon)
	if err != nil {
		log.Fatalf("Error: %s\n", err)
	}
}
