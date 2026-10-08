package movie_list

import (
	"bytes"
	"embed"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"sort"
	"strconv"
	"sync"
	"html/template"
	"time"

	tmdb "github.com/cyruzin/golang-tmdb"
)

//go:embed index.html
var index embed.FS

type searchResult struct {
	Index     int
	Image     string
	MediaType string
	Name      string
	ID        int64
	URL       string
	Data      string
}

type Item struct {
	Name    string
	Status  string
	Image   string
	URL     string
	Watched bool
	Added   int64
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

func tmdbSearch(query string) ([]byte, error) {
	results, err := TmdbClient.GetSearchMulti(
		query,
		nil,
	)
	if err != nil {
		return nil, nil
	}
	var rendered []*searchResult
	for i, res := range results.Results {
		if !(res.MediaType == "movie" || res.MediaType == "tv") {
			continue
		}
		var name string
		var date string
		if res.Title != "" {
			name = res.Title
		} else if res.Name != "" {
			name = res.Name
		} else {
			continue
		}
		if res.ReleaseDate != "" {
			date = res.ReleaseDate
		} else if res.FirstAirDate != "" {
			date = res.FirstAirDate
		} else {
			date = ""
		}
		var data string
		if res.MediaType == "movie" {
			data = "<pre>Type: Movie"
		} else {
			data = "<pre>Type: TV Show"
		}
		if date != "" {
			data = fmt.Sprintf("%s<br />Released: %s", data, date)
		}
		data = data + "</pre>"
		rendered = append(rendered, &searchResult{
			Index:     i,
			Image:     tmdb.GetImageURL(res.PosterPath, tmdb.Original),
			MediaType: res.MediaType,
			Name:      name,
			ID:        res.ID,
			URL:       fmt.Sprintf("https://themoviedb.org/%s/%d", res.MediaType, res.ID),
			Data:      data,
		})
	}
	table, err := template.New("table").Parse(`
	<table class="overflow-auto">
		<thead>
			<tr>
				<th scope="col"></th>
				<th scope="col">Image</th>
				<th scope="col">Name</th>
				<th scope="col">Info</th>
			</tr>
		</thead>
		<tbody>
			{{ range $element := .SearchResults }}
		<tr>
			<div id="{{ print "data" $element.Index }}" class="hidden">
				<input name="name" value="{{ $element.Name }}" />
				<input name="url" value="{{ $element.URL }}" />
				<input name="image" value="{{ $element.Image }}" />
			</div>
			<td>
			<button hx-post="add"
				hx-swap=outerHTML
				hx-confirm="{{ print "add '" $element.Name "'?"}}"
				hx-trigger="click throttle:1000"
				hx-include="{{ print "#data" $element.Index }}">
					+Add
			</button>
			</td>
			<td><img src="{{ $element.Image }}" /></td>
			<td><a href="{{ $element.URL }}">{{ $element.Name }}</a></td>
			<td>{{ $element.Data }}</td>
		</tr>
		{{ end }}
		</tbody>
	</table>
		`)
	buf := &bytes.Buffer{}
	table.Execute(buf, map[string]any{
		"SearchResults": rendered,
	})
	if err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}

func Server(port int, favicon string) error {
	indexData, err := index.ReadFile("index.html")
	if err != nil {
		return err
	}

	http.HandleFunc("/",
		func(w http.ResponseWriter, r *http.Request) {
			// write index to response
			w.Header().Set("Content-Type", "text/html")
			tmpl, err := template.New("index").Parse(string(indexData))
			if err != nil {
				fatalError(w, err)
				return
			}
			buf := &bytes.Buffer{}
			err = tmpl.Execute(buf, map[string]any{
				"Favicon": favicon,
			})
			if err != nil {
				fatalError(w, err)
				return
			}
			w.WriteHeader(http.StatusOK)
			w.Write(buf.Bytes())
			w.Write(indexData)
		})

	// start server
	http.HandleFunc("/search", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html")
		query := r.PostFormValue("q")
		if query == "" {
			w.WriteHeader(http.StatusBadRequest)
			w.Write([]byte("No query provided"))
			return
		}
		res, err := tmdbSearch(query)
		if err != nil {
			fatalError(w, err)
			return
		}
		w.WriteHeader(http.StatusOK)
		w.Write(res)
	})

	lock := sync.RWMutex{}
	filepath := "data.json"

	http.HandleFunc("/add", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html")
		lock.Lock()
		defer lock.Unlock()
		items, err := loadItems(filepath)
		if err != nil {
			fatalError(w, err)
			return
		}
		items = append(items, Item{
			Name:   r.FormValue("name"),
			Image:  r.FormValue("image"),
			URL:    r.FormValue("url"),
			Status: "plan_to_watch",
			Added:  time.Now().UnixNano(),
		})
		fmt.Printf("%+v", items[len(items)-1])
		err = dumpItems(filepath, items)
		if err != nil {
			fatalError(w, err)
			return
		}
		// send a trigger to the list to refetch after we add something
		w.Header().Add("HX-TRIGGER", "itemsUpdated")
		w.WriteHeader(http.StatusOK)
		w.Write([]byte("Added!")) // replaces the button with 'Added!'
	})

	http.HandleFunc("/items", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html")
		// TODO: add a dropdown/another button to mark something as 'watching'?
		tmpl, err := template.New("items").Parse(`
	{{ range $chunk := .Items }}
	<div class="grid">
		{{ range $element := $chunk }}
		<article>
			{{ if $element.Name }}
			<header><img src="{{ $element.Image }}" /></header>
			<div class="hidden" id="{{ print "watched" $element.Added }}">
				<input name="id" value="{{ $element.Added }}" />
				<input name="status" value="completed" />
			</div>
			<div>
			<p>
				{{ $element.Name }}
			</p>
			<p>
			{{ if ne $element.Status "plan_to_watch" }}
				<p><i>{{ print "Status: " $element.Status }}</i></p>
			{{ end }}
			<a class="contrast" href="{{ $element.URL }}"><button role="none" class="contrast"><small>More Info</small></button></a>
			<button class="contrast"
				hx-include="{{ print "#watched" $element.Added }}"
				hx-post="mark"
				hx-swap=outerHTML
				hx-confirm="{{ print "Are you sure you want to mark '" $element.Name "' watched? (this removes it from the page)" }}"
				/><small>✔️</small>
				</button>
			 </div>
			{{ else }}
			<div style="height: 100%; width: 100%; color: white; background-color: whitesmoke; border-radius: 1rem">...</div>
			{{ end }}
		</article>
		{{ end }}
		</div>
	{{ end }}
`)
		if err != nil {
			fatalError(w, err)
			return
		}

		gridWidth := 4

		lock.Lock()
		defer lock.Unlock()
		allItems, err := loadItems(filepath)
		// filter to unwatched items, chunk into lists of 4
		var items [][]Item
		var chunk []Item
		for _, it := range allItems {
			// if we have 4 items, move values from chunk and reset
			if len(chunk) == gridWidth {
				items = append(items, chunk)
				chunk = make([]Item, 0)
			}
			if it.Status != "completed" {
				chunk = append(chunk, it)
			}
		}
		// add last chunk if not empty
		if len(chunk) > 0 {
			// if there's anything, pad with extra items to make
			// grid line up nicely
			for {
				if len(chunk) >= gridWidth {
					break
				}
				chunk = append(chunk, Item{})
			}
			items = append(items, chunk)
		}
		if err != nil {
			fatalError(w, err)
			return
		}
		buf := &bytes.Buffer{}
		err = tmpl.Execute(buf, map[string]any{
			"Items": items,
		})
		if err != nil {
			fatalError(w, err)
			return
		}
		w.WriteHeader(http.StatusOK)
		w.Write(buf.Bytes())
	})

	http.HandleFunc("/mark", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html")
		id := r.FormValue("id")
		if id == "" {
			fatalError(w, errors.New("No id passed to mark complete"))
			return
		}
		var idInt64 int64
		idInt, err := strconv.Atoi(id)
		if err != nil {
			fatalError(w, err)
			return
		}
		idInt64 = int64(idInt)
		status := coerceStatus(r.FormValue("status"))

		lock.Lock()
		defer lock.Unlock()
		filepath := "data.json"
		items, err := loadItems(filepath)
		if err != nil {
			fatalError(w, err)
			return
		}

		for ind, it := range items {
			if it.Added == idInt64 {
				items[ind] = Item{
					Name:   it.Name,
					Image:  it.Image,
					URL:    it.URL,
					Status: status,
					Added:  it.Added,
				}
				err := dumpItems(filepath, items)
				if err != nil {
					fatalError(w, err)
					return
				}
				w.WriteHeader(http.StatusAccepted)
				fmt.Fprintf(w, "Marked as %s", status)
				return
			}
		}

		fatalError(w, fmt.Errorf("Couldn't find a value that matched the ID %d", idInt64))
	})

	fmt.Fprintf(os.Stderr, "listening on port %d\n", port)
	return http.ListenAndServe(fmt.Sprintf(":%d", port), nil)
}
