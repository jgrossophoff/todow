package main

import (
	"bytes"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"html/template"
	"log"
	"net/http"
	"regexp"
	"strconv"
	"time"

	"github.com/boltdb/bolt"
	"github.com/j1436go/todow"
)

type reqType int

const (
	reqTypeCLI = iota
	reqTypeForm
)

type boltDB struct {
	*bolt.DB
}

var (
	listenAddr = flag.String("a", ":9999", "Listen address")
	user       = flag.String("u", todow.HTTPUser, "HTTP Basic username")
	pass       = flag.String("p", todow.HTTPPassword, "HTTP Basic password")

	db boltDB

	bucketName    = []byte("todow")
	collectionKey = []byte("items")

	idRegexp = regexp.MustCompile(todow.APIPath + "([0-9]+)")
)

func main() {
	flag.Parse()

	http.HandleFunc(todow.APIPath, func(w http.ResponseWriter, r *http.Request) {
		switch r.Method {
		case "GET":
			authMiddleware(allItems)(w, r)
		case "POST":
			authMiddleware(addItem)(w, r)
		case "DELETE":
			authMiddleware(withID(removeItem))(w, r)
		case "PATCH":
			authMiddleware(withID(completeItem))(w, r)
		default:
			http.NotFound(w, r)
		}
	})

	http.HandleFunc("/", authMiddleware(func(w http.ResponseWriter, r *http.Request) {
		buf, err := db.allItems()
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}

		var col []*todow.Item
		if err = json.Unmarshal(buf, &col); err != nil {
			http.Error(w, fmt.Sprintf("unable to unmarshal collection: %s", err.Error()), http.StatusInternalServerError)
			return
		}

		if err := tmpl.Execute(w, struct {
			Items   []*todow.Item
			APIPath string
		}{
			col,
			todow.APIPath,
		}); err != nil {
			log.Println(err)
		}
	}))

	log.Printf("listening on %s", *listenAddr)
	http.ListenAndServe(*listenAddr, nil)
}

func init() {
	d, err := bolt.Open("todos.db", 0600, nil)
	if err != nil {
		log.Panicf("unable to open bolt db: %s", err)
	}
	db = boltDB{d}
}

func withID(h func(w http.ResponseWriter, r *http.Request, id int64)) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		m := idRegexp.FindStringSubmatch(r.URL.Path)
		if len(m) == 0 {
			http.NotFound(w, r)
			return
		}
		id, _ := strconv.ParseInt(m[1], 10, 64)
		h(w, r, id)
	}
}

func addItem(w http.ResponseWriter, r *http.Request) {
	var item todow.Item

	var typ reqType

	if r.Header.Get("Content-Type") == "application/json" {
		typ = reqTypeCLI
		err := json.NewDecoder(r.Body).Decode(&item)
		if err != nil {
			http.Error(w, fmt.Sprintf("unable to decode todo item: %s"), http.StatusBadRequest)
			return
		}
		defer r.Body.Close()
	} else if r.Header.Get("Content-Type") == "application/x-www-form-urlencoded" {
		typ = reqTypeForm
		r.ParseForm()
		body := r.FormValue("body")
		item.Body = body
		item.Created = time.Now()
	} else {
		http.Error(w, "content type not supported", http.StatusBadRequest)
		return
	}

	err := db.addItem(&item)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	switch typ {
	case reqTypeCLI:
		w.WriteHeader(201)
		fmt.Fprintf(w, "Added item #%d\n", item.ID)
	case reqTypeForm:
		http.Redirect(w, r, "/", 303)
	default:
		http.Redirect(w, r, "/", 303)
	}
}

func (db *boltDB) addItem(item *todow.Item) error {
	return db.Update(func(tx *bolt.Tx) error {
		col := []*todow.Item{}

		buck, err := tx.CreateBucketIfNotExists(bucketName)
		if err != nil {
			return fmt.Errorf("unable to create/get bucket: %s", err)
		}

		p := buck.Get(collectionKey)

		if p != nil {
			err = json.NewDecoder(bytes.NewBuffer(p)).Decode(&col)
			if err != nil {
				return fmt.Errorf("collection seems corrupt: %s", err)
			}
		}

		var id int64 = 1
		for _, v := range col {
			if v.ID >= id {
				id = v.ID + 1
			}
		}

		item.ID = id

		col = append(col, item)

		j, err := json.Marshal(col)
		if err != nil {
			return fmt.Errorf("unable to marshal item collection: %s", err)
		}

		buck.Put(collectionKey, j)
		log.Printf("added item %+v", item)
		return nil
	})
}

func removeItem(w http.ResponseWriter, r *http.Request, id int64) {
	switch err := db.removeItem(id).(type) {
	case ErrNotFound:
		http.NotFound(w, r)
	case error:
		http.Error(w, err.Error(), http.StatusInternalServerError)
	case nil:
		w.WriteHeader(200)
		fmt.Fprintf(w, "Removed item #%d\n", id)
	}
}

func (db boltDB) removeItem(id int64) error {
	return db.Update(func(tx *bolt.Tx) error {
		col := []*todow.Item{}

		buck, err := tx.CreateBucketIfNotExists(bucketName)
		if err != nil {
			fmt.Errorf("unable to create/get bucket: %s", err)
			return err
		}

		p := buck.Get(collectionKey)

		if p == nil {
			return new(ErrNotFound)
		}

		err = json.NewDecoder(bytes.NewBuffer(p)).Decode(&col)
		if err != nil {
			return fmt.Errorf("collection seems corrupt: %s", err)
		}

		for i, v := range col {
			if v.ID == id {
				col = append(col[0:i], col[i+1:]...)
				j, err := json.Marshal(col)
				if err != nil {
					return fmt.Errorf("unable to marshal collection: %s", err)
				}

				buck.Put(collectionKey, j)
				log.Printf("removed item %d", id)
				return nil
			}
		}

		return new(ErrNotFound)
	})
}

func completeItem(w http.ResponseWriter, r *http.Request, id int64) {
	switch err := db.completeItem(id).(type) {
	case ErrNotFound:
		http.NotFound(w, r)
	case error:
		http.Error(w, err.Error(), http.StatusInternalServerError)
	case nil:
		w.WriteHeader(200)
		fmt.Fprintf(w, "Completed item #%d\n", id)
	}
}

func (db boltDB) completeItem(id int64) error {
	return db.Update(func(tx *bolt.Tx) error {
		col := []*todow.Item{}

		buck, err := tx.CreateBucketIfNotExists(bucketName)
		if err != nil {
			return fmt.Errorf("unable to create/get bucket: %s", err)
		}

		p := buck.Get(collectionKey)

		if p == nil {
			return new(ErrNotFound)
		}

		err = json.NewDecoder(bytes.NewBuffer(p)).Decode(&col)
		if err != nil {
			return fmt.Errorf("collection seems corrupt: %s", err)
		}

		for i, v := range col {
			if v.ID == id {
				col[i].Done = true
				j, err := json.Marshal(col)
				if err != nil {
					return fmt.Errorf("unable to marshal collection: %s", err)
				}

				buck.Put(collectionKey, j)
				log.Printf("completed item %d", id)
				return nil
			}
		}

		return new(ErrNotFound)
	})
}

func allItems(w http.ResponseWriter, r *http.Request) {
	p, err := db.allItems()
	if err != nil {
		http.Error(w, fmt.Sprintf("no items yet"), http.StatusInternalServerError)
		return
	}

	log.Printf("%s", p)

	w.Header().Set("Content-Type", "application/json")
	w.Write(p)
}

func (db boltDB) allItems() ([]byte, error) {
	var buf []byte

	return buf, db.View(func(tx *bolt.Tx) error {
		buck := tx.Bucket(bucketName)
		if buck == nil {
			return errors.New("no items yet")
		}

		buf = buck.Get(collectionKey)
		if buf == nil {
			return errors.New("no items yet")
		}

		return nil
	})
}

func authMiddleware(h http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		u, p, _ := r.BasicAuth()
		if !authorized(u, p) {
			w.Header().Set("WWW-Authenticate", "Basic")
			http.Error(w, http.StatusText(http.StatusUnauthorized), http.StatusUnauthorized)
			return
		}

		h.ServeHTTP(w, r)
	}
}

func authorized(u, p string) bool {
	return u == *user && p == *pass
}

type ErrNotFound struct{}

func (e ErrNotFound) Error() string { return "not found" }

var tmpl = template.Must(template.New("").Parse(`
<!DOCTYPE html>
<html lang="en">
<head>
	<meta charset="UTF-8">
	<title>Todow</title>
	<style>
		td {
			padding: 4px 10px;
		}
	</style>
</head>
<body>
	Web todo list

	<h2>Items</h2>
	<table>
		<thead>
			<tr>
				<td>ID</td>
				<td>Body</td>
				<td>Created</td>
				<td>Done</td>
				<td>Remove</td>
			</tr>
		</thead>
		{{range .Items}}
			<tr class="item" data-id="{{.ID}}">
				<td>{{.ID}}</td>
				<td>{{.Body}}</td>
				<td>{{.Created.Format "Mon 02.01.2006 15:04:05"}}</td>
				<td>{{.Done}}</td>
				<td>
					<button class="rm-trigger">Remove</button>
				</td>
			</tr>
		{{end}}
	</table>

	<h2>Add</h2>
	<form action="{{$.APIPath}}" method="POST">
		<input type="text" name="body" placeholder="Body">
		<button>Submit</button>
	</form>

	<script>
		var items = document.querySelectorAll(".item");

		for (var i = items.length-1; i >= 0; i--) {
			var item = items[i];
			var trigger = item.querySelector(".rm-trigger");

			bindRemove(item, trigger);
		}

		function bindRemove(item, trigger) {
			trigger.addEventListener("click", function(e) {
				var id = item.getAttribute("data-id");
				if(confirm("Item #"+id+" wirklich löschen?")) {
					var xhr = new XMLHttpRequest();

					xhr.addEventListener("load", function(e) {
						if (xhr.status === 200) {
							item.remove();
							return;
						}

						alert("Delete failed. Check console.");
						console.log(xhr);
						console.log(e);
					});

					xhr.open("DELETE", "/api/"+id.toString());
					xhr.send();

				}
			});
		}
	</script>
</body>
</html>
`))
