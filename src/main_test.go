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

func TestDetailSeriesPreservesNativeMeasurements(t *testing.T) {
	start := time.Date(2026, time.January, 1, 12, 0, 0, 0, time.UTC)
	points := []driveDetailPoint{
		{T: start, Lng: float64p(-82.4), Lat: float64p(28.1), Speed: float64p(50), Soc: float64p(80)},
		{T: start.Add(time.Second), Lng: float64p(-82.3), Lat: float64p(28.2), Speed: float64p(51)},
		{T: start.Add(2 * time.Second), Soc: float64p(79)},
		{T: start.Add(3 * time.Second)},
	}
	series := detailSeries(points)
	if len(series) != 3 {
		t.Fatalf("series length = %d, want 3 genuine telemetry rows", len(series))
	}
	if series[0].T != points[0].T || series[2].T != points[2].T {
		t.Fatal("series must retain chronological native measurements")
	}
	if series[1].Soc != nil || series[2].Speed != nil {
		t.Fatal("missing telemetry must remain null")
	}
	if series[0].Lng == nil || series[0].Lat == nil || *series[0].Lng != -82.4 || *series[0].Lat != 28.1 {
		t.Fatal("telemetry must retain the position from its source row")
	}
	if series[2].Lng != nil || series[2].Lat != nil {
		t.Fatal("missing coordinates must remain null")
	}
	if series := detailSeries([]driveDetailPoint{{T: start}}); series != nil {
		t.Fatalf("rows without telemetry = %#v, want nil", series)
	}
}

func TestDetailRouteBoundsCoordinatesWithoutReducingTelemetry(t *testing.T) {
	points := make([]driveDetailPoint, maxDetailTracePoints+2)
	for i := range points {
		v := float64(i)
		points[i] = driveDetailPoint{Lng: float64p(v), Lat: float64p(v), Speed: float64p(v), Soc: float64p(80)}
	}
	points[1].Lng = nil
	route := detailRoute(points)
	if len(route) != maxDetailTracePoints {
		t.Fatalf("route length = %d, want %d", len(route), maxDetailTracePoints)
	}
	if *route[0].Lng != 0 || *route[len(route)-1].Lng != float64(len(points)-1) {
		t.Fatal("route must retain coordinate endpoints")
	}
	if got := len(detailSeries(points)); got != len(points) {
		t.Fatalf("telemetry length = %d, want %d", got, len(points))
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
	if len(detail.Series) == 0 || detail.Series[0].Lng == nil || detail.Series[0].Lat == nil {
		t.Fatal("demo telemetry must include its matching coordinates")
	}
	if *detail.Series[0].Lng != drive.coords[0][0] || *detail.Series[0].Lat != drive.coords[0][1] {
		t.Fatal("demo telemetry coordinates do not match the detailed route")
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
	for _, count := range []int{600, 2700, 7200, 10_000, 20_000, 50_000} {
		b.Run(strconv.Itoa(count), func(b *testing.B) {
			points := make([]driveDetailPoint, count)
			for i := range points {
				lng, lat := float64(i)/100_000, float64(i)/200_000
				points[i] = driveDetailPoint{T: time.Unix(int64(i), 0).UTC(), Lng: float64p(lng), Lat: float64p(lat), Speed: float64p(float64(i % 130)), Soc: float64p(float64(100 - i%100))}
			}
			b.ReportAllocs()
			b.ResetTimer()
			for range b.N {
				route := detailRoute(points)
				coords := make([][]float64, 0, len(route))
				for _, p := range route {
					coords = append(coords, []float64{*p.Lng, *p.Lat, *p.Speed})
				}
				_, _ = json.Marshal(Detail{Activity: Activity{Coords: coords}, Series: detailSeries(points)})
			}
		})
	}
}

func BenchmarkOverviewTracePreparation(b *testing.B) {
	for _, drives := range []int{200, 500} {
		b.Run(strconv.Itoa(drives), func(b *testing.B) {
			raw := make([][][]float64, drives)
			for d := range raw {
				raw[d] = make([][]float64, 300)
				for i := range raw[d] {
					raw[d][i] = []float64{float64(i) / 10_000, float64(d)/100_000 + float64(i%11)/100_000, float64(i % 130)}
				}
			}
			b.ReportAllocs()
			b.ResetTimer()
			for range b.N {
				activities := make([]Activity, drives)
				for d, points := range raw {
					activities[d] = Activity{ID: "d" + strconv.Itoa(d), Kind: "drive", Coords: simplify(points, maxTracePoints)}
				}
				_, _ = json.Marshal(activities)
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
