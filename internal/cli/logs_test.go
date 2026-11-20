package cli

import (
	"context"
	"fmt"
	"net/http"
	"strings"
	"sync"
	"testing"
	"time"
)

func TestCLILogsFollow(t *testing.T) {
	t1 := time.Date(2026, 10, 24, 3, 0, 1, 500, time.UTC)
	t2 := t1.Add(time.Second)
	t3 := t2.Add(time.Second)
	line := func(tm time.Time, text string) string {
		return fmt.Sprintf(`{"Time":%q,"Deploy":"dep_1","Text":%q}`, tm.Format(time.RFC3339Nano), text)
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	var mu sync.Mutex
	var sinces, queries []string
	mux := http.NewServeMux()
	mux.HandleFunc("GET /api/services/acme/production/api/logs", func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		defer mu.Unlock()
		sinces = append(sinces, r.URL.Query().Get("since"))
		queries = append(queries, r.URL.Query().Get("q"))
		switch len(sinces) {
		case 1:
			fmt.Fprintf(w, `{"Lines":[%s,%s]}`, line(t1, "one"), line(t2, "two"))
		case 2:
			fmt.Fprintf(w, `{"Lines":[%s]}`, line(t3, "three"))
		default:
			cancel()
			fmt.Fprint(w, `{"Lines":null}`)
		}
	})
	out := apiCLI(t, mux)
	followEvery = 5 * time.Millisecond
	if err := Run(ctx, "logs", []string{"api", "-f", "-q", "o"}); err != nil {
		t.Fatal(err)
	}
	mu.Lock()
	defer mu.Unlock()
	if sinces[0] != "" || sinces[1] != t2.Format(time.RFC3339Nano) || sinces[2] != t3.Format(time.RFC3339Nano) || queries[1] != "o" {
		t.Fatalf("sinces %v queries %v", sinces, queries)
	}
	s := out.String()
	for _, w := range []string{"one", "two", "three"} {
		if strings.Count(s, w) != 1 {
			t.Fatalf("%q printed %d times:\n%s", w, strings.Count(s, w), s)
		}
	}
}

func TestCLILogsFollowFullPage(t *testing.T) {
	t1 := time.Date(2026, 10, 24, 3, 0, 1, 0, time.UTC)
	line := func(tm time.Time) string {
		return fmt.Sprintf(`{"Time":%q,"Deploy":"dep_1","Text":"x"}`, tm.Format(time.RFC3339Nano))
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	var mu sync.Mutex
	var sinces []string
	mux := http.NewServeMux()
	mux.HandleFunc("GET /api/services/acme/production/api/logs", func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		defer mu.Unlock()
		sinces = append(sinces, r.URL.Query().Get("since"))
		if len(sinces) == 1 {
			fmt.Fprintf(w, `{"Lines":[%s,%s]}`, line(t1), line(t1.Add(time.Second)))
			return
		}
		cancel()
		fmt.Fprint(w, `{"Lines":null}`)
	})
	apiCLI(t, mux)
	every := followEvery
	followEvery = time.Hour
	defer func() { followEvery = every }()
	if err := Run(ctx, "logs", []string{"api", "-f", "-n", "2"}); err != nil {
		t.Fatal(err)
	}
	mu.Lock()
	defer mu.Unlock()
	if len(sinces) != 2 || sinces[1] != t1.Add(time.Second).Format(time.RFC3339Nano) {
		t.Fatalf("sinces %v", sinces)
	}
}

func TestCLILogsErrors(t *testing.T) {
	var stream string
	mux := http.NewServeMux()
	mux.HandleFunc("GET /api/services/acme/production/api/logs", func(w http.ResponseWriter, r *http.Request) {
		stream = r.URL.Query().Get("stream")
		fmt.Fprint(w, `{"Lines":[{"Time":"2026-11-05T10:00:01Z","Text":"listening"},{"Time":"2026-11-05T10:00:02Z","Text":"panic: x","Stream":"stderr"}]}`)
	})
	out := apiCLI(t, mux)
	if err := Run(context.Background(), "logs", []string{"api"}); err != nil {
		t.Fatal(err)
	}
	if stream != "" || out.String() != "2026-11-05 10:00:01  listening\n2026-11-05 10:00:02  stderr  panic: x\n" {
		t.Fatalf("%q %q", stream, out.String())
	}
	if err := Run(context.Background(), "logs", []string{"api", "--errors"}); err != nil {
		t.Fatal(err)
	}
	if stream != "stderr" {
		t.Fatalf("%q", stream)
	}
}
