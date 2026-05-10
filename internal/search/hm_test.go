package search

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func swapHMBaseURL(t *testing.T, u string) {
	t.Helper()
	prev := HMBaseURL
	HMBaseURL = u
	t.Cleanup(func() { HMBaseURL = prev })
}

const hmFixture = `{
  "options": [
    {"title": "programs.firefox.enable", "type": "boolean", "description": "Whether to enable Firefox."},
    {"title": "programs.firefox.profiles", "type": "attribute set", "description": "Firefox profile\nsettings."},
    {"title": "programs.git.enable", "type": "boolean", "description": "Whether to enable Git."}
  ]
}`

func TestHMHappyPath(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if got, want := r.URL.Path, "/data/options-release-25.11.json"; got != want {
			t.Errorf("path: got %q want %q", got, want)
		}
		_, _ = w.Write([]byte(hmFixture))
	}))
	defer srv.Close()
	swapHMBaseURL(t, srv.URL)

	res, err := HM(context.Background(), HMRequest{Query: "firefox", CacheDir: t.TempDir()})
	if err != nil {
		t.Fatalf("HM: %v", err)
	}
	if len(res.Options) != 2 {
		t.Fatalf("options: %+v", res.Options)
	}
	if res.Options[0].Name != "programs.firefox.enable" {
		t.Errorf("name: %q", res.Options[0].Name)
	}
	if res.Options[1].Description != "Firefox profile settings." {
		t.Errorf("desc whitespace: %q", res.Options[1].Description)
	}

	text := FormatHMText(res)
	if !strings.Contains(text, "programs.firefox.enable\tboolean\tWhether to enable Firefox.\n") {
		t.Errorf("text: %q", text)
	}

	js, err := FormatHMJSON(res)
	if err != nil {
		t.Fatalf("FormatHMJSON: %v", err)
	}
	var roundtrip []HMOption
	if err := json.Unmarshal([]byte(js), &roundtrip); err != nil {
		t.Fatalf("json unmarshal: %v", err)
	}
	if len(roundtrip) != 2 || roundtrip[0].Name != "programs.firefox.enable" {
		t.Errorf("roundtrip: %+v", roundtrip)
	}
}

func TestHMSizeLimit(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(hmFixture))
	}))
	defer srv.Close()
	swapHMBaseURL(t, srv.URL)

	res, err := HM(context.Background(), HMRequest{Query: "programs", Size: 1, CacheDir: t.TempDir()})
	if err != nil {
		t.Fatalf("HM: %v", err)
	}
	if len(res.Options) != 1 {
		t.Errorf("size: %d", len(res.Options))
	}
}

func TestHMNoResults(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(hmFixture))
	}))
	defer srv.Close()
	swapHMBaseURL(t, srv.URL)

	res, err := HM(context.Background(), HMRequest{Query: "doesnotexistxyz", CacheDir: t.TempDir()})
	if err != nil {
		t.Fatalf("HM: %v", err)
	}
	if got := FormatHMText(res); got != "no results\n" {
		t.Errorf("empty: %q", got)
	}
}

func TestHMCacheReused(t *testing.T) {
	hits := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits++
		_, _ = w.Write([]byte(hmFixture))
	}))
	defer srv.Close()
	swapHMBaseURL(t, srv.URL)

	dir := t.TempDir()
	for i := 0; i < 3; i++ {
		_, err := HM(context.Background(), HMRequest{Query: "git", CacheDir: dir, CacheTTL: time.Hour})
		if err != nil {
			t.Fatalf("HM: %v", err)
		}
	}
	if hits != 1 {
		t.Errorf("hits: %d, want 1 (cache should serve subsequent calls)", hits)
	}
}

func TestHMCacheRefresh(t *testing.T) {
	hits := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits++
		_, _ = w.Write([]byte(hmFixture))
	}))
	defer srv.Close()
	swapHMBaseURL(t, srv.URL)

	dir := t.TempDir()
	if _, err := HM(context.Background(), HMRequest{Query: "git", CacheDir: dir}); err != nil {
		t.Fatalf("HM: %v", err)
	}
	if _, err := HM(context.Background(), HMRequest{Query: "git", CacheDir: dir, Refresh: true}); err != nil {
		t.Fatalf("HM: %v", err)
	}
	if hits != 2 {
		t.Errorf("hits: %d, want 2 (refresh should re-fetch)", hits)
	}
	if _, err := os.ReadFile(filepath.Join(dir, "hm-options-release-25.11.json")); err != nil {
		t.Errorf("cache file missing: %v", err)
	}
}

func TestHMHTTPError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "missing", http.StatusNotFound)
	}))
	defer srv.Close()
	swapHMBaseURL(t, srv.URL)

	_, err := HM(context.Background(), HMRequest{Query: "anything", CacheDir: t.TempDir()})
	if err == nil {
		t.Fatal("expected error")
	}
	if !strings.Contains(err.Error(), "404") {
		t.Errorf("err: %v", err)
	}
}

func TestHMEmptyQueryRejected(t *testing.T) {
	_, err := HM(context.Background(), HMRequest{Query: "  "})
	if err == nil {
		t.Fatal("expected error")
	}
}
