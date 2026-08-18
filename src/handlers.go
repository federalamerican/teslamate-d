package main

import (
	"encoding/json"
	"net/http"
	"strconv"
	"sync"
	"time"
)

const (
	feedDefaultLimit      = 200
	feedMaxLimit          = 1000
	cacheTTL              = 3 * time.Minute
	cacheMaxEntries       = 200
	detailCacheMaxEntries = 16
)

func registerAPI(mux *http.ServeMux, s Store, cfg Config) {
	cache := newRespCache(cacheMaxEntries)
	detailCache := newRespCache(detailCacheMaxEntries)

	mux.HandleFunc("/api/config", func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, http.StatusOK, map[string]any{
			"title":         cfg.Title,
			"units":         cfg.Units,
			"map_style_url": cfg.MapStyleURL,
			"demo":          cfg.Demo,
			"featured_sel":  s.Featured(),
		})
	})

	mux.HandleFunc("/api/cars", func(w http.ResponseWriter, r *http.Request) {
		v, err := s.Cars(r.Context())
		respond(w, v, err)
	})

	// The DB is append-only history, so summary and activities are cached
	// briefly per exact query.
	mux.HandleFunc("/api/summary", func(w http.ResponseWriter, r *http.Request) {
		if v, ok := cache.get(r.URL.RequestURI()); ok {
			respond(w, v, nil)
			return
		}
		v, err := s.Summary(r.Context(), parseRange(r))
		if err == nil {
			cache.put(r.URL.RequestURI(), v)
		}
		respond(w, v, err)
	})

	mux.HandleFunc("/api/activities", func(w http.ResponseWriter, r *http.Request) {
		if v, ok := cache.get(r.URL.RequestURI()); ok {
			respond(w, v, nil)
			return
		}
		limit := feedDefaultLimit
		if n, err := strconv.Atoi(r.URL.Query().Get("limit")); err == nil && n > 0 {
			limit = min(n, feedMaxLimit)
		}
		before := parseTime(r.URL.Query().Get("before"))
		v, err := s.Activities(r.Context(), parseRange(r), limit, before)
		if v == nil {
			v = []Activity{}
		}
		if err == nil {
			cache.put(r.URL.RequestURI(), v)
		}
		respond(w, v, err)
	})

	// Detail payload for one activity (drive series / charge curve).
	mux.HandleFunc("GET /api/activities/{id}", func(w http.ResponseWriter, r *http.Request) {
		if v, ok := detailCache.get(r.URL.RequestURI()); ok {
			respond(w, v, nil)
			return
		}
		v, err := s.Detail(r.Context(), r.PathValue("id"))
		if err != nil {
			respond(w, nil, err)
			return
		}
		if v == nil {
			writeJSON(w, http.StatusNotFound, map[string]string{"error": "unknown activity"})
			return
		}
		detailCache.put(r.URL.RequestURI(), v)
		respond(w, v, nil)
	})
}

// parseRange reads ?car_id=&from=&to= as either YYYY-MM-DD or RFC3339.
// No window means all of time, which Summary treats specially.
func parseRange(r *http.Request) Range {
	now := time.Now()
	out := Range{From: time.Date(2000, 1, 1, 0, 0, 0, 0, time.UTC), To: now.Add(24 * time.Hour), AllTime: true}
	if v := parseTime(r.URL.Query().Get("from")); !v.IsZero() {
		out.From = v
		out.AllTime = false
	}
	if v := parseTime(r.URL.Query().Get("to")); !v.IsZero() {
		// A date-only "to" means "through that day", so push to the next midnight.
		out.To = v
		if v.Hour() == 0 && v.Minute() == 0 && v.Second() == 0 {
			out.To = v.Add(24 * time.Hour)
		}
	}
	if c := r.URL.Query().Get("car_id"); c != "" {
		if n, err := strconv.Atoi(c); err == nil {
			out.CarID = &n
		}
	}
	return out
}

func parseTime(s string) time.Time {
	if s == "" {
		return time.Time{}
	}
	for _, layout := range []string{time.RFC3339, "2006-01-02"} {
		if t, err := time.Parse(layout, s); err == nil {
			return t
		}
	}
	return time.Time{}
}

func respond(w http.ResponseWriter, v any, err error) {
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, v)
}

func writeJSON(w http.ResponseWriter, code int, v any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(code)
	_ = json.NewEncoder(w).Encode(v)
}

// respCache is a tiny TTL cache for JSON-able responses.
type respCache struct {
	mu         sync.Mutex
	m          map[string]cacheEntry
	maxEntries int
}

type cacheEntry struct {
	v   any
	exp time.Time
}

func newRespCache(maxEntries int) *respCache {
	return &respCache{m: map[string]cacheEntry{}, maxEntries: maxEntries}
}

func (c *respCache) get(key string) (any, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	e, ok := c.m[key]
	if !ok || time.Now().After(e.exp) {
		delete(c.m, key)
		return nil, false
	}
	return e.v, true
}

func (c *respCache) put(key string, v any) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if len(c.m) >= c.maxEntries {
		now := time.Now()
		for k, e := range c.m {
			if now.After(e.exp) {
				delete(c.m, k)
			}
		}
		if len(c.m) >= c.maxEntries {
			c.m = map[string]cacheEntry{}
		}
	}
	c.m[key] = cacheEntry{v: v, exp: time.Now().Add(cacheTTL)}
}
