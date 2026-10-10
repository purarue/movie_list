package main

import (
	"flag"
	"fmt"
	"log"
	"os"

	"github.com/purarue/movie_list"
)

func main() {
	port := flag.Int("port", 9006, "port to host server on")
	favicon := flag.String("favicon", os.Getenv("MOVIE_LIST_FAVICON"), "favicon/icon for application [env: MOVIE_LIST_FAVICON]")
	flag.Usage = func() {
		fmt.Fprintln(os.Stderr, "usage: ./movie_list [FLAG...]\n\nMust set TMDB_API_KEY for search access")
		fmt.Fprintln(os.Stderr, "")
		flag.PrintDefaults()
	}
	flag.Parse()
	movie_list.InitializeClient()
	err := movie_list.Server(*port, *favicon)
	if err != nil {
		log.Fatalf("Error: %s\n", err)
	}
}
