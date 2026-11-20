package main

import (
	"context"
	"fmt"
	"log"
	"net/http"
	"os"
	"time"

	"github.com/jackc/pgx/v5"
)

func main() {
	if len(os.Args) > 1 && os.Args[1] == "migrate" {
		migrate()
		return
	}
	http.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		ctx, cancel := context.WithTimeout(r.Context(), 5*time.Second)
		defer cancel()
		conn, err := pgx.Connect(ctx, os.Getenv("DATABASE_URL"))
		if err != nil {
			http.Error(w, "database: "+err.Error(), http.StatusInternalServerError)
			return
		}
		defer conn.Close(context.Background())
		if _, err := conn.Exec(ctx, "CREATE TABLE IF NOT EXISTS notes (note text)"); err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		var note string
		err = conn.QueryRow(ctx, "SELECT note FROM notes LIMIT 1").Scan(&note)
		if err == pgx.ErrNoRows {
			note = "no notes"
		} else if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		fmt.Fprintln(w, note)
	})
	log.Fatal(http.ListenAndServe(":"+os.Getenv("PORT"), nil))
}

func migrate() {
	if os.Getenv("MIGRATE_FAIL") == "1" {
		fmt.Println("migration failed")
		os.Exit(1)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	conn, err := pgx.Connect(ctx, os.Getenv("DATABASE_URL"))
	if err != nil {
		log.Fatal(err)
	}
	defer conn.Close(ctx)
	if _, err := conn.Exec(ctx, "CREATE TABLE IF NOT EXISTS notes (note text)"); err != nil {
		log.Fatal(err)
	}
	fmt.Println("migrated")
}
