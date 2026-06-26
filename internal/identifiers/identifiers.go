// Package identifiers implements allowlist-based extraction of searchable key/value
// pairs from captured request bodies (JSON), headers, and query parameters.
package identifiers

import (
	"encoding/json"
	"fmt"
	"net/url"
	"strings"

	"gh.tarampamp.am/webhook-tester/v2/internal/storage"
)

const (
	defaultMaxNodes = 5000
	defaultMaxDepth = 64
)

// Extractor walks a request and produces a deduplicated list of storage.Identifier
// values for any allowlisted key found in the JSON body, HTTP headers, or (optionally)
// URL query parameters. Its Extract method is directly assignable to
// storage.IdentifierExtractor.
type Extractor struct {
	// Keys is the lower-cased set of JSON field names (and query param names when
	// ScanQuery is true) to extract.
	Keys map[string]struct{}

	// Headers is the lower-cased set of HTTP header names to extract.
	Headers map[string]struct{}

	// ScanQuery controls whether URL query parameters are scanned against Keys.
	ScanQuery bool

	// MaxNodes caps the total number of JSON nodes visited during body walking.
	// Defaults to 5000.
	MaxNodes int
}

// Compile-time guarantee that Extract satisfies the storage extractor seam, so a
// signature drift is caught by `go build` (not only by the test-side check). Task 10
// wires this via storage.WithSQLiteExtractor(extractor.Extract).
var _ storage.IdentifierExtractor = (&Extractor{}).Extract

// NewExtractor constructs an Extractor with the given allowlists.
// All entries in keys and headers are lower-cased before being stored so that
// comparison at extraction time is always case-insensitive.
// scanQuery enables URL query-parameter scanning (matched against Keys).
func NewExtractor(keys, headers []string, scanQuery bool) *Extractor {
	e := &Extractor{
		Keys:      make(map[string]struct{}, len(keys)),
		Headers:   make(map[string]struct{}, len(headers)),
		ScanQuery: scanQuery,
		MaxNodes:  defaultMaxNodes,
	}

	for _, k := range keys {
		e.Keys[strings.ToLower(k)] = struct{}{}
	}

	for _, h := range headers {
		e.Headers[strings.ToLower(h)] = struct{}{}
	}

	return e
}

// Extract implements storage.IdentifierExtractor. It extracts allowlisted identifiers
// from the JSON body, HTTP headers, and (if ScanQuery) query params. Malformed JSON
// bodies are silently skipped; headers and query params are always scanned.
func (e *Extractor) Extract(body []byte, headers []storage.HttpHeader, rawURL string) []storage.Identifier {
	seen := make(map[storage.Identifier]struct{})

	var results []storage.Identifier

	add := func(key, value string) {
		id := storage.Identifier{Key: key, Value: value}
		if _, exists := seen[id]; !exists {
			seen[id] = struct{}{}
			results = append(results, id)
		}
	}

	// Walk JSON body if non-empty.
	if len(body) > 0 && len(e.Keys) > 0 {
		var parsed any
		if err := json.Unmarshal(body, &parsed); err == nil {
			counter := 0
			walkJSON(parsed, e.Keys, &counter, e.MaxNodes, 0, defaultMaxDepth, add)
		}
		// Malformed JSON → silently skip body extraction.
	}

	// Scan HTTP headers.
	for _, h := range headers {
		lower := strings.ToLower(h.Name)
		if _, ok := e.Headers[lower]; ok {
			add(lower, h.Value)
		}
	}

	// Scan query parameters.
	if e.ScanQuery && rawURL != "" && len(e.Keys) > 0 {
		if u, err := url.Parse(rawURL); err == nil {
			for param, values := range u.Query() {
				lower := strings.ToLower(param)
				if _, ok := e.Keys[lower]; ok {
					for _, v := range values {
						add(lower, v)
					}
				}
			}
		}
	}

	return results
}

// walkJSON recursively visits a parsed JSON value and records any object field
// whose lower-cased key is in the allowlist, provided the value is a scalar
// (string, number, or bool — not null, object, or array).
func walkJSON(
	node any,
	keys map[string]struct{},
	counter *int,
	maxNodes int,
	depth, maxDepth int,
	add func(key, value string),
) {
	if *counter >= maxNodes || depth > maxDepth {
		return
	}

	*counter++

	switch v := node.(type) {
	case map[string]any:
		for k, child := range v {
			lower := strings.ToLower(k)
			if _, ok := keys[lower]; ok {
				// Only record scalar values (non-null, non-object, non-array).
				if s, ok := scalarString(child); ok {
					add(lower, s)
				}
				// Even if the value is an object/array we still recurse into it so
				// deeper keys can be found.
			}
			// Always descend regardless of whether the key matched.
			walkJSON(child, keys, counter, maxNodes, depth+1, maxDepth, add)
		}
	case []any:
		for _, item := range v {
			walkJSON(item, keys, counter, maxNodes, depth+1, maxDepth, add)
		}
	}
}

// scalarString converts a JSON scalar (string, float64, bool) to its string
// representation. It returns ("", false) for null (nil) and non-scalar types.
func scalarString(v any) (string, bool) {
	switch val := v.(type) {
	case string:
		return val, true
	case float64:
		// Render integers without a decimal point.
		if val == float64(int64(val)) {
			return fmt.Sprintf("%d", int64(val)), true
		}

		return fmt.Sprintf("%g", val), true

	case bool:
		if val {
			return "true", true
		}

		return "false", true
	default:
		// nil (JSON null) or object/array.
		return "", false
	}
}
