package main

import (
	"log"

	"github.com/purarue/movie_list"
)

func main() {
	movie_list.InitializeClient()
	err := movie_list.Server(9006)
	if err != nil {
		log.Fatalf("Error: %s\n", err)
	}
}
