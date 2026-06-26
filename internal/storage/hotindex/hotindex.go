// Package hotindex provides an in-memory "hot index" that maps identifier
// key/value pairs to the set of requests that carried them.  It retains only
// the most-recent window of captures (default: 7 days) and is designed for
// O(1) exact lookups and O(n) prefix lookups over the live entry set.
//
// Normalization contract (must mirror the SQLite driver):
//   - Keys are lower-cased with strings.ToLower before storage and lookup.
//   - Values are stored and compared as-is (case-sensitive, no wildcards).
//
// Internal map key layout: lower(key) + "\x00" + value
//
// Rebuild contract: the map passed to Rebuild must use the same composite key
// format — lower(key)+"\x00"+value — so callers (e.g. a warm-up path that
// reads from SQLite) must normalize keys themselves before calling Rebuild.
package hotindex

import (
	"sort"
	"strings"
	"sync"
	"time"

	"gh.tarampamp.am/webhook-tester/v2/internal/storage"
)

// Ref is a lightweight pointer to a captured request.
type Ref struct {
	SessionID           string
	SessionSlug         string
	RequestID           string
	CapturedAtUnixMilli int64
}

// HotIndex is a concurrent in-memory index of identifier → []Ref entries,
// bounded by a configurable retention window.
type HotIndex struct {
	mu     sync.RWMutex
	index  map[string][]Ref // composite key → refs (unsorted; sorted at read time)
	window time.Duration
}

// New returns a new HotIndex that retains refs captured within the given window.
func New(window time.Duration) *HotIndex {
	return &HotIndex{
		index:  make(map[string][]Ref),
		window: window,
	}
}

// compositeKey builds the internal map key from an already-normalized (lower-case) key
// and a value.
func compositeKey(normKey, value string) string { return normKey + "\x00" + value }

// Add records ref under the given key/value pair.
// The key is lower-cased; the value is stored as-is.
func (h *HotIndex) Add(key, value string, ref Ref) {
	k := compositeKey(strings.ToLower(key), value)

	h.mu.Lock()
	h.index[k] = append(h.index[k], ref)
	h.mu.Unlock()
}

// Lookup returns all refs for the given key/value pair, sorted newest-first.
// For IdentifierMatchExact the lookup is O(1); for IdentifierMatchPrefix the
// entire map is iterated (O(n)) and value-prefix matching is case-sensitive
// and wildcard-free (consistent with the SQLite driver's substr check).
// Returns nil when there are no matches.
func (h *HotIndex) Lookup(key, value string, match storage.IdentifierMatch) []Ref {
	normKey := strings.ToLower(key)

	h.mu.RLock()
	defer h.mu.RUnlock()

	var results []Ref

	switch match {
	case storage.IdentifierMatchExact:
		k := compositeKey(normKey, value)
		if refs, ok := h.index[k]; ok {
			results = make([]Ref, len(refs))
			copy(results, refs)
		}

	case storage.IdentifierMatchPrefix:
		prefix := normKey + "\x00"
		for k, refs := range h.index {
			if !strings.HasPrefix(k, prefix) {
				continue
			}
			// Extract the stored value (everything after key+NUL).
			storedValue := k[len(prefix):]
			if strings.HasPrefix(storedValue, value) {
				results = append(results, refs...)
			}
		}
	}

	if len(results) == 0 {
		return nil
	}

	sort.Slice(results, func(i, j int) bool {
		return results[i].CapturedAtUnixMilli > results[j].CapturedAtUnixMilli
	})

	return results
}

// Evict removes all refs whose CapturedAtUnixMilli is strictly less than
// now minus the configured window.  Composite keys whose ref slice becomes
// empty are deleted from the map.
func (h *HotIndex) Evict(now time.Time) {
	cutoff := now.Add(-h.window).UnixMilli()

	h.mu.Lock()
	defer h.mu.Unlock()

	for k, refs := range h.index {
		kept := refs[:0]
		for _, r := range refs {
			if r.CapturedAtUnixMilli >= cutoff {
				kept = append(kept, r)
			}
		}

		if len(kept) == 0 {
			delete(h.index, k)
		} else {
			h.index[k] = kept
		}
	}
}

// Rebuild replaces the entire index with the provided map.  This is intended
// for startup warm-up from a durable store.
//
// CONTRACT: The keys in refs must already be in the normalized composite
// format lower(key)+"\x00"+value.  Rebuild does not re-normalize them.
func (h *HotIndex) Rebuild(refs map[string][]Ref) {
	newIndex := make(map[string][]Ref, len(refs))
	for k, v := range refs {
		cp := make([]Ref, len(v))
		copy(cp, v)
		newIndex[k] = cp
	}

	h.mu.Lock()
	h.index = newIndex
	h.mu.Unlock()
}
