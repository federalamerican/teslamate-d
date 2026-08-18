package main

import (
	"compress/gzip"
	"context"
	"embed"
	"io/fs"
	"log"
	"net/http"
	"os"
	"os/signal"
	"strconv"
	"strings"
	"syscall"
	"time"
)

//go:embed all:web/dist
var webFS embed.FS

func main() {
	cfg := loadConfig()
	log.Printf("teslamate-dash starting on :%s (demo=%v units=%s)", cfg.Port, cfg.Demo, cfg.Units)

	var store Store
	if cfg.Demo {
		store = newDemoStore(cfg.Units)
		log.Printf("DEMO MODE: serving synthetic data, no database connection is made")
	} else {
		db, err := openDB(cfg)
		if err != nil {
			log.Fatalf("database connection failed: %v", err)
		}
		defer db.Close()
		if err := db.checkSchema(context.Background()); err != nil {
			log.Fatalf("schema check failed: %v", err)
		}
		log.Printf("connected read-only to TeslaMate database %q", cfg.dbName)
		store = db
	}

	mux := http.NewServeMux()
	registerAPI(mux, store, cfg)

	sub, err := fs.Sub(webFS, "web/dist")
	if err != nil {
		log.Fatalf("embed: %v", err)
	}
	mux.Handle("/", staticCache(sub, http.FileServer(http.FS(sub))))

	srv := &http.Server{
		Addr:              ":" + cfg.Port,
		Handler:           logRequests(gzipResponses(mux)),
		ReadHeaderTimeout: 10 * time.Second,
	}

	go func() {
		if err := srv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			log.Fatalf("http server: %v", err)
		}
	}()

	stop := make(chan os.Signal, 1)
	signal.Notify(stop, syscall.SIGINT, syscall.SIGTERM)
	<-stop

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	_ = srv.Shutdown(ctx)
	log.Printf("shut down cleanly")
}

// staticCache lets content-addressed Vite assets stay in the browser cache,
// while the HTML entry point always checks for a newly deployed build.
func staticCache(static fs.FS, next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case strings.HasPrefix(r.URL.Path, "/assets/"):
			name := strings.TrimPrefix(r.URL.Path, "/")
			if fs.ValidPath(name) {
				if info, err := fs.Stat(static, name); err == nil && !info.IsDir() {
					w.Header().Set("Cache-Control", "public, max-age=31536000, immutable")
				}
			}
		case r.URL.Path == "/" || r.URL.Path == "/index.html":
			w.Header().Set("Cache-Control", "no-cache")
		}
		next.ServeHTTP(w, r)
	})
}

// gzipResponses reduces the JavaScript, CSS, and JSON sent over the network.
// Range and HEAD responses stay uncompressed so FileServer can preserve their
// normal semantics.
func gzipResponses(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Add("Vary", "Accept-Encoding")
		if r.Method == http.MethodHead || r.Header.Get("Range") != "" || !acceptsGzip(r.Header.Get("Accept-Encoding")) {
			next.ServeHTTP(w, r)
			return
		}
		w.Header().Set("Content-Encoding", "gzip")
		gz, err := gzip.NewWriterLevel(w, gzip.BestSpeed)
		if err != nil {
			next.ServeHTTP(w, r)
			return
		}
		defer gz.Close()
		next.ServeHTTP(&gzipResponseWriter{ResponseWriter: w, writer: gz}, r)
	})
}

type gzipResponseWriter struct {
	http.ResponseWriter
	writer *gzip.Writer
}

func (w *gzipResponseWriter) WriteHeader(code int) {
	w.Header().Del("Content-Length")
	w.ResponseWriter.WriteHeader(code)
}

func (w *gzipResponseWriter) Write(b []byte) (int, error) {
	w.Header().Del("Content-Length")
	return w.writer.Write(b)
}

func acceptsGzip(header string) bool {
	var wildcard *float64
	for _, part := range strings.Split(header, ",") {
		fields := strings.Split(part, ";")
		encoding := strings.TrimSpace(fields[0])
		quality := 1.0
		for _, param := range fields[1:] {
			key, value, ok := strings.Cut(strings.TrimSpace(param), "=")
			if !ok || !strings.EqualFold(strings.TrimSpace(key), "q") {
				continue
			}
			parsed, err := strconv.ParseFloat(strings.TrimSpace(value), 64)
			if err != nil || parsed < 0 || parsed > 1 {
				quality = 0
			} else {
				quality = parsed
			}
			break
		}
		if strings.EqualFold(encoding, "gzip") {
			return quality > 0
		}
		if encoding != "*" {
			continue
		}
		wildcard = &quality
	}
	return wildcard != nil && *wildcard > 0
}

func logRequests(h http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		start := time.Now()
		h.ServeHTTP(w, r)
		log.Printf("%s %s (%s)", r.Method, r.URL.Path, time.Since(start).Round(time.Millisecond))
	})
}
