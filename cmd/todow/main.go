package main

import (
	"bytes"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"io/ioutil"
	"net/http"
	"os"
	"strings"
	"text/tabwriter"
	"time"

	"github.com/j1436go/todow"
)

var (
	domain = flag.String("h", "http://localhost:9999", "Server domain without API path")
	user   = flag.String("u", todow.HTTPUser, "HTTP Basic username")
	pass   = flag.String("p", todow.HTTPPassword, "HTTP Basic password")

	client = http.Client{
		Timeout: time.Second * 7,
	}
)

func main() {
	flag.Parse()

	if len(flag.Args()) == 0 {
		fmt.Fprintln(os.Stderr, help)
		return
	}

	switch flag.Args()[0] {
	case "ls":
		listItems()
	case "add":
		addItem()
	case "rm":
		removeItem()
	case "c":
		completeItem()
	case "help":
		fmt.Fprintln(os.Stderr, help)
	default:
		fmt.Fprintln(os.Stderr, help)
	}
}

func addItem() {
	if len(os.Args) == 2 {
		printErrLn("Missing item text")
	}
	item := &todow.Item{
		Body:    strings.Join(os.Args[2:], " "),
		Created: time.Now(),
	}

	var buf bytes.Buffer
	err := json.NewEncoder(&buf).Encode(item)
	if err != nil {
		printErrLn("Unable to marshal item to json: %s", err)
	}

	req := request("POST")
	req.Body = ioutil.NopCloser(&buf)
	resp, err := client.Do(req)
	if err != nil {
		printErrLn("Unable to POST %s: %s", *req.URL, err)
	}

	buf.Reset()
	io.Copy(&buf, resp.Body)
	fmt.Fprintln(os.Stdout, buf.String())
}

func removeItem() {
	if len(os.Args) == 2 {
		printErrLn("Missing item id")
	}

	id := os.Args[2]

	req := request("DELETE")
	req.URL.Path += id
	resp, err := client.Do(req)
	if err != nil {
		printErrLn("Unable to DELETE %s: %s", *req.URL, err)
	}

	var buf bytes.Buffer
	io.Copy(&buf, resp.Body)
	defer resp.Body.Close()
	fmt.Fprint(os.Stdout, buf.String())
	return
}

func completeItem() {
	if len(os.Args) == 2 {
		printErrLn("Missing item id")
	}

	id := os.Args[2]

	req := request("PATCH")
	req.URL.Path += id
	resp, err := client.Do(req)
	if err != nil {
		printErrLn("Unable to PATH %s: %s", *req.URL, err)
	}

	var buf bytes.Buffer
	io.Copy(&buf, resp.Body)
	defer resp.Body.Close()
	fmt.Fprint(os.Stdout, buf.String())
	return
}

func listItems() {
	req := request("GET")
	resp, err := client.Do(req)
	if err != nil {
		printErrLn("Unable to GET %s: %s", *req.URL, err)
	}

	if strings.Contains(resp.Header.Get("Content-Type"), "text/plain") {
		var buf bytes.Buffer
		io.Copy(&buf, resp.Body)
		defer resp.Body.Close()
		fmt.Fprint(os.Stdout, buf.String())
		return
	}

	col := []*todow.Item{}
	err = json.NewDecoder(resp.Body).Decode(&col)
	if err != nil {
		printErrLn("unable to decode json response: %s", err)
	}
	defer resp.Body.Close()

	tw := tabwriter.NewWriter(os.Stdout, 0, 8, 0, '\t', 0)
	fmt.Fprintln(tw, "ID\tBody\tDone")
	for _, v := range col {
		var done rune

		if v.Done {
			done = '\u221A'
		} else {
			done = ' '
		}
		fmt.Fprintf(
			tw,
			"%d\t%s\t%c",
			v.ID,
			v.Body,
			done,
		)
		fmt.Fprintln(tw)
	}
	tw.Flush()
}

func request(method string) *http.Request {
	req, _ := http.NewRequest(method, *domain+todow.APIPath, nil)
	req.SetBasicAuth(*user, *pass)
	req.Header.Set("Content-Type", "application/json")
	return req
}

func printErrLn(f string, args ...interface{}) {
	fmt.Printf(f+"\n", args...)
	os.Exit(1)
}

var help = `todow [COMMAND] [ARGUMENTS]...

Flags:
	-domain
		Domain of the todow server


Commands:
	ls
		List all items

	add [BODY]
		Add item

	rm [ID]
		Remove item

	c [ID]
		Mark item complete

`
