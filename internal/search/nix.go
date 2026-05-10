// Package search implements the `spore search` lookup helpers.
//
// `Nix` queries the same Elasticsearch backend that powers
// https://search.nixos.org. It exists because `nix search nixpkgs`
// evaluates the registry (slow) and only covers packages, while agents
// also need NixOS option lookup. The backend takes basic-auth
// credentials embedded in the public frontend bundle; they are not
// secret but they rotate. See the MAINTENANCE notes here for what to
// bump when the backend returns 404 / 401.
//
// MAINTENANCE - read if Nix starts returning 404 / empty / 401:
//
//  1. Mapping version bump. The index name embeds a schema version
//     (DefaultVersion). Source of truth:
//     https://raw.githubusercontent.com/NixOS/nixos-search/main/version.nix
//     Bump DefaultVersion to the `frontend` value.
//  2. Credential rotation. Source of truth:
//     https://raw.githubusercontent.com/NixOS/nixos-search/main/frontend/config/webpack.common.js
//     Look for ELASTICSEARCH_{USERNAME,PASSWORD}.
//  3. Channel gone. Use a current one
//     (see https://search.nixos.org dropdown).
package search

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"
)

const (
	DefaultVersion = "45"
	DefaultUser    = "aWVSALXpZv"
	DefaultPass    = "X8gPHnzL52wFEekuxsfQ9cSh"
	DefaultChannel = "unstable"
	DefaultSize    = 10
)

var (
	NixBaseURL = "https://search.nixos.org/backend"

	httpTimeout = 15 * time.Second
)

type NixKind string

const (
	KindPackages NixKind = "packages"
	KindOptions  NixKind = "options"
)

// NixRequest configures a Nix search request.
type NixRequest struct {
	Kind    NixKind
	Query   string
	Channel string
	Size    int
	Version string
	User    string
	Pass    string
}

// Package is a single package hit from the Elasticsearch backend.
type Package struct {
	AttrName    string `json:"attr"`
	Version     string `json:"version"`
	Description string `json:"description"`
}

// Option is a single option hit from the Elasticsearch backend.
type Option struct {
	Name        string `json:"name"`
	Type        string `json:"type"`
	Description string `json:"description"`
}

// NixResult holds whichever hit kind the request returned. Only one of
// the slices is populated depending on Kind.
type NixResult struct {
	Kind     NixKind
	Packages []Package
	Options  []Option
}

// Nix runs a search against the nixos.org Elasticsearch backend.
func Nix(ctx context.Context, o NixRequest) (NixResult, error) {
	if strings.TrimSpace(o.Query) == "" {
		return NixResult{}, errors.New("search nix: query is empty")
	}
	switch o.Kind {
	case KindPackages, KindOptions:
	default:
		return NixResult{}, fmt.Errorf("search nix: unknown kind %q", o.Kind)
	}
	if o.Channel == "" {
		o.Channel = DefaultChannel
	}
	if o.Size <= 0 {
		o.Size = DefaultSize
	}
	if o.Version == "" {
		o.Version = DefaultVersion
	}
	if o.User == "" {
		o.User = DefaultUser
	}
	if o.Pass == "" {
		o.Pass = DefaultPass
	}

	body, err := buildBody(o)
	if err != nil {
		return NixResult{}, err
	}
	index := fmt.Sprintf("latest-%s-nixos-%s", o.Version, o.Channel)
	url := strings.TrimRight(NixBaseURL, "/") + "/" + index + "/_search"

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(body))
	if err != nil {
		return NixResult{}, err
	}
	req.Header.Set("Content-Type", "application/json")
	req.SetBasicAuth(o.User, o.Pass)

	client := &http.Client{Timeout: httpTimeout}
	resp, err := client.Do(req)
	if err != nil {
		return NixResult{}, fmt.Errorf("search nix: %w", err)
	}
	defer resp.Body.Close()

	respBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return NixResult{}, fmt.Errorf("search nix: read body: %w", err)
	}
	if resp.StatusCode/100 != 2 {
		snippet := strings.TrimSpace(string(respBody))
		if len(snippet) > 200 {
			snippet = snippet[:200] + "..."
		}
		return NixResult{}, fmt.Errorf("search nix: %s: %s", resp.Status, snippet)
	}

	return parseHits(o.Kind, respBody)
}

func buildBody(o NixRequest) ([]byte, error) {
	switch o.Kind {
	case KindPackages:
		return json.Marshal(map[string]any{
			"size": o.Size,
			"query": map[string]any{
				"bool": map[string]any{
					"filter": []any{map[string]any{"term": map[string]any{"type": "package"}}},
					"must": []any{map[string]any{"multi_match": map[string]any{
						"query": o.Query,
						"fields": []string{
							"package_attr_name^9",
							"package_pname^6",
							"package_description^3",
							"package_longDescription",
							"package_programs",
						},
					}}},
				},
			},
		})
	case KindOptions:
		if strings.Contains(o.Query, ".") {
			return json.Marshal(map[string]any{
				"size": o.Size,
				"query": map[string]any{
					"bool": map[string]any{
						"filter": []any{map[string]any{"term": map[string]any{"type": "option"}}},
						"must": []any{map[string]any{"query_string": map[string]any{
							"query":            "option_name:" + o.Query + "*",
							"analyze_wildcard": true,
						}}},
					},
				},
			})
		}
		return json.Marshal(map[string]any{
			"size": o.Size,
			"query": map[string]any{
				"bool": map[string]any{
					"filter": []any{map[string]any{"term": map[string]any{"type": "option"}}},
					"must": []any{map[string]any{"multi_match": map[string]any{
						"query": o.Query,
						"fields": []string{
							"option_name^9",
							"option_description^3",
						},
					}}},
				},
			},
		})
	}
	return nil, fmt.Errorf("search nix: unknown kind %q", o.Kind)
}

type esResponse struct {
	Hits struct {
		Hits []struct {
			Source map[string]any `json:"_source"`
		} `json:"hits"`
	} `json:"hits"`
}

func parseHits(kind NixKind, body []byte) (NixResult, error) {
	var r esResponse
	if err := json.Unmarshal(body, &r); err != nil {
		return NixResult{}, fmt.Errorf("search nix: parse response: %w", err)
	}
	res := NixResult{Kind: kind}
	for _, h := range r.Hits.Hits {
		switch kind {
		case KindPackages:
			res.Packages = append(res.Packages, Package{
				AttrName:    str(h.Source["package_attr_name"]),
				Version:     str(h.Source["package_pversion"]),
				Description: cleanDesc(str(h.Source["package_description"])),
			})
		case KindOptions:
			res.Options = append(res.Options, Option{
				Name:        str(h.Source["option_name"]),
				Type:        str(h.Source["option_type"]),
				Description: cleanDesc(str(h.Source["option_description"])),
			})
		}
	}
	return res, nil
}

func str(v any) string {
	if s, ok := v.(string); ok {
		return s
	}
	return ""
}

func cleanDesc(s string) string {
	s = stripTags(s)
	s = strings.ReplaceAll(s, "\n", " ")
	for strings.Contains(s, "  ") {
		s = strings.ReplaceAll(s, "  ", " ")
	}
	s = strings.TrimSpace(s)
	if len(s) > 160 {
		s = s[:160]
	}
	return s
}

func stripTags(s string) string {
	var out strings.Builder
	out.Grow(len(s))
	inTag := false
	for _, r := range s {
		switch {
		case r == '<':
			inTag = true
		case r == '>':
			inTag = false
		case !inTag:
			out.WriteRune(r)
		}
	}
	return out.String()
}

// FormatText renders a NixResult as one TSV line per hit, matching the
// legacy harness/search-nix.sh output. Returns "no results\n" when
// empty.
func FormatText(r NixResult) string {
	var b strings.Builder
	switch r.Kind {
	case KindPackages:
		if len(r.Packages) == 0 {
			return "no results\n"
		}
		for _, p := range r.Packages {
			fmt.Fprintf(&b, "%s\t%s\t%s\n", p.AttrName, p.Version, p.Description)
		}
	case KindOptions:
		if len(r.Options) == 0 {
			return "no results\n"
		}
		for _, o := range r.Options {
			fmt.Fprintf(&b, "%s\t%s\t%s\n", o.Name, o.Type, o.Description)
		}
	}
	return b.String()
}

// FormatJSON renders a NixResult as a pretty-printed JSON array of hit
// objects. The shape matches Package or Option depending on Kind.
func FormatJSON(r NixResult) (string, error) {
	var v any
	switch r.Kind {
	case KindPackages:
		if r.Packages == nil {
			v = []Package{}
		} else {
			v = r.Packages
		}
	case KindOptions:
		if r.Options == nil {
			v = []Option{}
		} else {
			v = r.Options
		}
	default:
		return "", fmt.Errorf("search nix: unknown kind %q", r.Kind)
	}
	out, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		return "", err
	}
	return string(out) + "\n", nil
}
