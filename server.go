package movie_list

import (
	"bytes"
	"embed"
	"encoding/json"
	"errors"
	"fmt"
	"html/template"
	"io"
	"net/http"
	"os"
	"sort"
	"strconv"
	"sync"
	"time"

	tmdb "github.com/cyruzin/golang-tmdb"
)

//go:embed templates/*.html
var templateFS embed.FS
var tmpl *template.Template

func init() {
	tmpl = template.Must(template.ParseFS(templateFS, "templates/*.html"))
}

type searchResult struct {
	Index     int
	Image     string
	MediaType string
	Name      string
	ID        uint64
	URL       string
	Data      []string
}

type Item struct {
	Name    string
	Status  string
	Image   string
	URL     string
	Watched bool
	Added   uint64
}

func coerceStatus(val string) string {
	switch val {
	case "watching":
		return "watching"
	case "completed":
		return "completed"
	}
	return "plan_to_watch"
}

func statusToOrder(val string) int {
	switch val {
	case "watching":
		return 1
	case "completed":
		return 3
	case "plan_to_watch":
		return 2
	}
	return 99
}

func validateItem(item *Item) (*Item, error) {
	newStatus := coerceStatus(item.Status)
	if newStatus == item.Status {
		return item, nil
	}
	// TODO: proxy images? maybe not worth it for this amount of usage
	return &Item{
		Name:   item.Name,
		Status: newStatus,
		Image:  item.Image,
		URL:    item.URL,
		Added:  item.Added,
	}, nil
}

func loadItems(file string) ([]Item, error) {
	var items []Item
	if _, err := os.Stat(file); os.IsNotExist(err) {
		return items, nil
	}
	fp, err := os.Open(file)
	if err != nil {
		return nil, err
	}
	bytesData, err := io.ReadAll(fp)
	if err != nil {
		return nil, err
	}
	err = json.Unmarshal(bytesData, &items)
	if err != nil {
		return nil, err
	}
	var validated []Item
	for _, it := range items {
		vit, err := validateItem(&it)
		if err != nil {
			return nil, err
		}
		validated = append(validated, *vit)
	}
	sort.Slice(validated, func(i, j int) bool {
		left := statusToOrder(validated[i].Status)
		right := statusToOrder(validated[j].Status)
		// if they're the same, reverse order so things added recently show at the top
		if left == right {
			return validated[i].Added > validated[j].Added
		}
		// otherwise order watching before plan_to_watch
		return left < right
	})
	return validated, nil
}

func CopyFile(srcpath, dstpath string) (err error) {
	r, err := os.Open(srcpath)
	// skip if the source path doesn't exist yet
	if os.IsNotExist(err) {
		return nil
	} else if err != nil {
		return err
	}
	defer r.Close() // ignore error: file was opened read-only.

	w, err := os.Create(dstpath)
	if err != nil {
		return err
	}

	defer func() {
		// Report the error from Close but only if there isn't an
		// existing error.
		if e := w.Close(); err == nil {
			err = e
		}
	}()

	_, err = io.Copy(w, r)
	return err
}

func dumpItems(file string, items []Item) error {
	// copy file to backup in-case write fails
	err := CopyFile(file, file+".bkp")
	if err != nil {
		return err
	}
	bytesVal, err := json.Marshal(items)
	if err != nil {
		return err
	}
	fp, err := os.Create(file)
	if err != nil {
		return err
	}
	defer fp.Close()
	fp.Write(bytesVal)
	return nil
}

func fatalError(w http.ResponseWriter, err error) {
	w.WriteHeader(http.StatusInternalServerError)
	fmt.Fprintf(w, "Error: %s", err.Error())
}

func some(vals []string) (string, error) {
	for _, val := range vals {
		if val != "" {
			return val, nil
		}
	}
	return "", errors.New("Missing value")
}

func tmdbSearch(w *http.ResponseWriter, query string) error {
	results, err := TmdbClient.GetSearchMulti(
		query,
		nil,
	)
	if err != nil {
		return err
	}
	var rendered []*searchResult
	if len(results.Results) == 0 {
		return errors.New("No results")
	}

	for i, res := range results.Results {
		if !(res.MediaType == "movie" || res.MediaType == "tv") {
			continue
		}
		name, err := some([]string{res.Title, res.Name})
		if err != nil {
			continue
		}
		data := []string{fmt.Sprintf("Type: %s", res.MediaType)}
		if date, _ := some([]string{res.ReleaseDate, res.FirstAirDate}); date != "" {
			data = append(data, fmt.Sprintf("Released: %s", date))
		}
		rendered = append(rendered, &searchResult{
			Index:     i,
			Image:     tmdb.GetImageURL(res.PosterPath, tmdb.Original),
			MediaType: res.MediaType,
			Name:      name,
			ID:        uint64(res.ID),
			URL:       fmt.Sprintf("https://themoviedb.org/%s/%d", res.MediaType, res.ID),
			Data:      data,
		})
	}
	return tmpl.ExecuteTemplate(*w, "search.frag.html", map[string]any{
		"SearchResults": rendered,
	})
}

func chunkItems[T any](s []T, size int) [][]T {
	var chunks [][]T
	for len(s) >= size {
		chunks = append(chunks, s[:size:size])
		s = s[size:]
	}
	if len(s) > 0 {
		chunks = append(chunks, s)
	}
	return chunks
}

func renderItems(w *http.ResponseWriter, lock *sync.RWMutex, datafile string) error {
	lock.Lock()
	defer lock.Unlock()

	allItems, err := loadItems(datafile)
	if err != nil {
		return err
	}

	// filter to unwatched items
	var filtered []Item
	for _, it := range allItems {
		if it.Status != "completed" {
			filtered = append(filtered, it)
		}
	}

	gridWidth := 4
	chunks := chunkItems(filtered, gridWidth)
	// pad empty items into the last chunk if the
	// number of items isn't divisible by 4
	//
	// makes rendering on the client easier
	if last := len(chunks) - 1; last >= 0 && len(chunks[last]) < gridWidth {
		padding := make([]Item, gridWidth-len(chunks[last]))
		chunks[last] = append(chunks[last], padding...)
	}

	return tmpl.ExecuteTemplate(*w, "items.frag.html", map[string]any{
		"Items": chunks,
	})
}

func Server(port int, favicon string) error {
	lock := sync.RWMutex{}
	data_filepath := "data.json"

	buf := &bytes.Buffer{}
	if err := tmpl.ExecuteTemplate(buf, "index.html", map[string]any{
		"Favicon": favicon,
	}); err != nil {
		return err
	}
	indexBytes := buf.Bytes()

	http.HandleFunc("/",
		func(w http.ResponseWriter, r *http.Request) {
			w.Write(indexBytes)
		})

	http.HandleFunc("/search", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html")
		query := r.PostFormValue("q")
		if query == "" {
			w.WriteHeader(http.StatusBadRequest)
			w.Write([]byte("No query provided"))
			return
		}
		if err := tmdbSearch(&w, query); err != nil {
			fatalError(w, err)
			return
		}
	})

	http.HandleFunc("/add", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html")
		lock.Lock()
		defer lock.Unlock()

		items, err := loadItems(data_filepath)
		if err != nil {
			fatalError(w, err)
			return
		}
		items = append(items, Item{
			Name:   r.FormValue("name"),
			Image:  r.FormValue("image"),
			URL:    r.FormValue("url"),
			Status: "plan_to_watch",
			Added:  uint64(time.Now().UnixNano()),
		})
		if err := dumpItems(data_filepath, items); err != nil {
			fatalError(w, err)
			return
		}
		// send a trigger to the list to refetch after we add something
		w.Header().Add("HX-TRIGGER", "itemsUpdated")
		w.WriteHeader(http.StatusAccepted)
		w.Write([]byte("Added!")) // replaces the button with 'Added!'
	})

	http.HandleFunc("/items", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html")
		if err := renderItems(&w, &lock, data_filepath); err != nil {
			fatalError(w, err)
			return
		}
	})

	http.HandleFunc("/mark", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html")
		idInt, err := strconv.ParseUint(r.FormValue("id"), 10, 0)
		if err != nil {
			fatalError(w, fmt.Errorf("Error parsing ID as integer"))
			return
		}
		status := coerceStatus(r.FormValue("status"))

		lock.Lock()
		defer lock.Unlock()

		items, err := loadItems(data_filepath)
		if err != nil {
			fatalError(w, err)
			return
		}

		for ind, it := range items {
			if it.Added == idInt {
				items[ind].Status = status
				err := dumpItems(data_filepath, items)
				if err != nil {
					fatalError(w, err)
					return
				}
				w.WriteHeader(http.StatusAccepted)
				fmt.Fprintf(w, "Marked as %s", status)
				return
			}
		}

		fatalError(w, fmt.Errorf("Couldn't find a value that matched the ID %d", idInt))
	})

	// start server
	fmt.Fprintf(os.Stderr, "listening on port %d\n", port)
	return http.ListenAndServe(fmt.Sprintf(":%d", port), nil)
}
