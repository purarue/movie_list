package main

import (
	"github.com/purarue/movie_list"
)

func main() {
	movie_list.InitializeClient()
	movie_list.Server(9005)
}
