package main

import (
	"compress/gzip"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"
	"testing/fstest"
	"time"
)

type detailCountingStore struct {
	*demoStore
	detailCalls int
}

func (s *detailCountingStore) Detail(ctx context.Context, id string) (*Detail, error) {
	s.detailCalls++
	return s.demoStore.Detail(ctx, id)
}

type detailErrorStore struct {
	*demoStore
	detailCalls int
}

func (s *detailErrorStore) Detail(context.Context, string) (*Detail, error) {
	s.detailCalls++
	return nil, errors.New("detail unavailable")
}

func TestDetailSeries(t *testing.T) {
	start := time.Date(2026, time.January, 1, 12, 0, 0, 0, time.UTC)
	for _, count := range []int{0, 1, 2, 96} {
		t.Run(strconv.Itoa(count), func(t *testing.T) {
			points := make([]driveDetailPoint, count)
			for i := range points {
				points[i] = driveDetailPoint{T: start.Add(time.Duration(i) * time.Second), Speed: float64(i), Soc: float64(100 - i)}
			}
			series := detailSeries(points)
			if count == 0 {
				if series != nil {
					t.Fatalf("empty series = %#v, want nil", series)
				}
				return
			}
			if series[0].T != points[0].T || series[len(series)-1].T != points[len(points)-1].T {
				t.Fatal("series must retain the first and final samples")
			}
			if count == 96 && len(series) != 49 {
				t.Fatalf("series length = %d, want 49", len(series))
			}
		})
	}
}

func TestDemoDetailUsesFullTrace(t *testing.T) {
	store := newDemoStore("km")
	if len(store.drives) == 0 {
		t.Fatal("demo store has no drives")
	}
	drive := store.drives[0]
	detail, err := store.Detail(context.Background(), "d"+strconv.Itoa(drive.id))
	if err != nil {
		t.Fatalf("Detail: %v", err)
	}
	if detail == nil {
		t.Fatal("Detail returned nil")
	}
	if got, want := len(detail.Coords), len(drive.coords); got != want {
		t.Fatalf("detail coordinate count = %d, want %d", got, want)
	}
	if len(detail.Coords) <= maxTracePoints {
		t.Fatalf("detail coordinate count = %d, want more than overview cap %d", len(detail.Coords), maxTracePoints)
	}
}

func TestDetailResponseUsesBoundedDetailCache(t *testing.T) {
	store := &detailCountingStore{demoStore: newDemoStore("km")}
	mux := http.NewServeMux()
	registerAPI(mux, store, Config{Units: "km"})
	h := gzipResponses(mux)
	path := "/api/activities/d" + strconv.Itoa(store.drives[0].id)

	for range 2 {
		rr := httptest.NewRecorder()
		req := httptest.NewRequest(http.MethodGet, path, nil)
		req.Header.Set("Accept-Encoding", "gzip")
		h.ServeHTTP(rr, req)
		if rr.Code != http.StatusOK {
			t.Fatalf("status = %d, want %d", rr.Code, http.StatusOK)
		}
		if got := rr.Header().Get("Content-Encoding"); got != "gzip" {
			t.Fatalf("Content-Encoding = %q, want gzip", got)
		}
		var detail Detail
		reader, err := gzip.NewReader(rr.Body)
		if err != nil {
			t.Fatalf("open gzip response: %v", err)
		}
		if err := json.NewDecoder(reader).Decode(&detail); err != nil {
			reader.Close()
			t.Fatalf("decode detail: %v", err)
		}
		reader.Close()
		if len(detail.Coords) <= maxTracePoints {
			t.Fatalf("detail coordinate count = %d, want more than overview cap %d", len(detail.Coords), maxTracePoints)
		}
	}
	if store.detailCalls != 1 {
		t.Fatalf("detail calls = %d, want cached single call", store.detailCalls)
	}
}

func TestDetailErrorsAndUnknownActivitiesAreNotCached(t *testing.T) {
	store := &detailErrorStore{demoStore: newDemoStore("km")}
	mux := http.NewServeMux()
	registerAPI(mux, store, Config{Units: "km"})

	for range 2 {
		rr := httptest.NewRecorder()
		mux.ServeHTTP(rr, httptest.NewRequest(http.MethodGet, "/api/activities/d1", nil))
		if rr.Code != http.StatusInternalServerError {
			t.Fatalf("status = %d, want %d", rr.Code, http.StatusInternalServerError)
		}
	}
	if store.detailCalls != 2 {
		t.Fatalf("detail calls = %d, want errors not to be cached", store.detailCalls)
	}

	unknownMux := http.NewServeMux()
	registerAPI(unknownMux, newDemoStore("km"), Config{Units: "km"})
	unknown := httptest.NewRecorder()
	unknownMux.ServeHTTP(unknown, httptest.NewRequest(http.MethodGet, "/api/activities/not-an-activity", nil))
	if unknown.Code != http.StatusNotFound {
		t.Fatalf("unknown activity status = %d, want %d", unknown.Code, http.StatusNotFound)
	}
}

func TestResponseCacheHonorsConfiguredBound(t *testing.T) {
	cache := newRespCache(2)
	cache.put("first", 1)
	cache.put("second", 2)
	cache.put("third", 3)
	if len(cache.m) > 2 {
		t.Fatalf("cache size = %d, want at most 2", len(cache.m))
	}
}

func BenchmarkDetailPayload(b *testing.B) {
	for _, count := range []int{600, 2700, 7200, 10_000, 20_000} {
		b.Run(strconv.Itoa(count), func(b *testing.B) {
			points := make([]driveDetailPoint, count)
			for i := range points {
				points[i] = driveDetailPoint{Lng: float64(i) / 100_000, Lat: float64(i) / 200_000, Speed: float64(i % 130), Soc: float64(100 - i%100)}
			}
			b.ReportAllocs()
			b.ResetTimer()
			for range b.N {
				coords := make([][]float64, 0, len(points))
				for _, p := range points {
					coords = append(coords, []float64{p.Lng, p.Lat, p.Speed})
				}
				_, _ = json.Marshal(Detail{Activity: Activity{Coords: coords}, Series: detailSeries(points)})
			}
		})
	}
}

func TestGzipResponses(t *testing.T) {
	h := gzipResponses(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/plain; charset=utf-8")
		_, _ = w.Write([]byte("compressed response"))
	}))
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.Header.Set("Accept-Encoding", "br, gzip")
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, req)

	if got := rr.Header().Get("Content-Encoding"); got != "gzip" {
		t.Fatalf("Content-Encoding = %q, want gzip", got)
	}
	if !strings.Contains(rr.Header().Get("Vary"), "Accept-Encoding") {
		t.Fatalf("Vary = %q, want Accept-Encoding", rr.Header().Get("Vary"))
	}
	reader, err := gzip.NewReader(rr.Result().Body)
	if err != nil {
		t.Fatalf("open gzip response: %v", err)
	}
	defer reader.Close()
	body, err := io.ReadAll(reader)
	if err != nil {
		t.Fatalf("read gzip response: %v", err)
	}
	if got := string(body); got != "compressed response" {
		t.Fatalf("body = %q", got)
	}
}

func TestGzipResponsesIdentityHasVary(t *testing.T) {
	h := gzipResponses(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte("identity response"))
	}))
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.Header.Set("Accept-Encoding", "br")
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, req)

	if got := rr.Header().Get("Content-Encoding"); got != "" {
		t.Fatalf("Content-Encoding = %q, want identity response", got)
	}
	if !strings.Contains(rr.Header().Get("Vary"), "Accept-Encoding") {
		t.Fatalf("Vary = %q, want Accept-Encoding", rr.Header().Get("Vary"))
	}
}

func TestStaticCache(t *testing.T) {
	files := fstest.MapFS{"assets/index-abc.js": &fstest.MapFile{Data: []byte("asset")}}
	h := staticCache(files, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {}))

	asset := httptest.NewRecorder()
	h.ServeHTTP(asset, httptest.NewRequest(http.MethodGet, "/assets/index-abc.js", nil))
	if got := asset.Header().Get("Cache-Control"); got != "public, max-age=31536000, immutable" {
		t.Fatalf("asset Cache-Control = %q", got)
	}

	index := httptest.NewRecorder()
	h.ServeHTTP(index, httptest.NewRequest(http.MethodGet, "/", nil))
	if got := index.Header().Get("Cache-Control"); got != "no-cache" {
		t.Fatalf("index Cache-Control = %q", got)
	}

	missing := httptest.NewRecorder()
	h.ServeHTTP(missing, httptest.NewRequest(http.MethodGet, "/assets/missing.js", nil))
	if got := missing.Header().Get("Cache-Control"); got != "" {
		t.Fatalf("missing asset Cache-Control = %q, want empty", got)
	}
}

func TestAcceptsGzip(t *testing.T) {
	for _, tc := range []struct {
		header string
		want   bool
	}{
		{"gzip, br", true},
		{"br, gzip;q=0", false},
		{"br, gzip;q=0.00", false},
		{"GZip", true},
		{"*;q=0.5", true},
		{"br", false},
	} {
		if got := acceptsGzip(tc.header); got != tc.want {
			t.Errorf("acceptsGzip(%q) = %v, want %v", tc.header, got, tc.want)
		}
	}
}
