package hotindex_test

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"gh.tarampamp.am/webhook-tester/v2/internal/storage"
	"gh.tarampamp.am/webhook-tester/v2/internal/storage/hotindex"
)

func TestHotIndex_ExactLookup_NewestFirst(t *testing.T) {
	t.Parallel()

	h := hotindex.New(7 * 24 * time.Hour)

	older := hotindex.Ref{SessionID: "s1", SessionSlug: "slug1", RequestID: "r1", CapturedAtUnixMilli: 1000}
	newer := hotindex.Ref{SessionID: "s2", SessionSlug: "slug2", RequestID: "r2", CapturedAtUnixMilli: 2000}

	// Add older first, then newer — lookup must return newest-first regardless of insertion order.
	h.Add("trackingId", "ABC", older)
	h.Add("trackingId", "ABC", newer)

	// Key is case-insensitively normalized: "trackingId" → "trackingid"
	got := h.Lookup("trackingId", "ABC", storage.IdentifierMatchExact)
	require.Len(t, got, 2)
	assert.Equal(t, newer, got[0], "newest ref must come first")
	assert.Equal(t, older, got[1])
}

func TestHotIndex_Evict_RemovesOldRefs(t *testing.T) {
	t.Parallel()

	window := 7 * 24 * time.Hour
	h := hotindex.New(window)

	now := time.UnixMilli(10_000_000)
	cutoff := now.Add(-window)

	old := hotindex.Ref{SessionID: "s1", RequestID: "r1", CapturedAtUnixMilli: cutoff.UnixMilli() - 1} // just before cutoff — must be evicted
	fresh := hotindex.Ref{SessionID: "s2", RequestID: "r2", CapturedAtUnixMilli: cutoff.UnixMilli()}   // exactly at cutoff — kept

	h.Add("trackingid", "ABC", old)
	h.Add("trackingid", "ABC", fresh)

	h.Evict(now)

	got := h.Lookup("trackingid", "ABC", storage.IdentifierMatchExact)
	require.Len(t, got, 1)
	assert.Equal(t, fresh, got[0])
}

func TestHotIndex_Evict_DeletesEmptyKeys(t *testing.T) {
	t.Parallel()

	window := 24 * time.Hour
	h := hotindex.New(window)

	// now is ~115 days after epoch; cutoff is ~114 days — far beyond CapturedAtUnixMilli=1.
	now := time.UnixMilli(10_000_000_000)

	h.Add("k", "v", hotindex.Ref{SessionID: "s1", RequestID: "r1", CapturedAtUnixMilli: 1})

	h.Evict(now)

	// All refs removed — key should be gone; lookup returns nil/empty.
	got := h.Lookup("k", "v", storage.IdentifierMatchExact)
	assert.Empty(t, got, "evicted key must return no results")
}

func TestHotIndex_PrefixLookup(t *testing.T) {
	t.Parallel()

	h := hotindex.New(7 * 24 * time.Hour)

	refABC := hotindex.Ref{SessionID: "s1", RequestID: "r1", CapturedAtUnixMilli: 100}
	refXYZ := hotindex.Ref{SessionID: "s2", RequestID: "r2", CapturedAtUnixMilli: 200}
	refWild := hotindex.Ref{SessionID: "s3", RequestID: "r3", CapturedAtUnixMilli: 300}

	h.Add("trackingid", "ABC", refABC)
	h.Add("trackingid", "XYZ", refXYZ)
	h.Add("trackingid", "AB_wildcard", refWild) // underscore is NOT a wildcard

	// Prefix "AB" should match "ABC" and "AB_wildcard" but NOT "XYZ"
	got := h.Lookup("trackingid", "AB", storage.IdentifierMatchPrefix)
	require.Len(t, got, 2, "prefix 'AB' must match 'ABC' and 'AB_wildcard'")

	// Must NOT contain XYZ
	for _, r := range got {
		assert.NotEqual(t, refXYZ, r, "XYZ must not appear in prefix 'AB' results")
	}
}

func TestHotIndex_PrefixLookup_UnderscoreIsLiteral(t *testing.T) {
	t.Parallel()

	h := hotindex.New(7 * 24 * time.Hour)

	refABC := hotindex.Ref{SessionID: "s1", RequestID: "r1", CapturedAtUnixMilli: 100}
	h.Add("k", "ABC", refABC)

	// Query prefix "A_C" — underscore is NOT a wildcard; "ABC" must NOT match
	got := h.Lookup("k", "A_C", storage.IdentifierMatchPrefix)
	assert.Empty(t, got, "underscore must not act as a wildcard")
}

func TestHotIndex_CaseSensitiveValue(t *testing.T) {
	t.Parallel()

	h := hotindex.New(7 * 24 * time.Hour)

	h.Add("key", "ABC", hotindex.Ref{SessionID: "s1", RequestID: "r1", CapturedAtUnixMilli: 100})
	h.Add("key", "abc", hotindex.Ref{SessionID: "s2", RequestID: "r2", CapturedAtUnixMilli: 200})

	// Exact lookup for "ABC" must not return "abc"
	got := h.Lookup("key", "ABC", storage.IdentifierMatchExact)
	require.Len(t, got, 1)
	assert.Equal(t, "s1", got[0].SessionID)

	// Exact lookup for "abc" must not return "ABC"
	got2 := h.Lookup("key", "abc", storage.IdentifierMatchExact)
	require.Len(t, got2, 1)
	assert.Equal(t, "s2", got2[0].SessionID)
}

func TestHotIndex_EmptyResult(t *testing.T) {
	t.Parallel()

	h := hotindex.New(7 * 24 * time.Hour)

	// No data added — must return nil/empty (not panic, not error)
	got := h.Lookup("nonexistent", "value", storage.IdentifierMatchExact)
	assert.Empty(t, got)

	got2 := h.Lookup("nonexistent", "val", storage.IdentifierMatchPrefix)
	assert.Empty(t, got2)
}

func TestHotIndex_Rebuild(t *testing.T) {
	t.Parallel()

	h := hotindex.New(7 * 24 * time.Hour)

	// Pre-populate with some data.
	h.Add("old", "val", hotindex.Ref{SessionID: "old-s", RequestID: "old-r", CapturedAtUnixMilli: 1})

	// Rebuild with a new map — keys are already normalized composite "key\x00value".
	refs := map[string][]hotindex.Ref{
		"newkey\x00V1": {
			{SessionID: "s1", RequestID: "r1", CapturedAtUnixMilli: 500},
			{SessionID: "s2", RequestID: "r2", CapturedAtUnixMilli: 1000},
		},
	}
	h.Rebuild(refs)

	// Old data gone.
	old := h.Lookup("old", "val", storage.IdentifierMatchExact)
	assert.Empty(t, old, "Rebuild must replace all contents")

	// New data present and newest-first.
	got := h.Lookup("newkey", "V1", storage.IdentifierMatchExact)
	require.Len(t, got, 2)
	assert.Equal(t, int64(1000), got[0].CapturedAtUnixMilli, "newest first after Rebuild")
}

func TestHotIndex_KeyNormalization(t *testing.T) {
	t.Parallel()

	h := hotindex.New(7 * 24 * time.Hour)

	ref := hotindex.Ref{SessionID: "s1", RequestID: "r1", CapturedAtUnixMilli: 100}
	h.Add("TrackingID", "Value", ref) // mixed case key

	// Lookup with different key casing — must find the same ref.
	got1 := h.Lookup("trackingid", "Value", storage.IdentifierMatchExact)
	got2 := h.Lookup("TRACKINGID", "Value", storage.IdentifierMatchExact)
	got3 := h.Lookup("TrackingID", "Value", storage.IdentifierMatchExact)

	require.Len(t, got1, 1)
	require.Len(t, got2, 1)
	require.Len(t, got3, 1)
	assert.Equal(t, ref, got1[0])
	assert.Equal(t, ref, got2[0])
	assert.Equal(t, ref, got3[0])
}
