package search

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func swapBaseURL(t *testing.T, u string) {
	t.Helper()
	prev := NixBaseURL
	NixBaseURL = u
	t.Cleanup(func() { NixBaseURL = prev })
}

func TestNixPackagesHappyPath(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if got, want := r.URL.Path, "/latest-45-nixos-unstable/_search"; got != want {
			t.Errorf("path: got %q want %q", got, want)
		}
		if u, p, ok := r.BasicAuth(); !ok || u != DefaultUser || p != DefaultPass {
			t.Errorf("basic auth missing or wrong: %q %q %v", u, p, ok)
		}
		body, _ := io.ReadAll(r.Body)
		var parsed map[string]any
		if err := json.Unmarshal(body, &parsed); err != nil {
			t.Fatalf("body parse: %v", err)
		}
		if parsed["size"].(float64) != 10 {
			t.Errorf("size: %v", parsed["size"])
		}
		_, _ = io.WriteString(w, `{"hits":{"hits":[
			{"_source":{"package_attr_name":"ripgrep","package_pversion":"14.1.0","package_description":"fast grep <code>impl</code>"}},
			{"_source":{"package_attr_name":"ripgrep-all","package_pversion":"0.10.0","package_description":"ripgrep wrapper"}}
		]}}`)
	}))
	defer srv.Close()
	swapBaseURL(t, srv.URL)

	res, err := Nix(context.Background(), NixRequest{Kind: KindPackages, Query: "ripgrep"})
	if err != nil {
		t.Fatalf("Nix: %v", err)
	}
	if len(res.Packages) != 2 {
		t.Fatalf("packages: %d", len(res.Packages))
	}
	if res.Packages[0].AttrName != "ripgrep" {
		t.Errorf("attr: %q", res.Packages[0].AttrName)
	}
	if res.Packages[0].Description != "fast grep impl" {
		t.Errorf("desc: %q", res.Packages[0].Description)
	}

	text := FormatText(res)
	if !strings.Contains(text, "ripgrep\t14.1.0\tfast grep impl\n") {
		t.Errorf("text: %q", text)
	}
	if strings.Count(text, "\n") != 2 {
		t.Errorf("text lines: %q", text)
	}

	js, err := FormatJSON(res)
	if err != nil {
		t.Fatalf("FormatJSON: %v", err)
	}
	var roundtrip []Package
	if err := json.Unmarshal([]byte(js), &roundtrip); err != nil {
		t.Fatalf("json unmarshal: %v", err)
	}
	if len(roundtrip) != 2 || roundtrip[0].AttrName != "ripgrep" {
		t.Errorf("json roundtrip: %+v", roundtrip)
	}
}

func TestNixOptionsDottedQuery(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		var parsed map[string]any
		if err := json.Unmarshal(body, &parsed); err != nil {
			t.Fatalf("body: %v", err)
		}
		q := parsed["query"].(map[string]any)["bool"].(map[string]any)["must"].([]any)[0].(map[string]any)
		if _, ok := q["query_string"]; !ok {
			t.Errorf("dotted query should use query_string, got %v", q)
		}
		_, _ = io.WriteString(w, `{"hits":{"hits":[
			{"_source":{"option_name":"programs.neovim.enable","option_type":"boolean","option_description":"Whether to enable Neovim."}}
		]}}`)
	}))
	defer srv.Close()
	swapBaseURL(t, srv.URL)

	res, err := Nix(context.Background(), NixRequest{Kind: KindOptions, Query: "programs.neovim", Channel: "25.11", Size: 5})
	if err != nil {
		t.Fatalf("Nix: %v", err)
	}
	if len(res.Options) != 1 || res.Options[0].Name != "programs.neovim.enable" {
		t.Fatalf("options: %+v", res.Options)
	}
}

func TestNixOptionsKeywordQueryUsesMultiMatch(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		var parsed map[string]any
		_ = json.Unmarshal(body, &parsed)
		q := parsed["query"].(map[string]any)["bool"].(map[string]any)["must"].([]any)[0].(map[string]any)
		if _, ok := q["multi_match"]; !ok {
			t.Errorf("non-dotted should use multi_match, got %v", q)
		}
		_, _ = io.WriteString(w, `{"hits":{"hits":[]}}`)
	}))
	defer srv.Close()
	swapBaseURL(t, srv.URL)

	res, err := Nix(context.Background(), NixRequest{Kind: KindOptions, Query: "neovim"})
	if err != nil {
		t.Fatalf("Nix: %v", err)
	}
	if got := FormatText(res); got != "no results\n" {
		t.Errorf("empty text: %q", got)
	}
}

func TestNixHTTPError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "missing index", http.StatusNotFound)
	}))
	defer srv.Close()
	swapBaseURL(t, srv.URL)

	_, err := Nix(context.Background(), NixRequest{Kind: KindPackages, Query: "anything"})
	if err == nil {
		t.Fatal("expected error")
	}
	if !strings.Contains(err.Error(), "404") {
		t.Errorf("err: %v", err)
	}
}

func TestNixEmptyQueryRejected(t *testing.T) {
	_, err := Nix(context.Background(), NixRequest{Kind: KindPackages, Query: "  "})
	if err == nil {
		t.Fatal("expected error")
	}
}

func TestNixUnknownKind(t *testing.T) {
	_, err := Nix(context.Background(), NixRequest{Kind: NixKind("bogus"), Query: "x"})
	if err == nil {
		t.Fatal("expected error")
	}
}
