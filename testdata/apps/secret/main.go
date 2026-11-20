package main

import (
	"fmt"
	"net/http"
	"os"
	"strings"
)

func main() {
	mode := read("MODE", "ok")
	version := read("VERSION", "v1")
	port := os.Getenv("PORT")
	if port == "" {
		port = "10000"
	}
	host := "0.0.0.0"
	switch mode {
	case "crash":
		fmt.Println("Traceback (most recent call last):")
		if g := os.Getenv("GREETING"); g != "" {
			fmt.Println("ModuleNotFoundError: No module named 'jinja2' while greeting " + g)
		} else {
			fmt.Println("ModuleNotFoundError: No module named 'jinja2'")
		}
		os.Exit(1)
	case "env":
		version = os.Getenv("GREETING")
	case "localhost":
		host = "127.0.0.1"
	}
	http.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) { fmt.Fprint(w, version) })
	fmt.Println("listening on", host+":"+port)
	if err := http.ListenAndServe(host+":"+port, nil); err != nil {
		fmt.Println(err)
		os.Exit(1)
	}
}

func read(name, def string) string {
	b, err := os.ReadFile(name)
	if err != nil {
		return def
	}
	return strings.TrimSpace(string(b))
}
