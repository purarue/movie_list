package movie_list

import (
	"bytes"
	"embed"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"sync"
	"text/template"
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
	Image   string
	URL     string
	Watched bool
	Added   int64
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
	return items, nil
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
	fp, err := os.OpenFile(file, os.O_CREATE|os.O_WRONLY, 0644)
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
			data = fmt.Sprintf("%s<br />Released: %s</pre>", data, date)
		}
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

func Server(port int) error {
	indexData, err := index.ReadFile("index.html")
	if err != nil {
		return err
	}

	http.HandleFunc("/",
		func(w http.ResponseWriter, r *http.Request) {
			// write index to response
			w.Header().Set("Content-Type", "text/html")
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
			Name:    r.FormValue("name"),
			Image:   r.FormValue("image"),
			URL:     r.FormValue("url"),
			Added:   time.Now().UnixNano(),
			Watched: false,
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
		tmpl, err := template.New("items").Parse(`
	{{ range $chunk := .Items }}
	<div class="grid">
		{{ range $element := $chunk }}
		<article>
			<header><img src="{{ $element.Image }}" /></header>
			<div class="hidden" id="{{ print "watched" $element.Added }}">
				<input name="id" value="{{ $element.Added }}" />
			</div>
			<div>
			<p>
				{{ $element.Name }}
			</p>
			<a class="contrast" href="{{ $element.URL }}"><button role="none" class="contrast">More Info</button></a>
			<button class="contrast"
				hx-include="{{ print "#watched" $element.Added }}"
				hx-post="watched"
				hx-swap=outerHTML
				hx-confirm="{{ print "Are you sure you want to mark '" $element.Name "' watched?" }}"
				/>✔️
				</button>
			 </div>
		</article>
		{{ end }}
		</div>
	{{ end }}
`)
		if err != nil {
			fatalError(w, err)
			return
		}

		lock.Lock()
		defer lock.Unlock()
		allItems, err := loadItems(filepath)
		// filter to unwatched items, chunk into lists of 4
		var items [][]Item
		var chunk []Item
		for _, it := range allItems {
			// if we have 4 items, move values from chunk and reset
			if len(chunk) == 4 {
				items = append(items, chunk)
				chunk = make([]Item, 0)
			}
			if it.Watched == false {
				chunk = append(chunk, it)
			}
		}
		// add last chunk if not empty
		if len(chunk) > 0 {
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

	http.HandleFunc("/watched", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html")
		lock.Lock()
		defer lock.Unlock()
		w.WriteHeader(http.StatusNotImplemented)
		w.Write([]byte("I haven't implemented this yet!"))
		// TODO: load files, find the value with the matching Id == After value (this uses nanosecond epoch time as ID)
		// and flip the value in that to true
	})

	fmt.Fprintf(os.Stderr, "listening on port %d\n", port)
	return http.ListenAndServe(fmt.Sprintf(":%d", port), nil)
}
