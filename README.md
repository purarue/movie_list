up here as reference

saves stuff into a JSON file with a mutex to avoid race conditions/corruption

using htmx and go fun :)

wrote not a lot of javascript and can have fun dynamic things on the page with synthetic events

## Usage

Compile with `go build ./cmd/movie_list/`

Then, run: `./movie_list`

## Config

If `MOVIE_LIST_FAVICON` is set, uses that URL as the favicon/icon in the top left.

Requires `TMDB_API_KEY` to be set, in order to search (from <https://www.themoviedb.org/>)
