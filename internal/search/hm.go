package search

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"
)

// MAINTENANCE - read if HM starts returning empty / 404:
//
//  1. Release gone. Available releases come from the dropdown at
//     https://home-manager-options.extranix.com (release-25.11,
//     release-25.05, master, ...). If the URL 404s, switch to a current
//     one.
//  2. URL moved. The data path is hard-coded in
//     https://home-manager-options.extranix.com/js/script.js
//     Grep it for "options-" to see the current path.

const (
	HMDefaultRelease  = "release-25.11"
	HMDefaultSize     = 10
	HMDefaultCacheTTL = 24 * time.Hour
)

var (
	HMBaseURL = "https://home-manager-options.extranix.com"

	hmHTTPTimeout = 60 * time.Second
)

// HMRequest configures a home-manager option search.
type HMRequest struct {
	Query    string
	Release  string
	Size     int
	Refresh  bool
	CacheDir string
	CacheTTL time.Duration
}

// HMOption is a single option hit from the home-manager JSON dump.
type HMOption struct {
	Name        string `json:"name"`
	Type        string `json:"type"`
	Description string `json:"description"`
}

// HMResult holds the matching home-manager options.
type HMResult struct {
	Options []HMOption
}

// HM searches the home-manager option JSON dump that powers
// https://home-manager-options.extranix.com. The dump is ~2.5 MB so
// HM caches it on disk under CacheDir for CacheTTL.
func HM(ctx context.Context, o HMRequest) (HMResult, error) {
	if strings.TrimSpace(o.Query) == "" {
		return HMResult{}, errors.New("search home-manager: query is empty")
	}
	if o.Release == "" {
		o.Release = HMDefaultRelease
	}
	if o.Size <= 0 {
		o.Size = HMDefaultSize
	}
	if o.CacheTTL <= 0 {
		o.CacheTTL = HMDefaultCacheTTL
	}
	if o.CacheDir == "" {
		base := os.Getenv("TMPDIR")
		if base == "" {
			base = "/tmp"
		}
		o.CacheDir = filepath.Join(base, "spore-search")
	}

	body, err := hmFetch(ctx, o)
	if err != nil {
		return HMResult{}, err
	}

	return hmFilter(body, o.Query, o.Size)
}

func hmFetch(ctx context.Context, o HMRequest) ([]byte, error) {
	if err := os.MkdirAll(o.CacheDir, 0o755); err != nil {
		return nil, fmt.Errorf("search home-manager: cache: %w", err)
	}
	cache := filepath.Join(o.CacheDir, "hm-options-"+o.Release+".json")

	if !o.Refresh {
		if info, err := os.Stat(cache); err == nil && info.Size() > 0 {
			if time.Since(info.ModTime()) < o.CacheTTL {
				return os.ReadFile(cache)
			}
		}
	}

	url := strings.TrimRight(HMBaseURL, "/") + "/data/options-" + o.Release + ".json"
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, err
	}
	client := &http.Client{Timeout: hmHTTPTimeout}
	resp, err := client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("search home-manager: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode/100 != 2 {
		return nil, fmt.Errorf("search home-manager: %s: %s", url, resp.Status)
	}

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("search home-manager: read body: %w", err)
	}

	tmp := cache + ".tmp"
	if err := os.WriteFile(tmp, body, 0o644); err != nil {
		return nil, fmt.Errorf("search home-manager: cache write: %w", err)
	}
	if err := os.Rename(tmp, cache); err != nil {
		return nil, fmt.Errorf("search home-manager: cache rename: %w", err)
	}
	return body, nil
}

type hmDump struct {
	Options []hmRawOption `json:"options"`
}

type hmRawOption struct {
	Title       string `json:"title"`
	Type        string `json:"type"`
	Description string `json:"description"`
}

func hmFilter(body []byte, query string, size int) (HMResult, error) {
	var dump hmDump
	if err := json.Unmarshal(body, &dump); err != nil {
		return HMResult{}, fmt.Errorf("search home-manager: parse: %w", err)
	}
	re, err := regexp.Compile("(?i)" + regexp.QuoteMeta(query))
	if err != nil {
		return HMResult{}, fmt.Errorf("search home-manager: query: %w", err)
	}
	res := HMResult{}
	for _, raw := range dump.Options {
		if !re.MatchString(raw.Title) && !re.MatchString(raw.Description) {
			continue
		}
		res.Options = append(res.Options, HMOption{
			Name:        raw.Title,
			Type:        raw.Type,
			Description: hmCleanDesc(raw.Description),
		})
		if len(res.Options) >= size {
			break
		}
	}
	return res, nil
}

func hmCleanDesc(s string) string {
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

// FormatHMText renders an HMResult as TSV, one line per hit, matching
// the legacy harness/search-hm.sh output. Returns "no results\n" when
// empty.
func FormatHMText(r HMResult) string {
	if len(r.Options) == 0 {
		return "no results\n"
	}
	var b strings.Builder
	for _, o := range r.Options {
		fmt.Fprintf(&b, "%s\t%s\t%s\n", o.Name, o.Type, o.Description)
	}
	return b.String()
}

// FormatHMJSON renders an HMResult as a pretty-printed JSON array.
func FormatHMJSON(r HMResult) (string, error) {
	v := r.Options
	if v == nil {
		v = []HMOption{}
	}
	out, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		return "", err
	}
	return string(out) + "\n", nil
}
