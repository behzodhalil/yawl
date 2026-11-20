package main

import (
	"fmt"
	"io"
	"net/http"
	"os"
	"strings"
	"time"
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
	case "print":
		fmt.Println("greeting is " + os.Getenv("GREETING"))
	}
	started := time.Now()
	http.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		if mode == "flaky" && time.Since(started) > 40*time.Second {
			http.Error(w, "flaky", http.StatusInternalServerError)
			return
		}
		fmt.Fprint(w, version)
	})
	http.HandleFunc("/file", func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodPut {
			b, _ := io.ReadAll(r.Body)
			if err := os.WriteFile("/data/note", b, 0o644); err != nil {
				http.Error(w, err.Error(), http.StatusInternalServerError)
			}
			return
		}
		b, err := os.ReadFile("/data/note")
		if err != nil {
			http.Error(w, err.Error(), http.StatusNotFound)
			return
		}
		w.Write(b)
	})
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
