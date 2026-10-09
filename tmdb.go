package movie_list

import (
	"log"
	"os"
	"sync"
)
import "github.com/cyruzin/golang-tmdb"

var (
	TmdbClient *tmdb.Client
	once       sync.Once
)

func InitializeClient() {
	apiKey := os.Getenv("TMDB_API_KEY")
	if apiKey == "" {
		log.Fatal("No TMDB_API_KEY environment variable set")
	}
	once.Do(func() {
		client, err := tmdb.Init(apiKey)
		if err != nil {
			log.Fatalf("Error initializing client %s\n", err.Error())
		}
		client.SetClientAutoRetry()
		TmdbClient = client
	})
}
