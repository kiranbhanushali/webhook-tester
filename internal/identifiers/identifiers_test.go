package identifiers_test

import (
	"fmt"
	"strings"
	"testing"

	"gh.tarampamp.am/webhook-tester/v2/internal/identifiers"
	"gh.tarampamp.am/webhook-tester/v2/internal/storage"
)

func TestExtract_NestedJSON(t *testing.T) {
	t.Parallel()

	e := identifiers.NewExtractor([]string{"trackingId", "referenceId"}, nil, false)

	body := []byte(`{"data":{"trackingId":"T1"},"items":[{"referenceId":"R1"}]}`)
	got := e.Extract(body, nil, "")

	want := []storage.Identifier{
		{Key: "trackingid", Value: "T1"},
		{Key: "referenceid", Value: "R1"},
	}

	if !identifiersEqual(got, want) {
		t.Errorf("nested JSON: got %v, want %v", got, want)
	}
}

func TestExtract_NumericValue(t *testing.T) {
	t.Parallel()

	e := identifiers.NewExtractor([]string{"trackingId"}, nil, false)

	body := []byte(`{"trackingId":123}`)
	got := e.Extract(body, nil, "")

	want := []storage.Identifier{
		{Key: "trackingid", Value: "123"},
	}

	if !identifiersEqual(got, want) {
		t.Errorf("numeric value: got %v, want %v", got, want)
	}
}

func TestExtract_BoolValue(t *testing.T) {
	t.Parallel()

	e := identifiers.NewExtractor([]string{"active"}, nil, false)

	body := []byte(`{"active":true}`)
	got := e.Extract(body, nil, "")

	want := []storage.Identifier{
		{Key: "active", Value: "true"},
	}

	if !identifiersEqual(got, want) {
		t.Errorf("bool value: got %v, want %v", got, want)
	}
}

func TestExtract_HeaderAllowlist(t *testing.T) {
	t.Parallel()

	e := identifiers.NewExtractor(nil, []string{"X-Tracking-Id"}, false)

	headers := []storage.HttpHeader{
		{Name: "X-Tracking-Id", Value: "H1"},
		{Name: "Content-Type", Value: "application/json"},
	}
	got := e.Extract(nil, headers, "")

	want := []storage.Identifier{
		{Key: "x-tracking-id", Value: "H1"},
	}

	if !identifiersEqual(got, want) {
		t.Errorf("header allowlist: got %v, want %v", got, want)
	}
}

func TestExtract_QueryParam(t *testing.T) {
	t.Parallel()

	e := identifiers.NewExtractor([]string{"referenceId"}, nil, true)

	got := e.Extract(nil, nil, "https://example.com/hook?referenceId=Q1&other=x")

	want := []storage.Identifier{
		{Key: "referenceid", Value: "Q1"},
	}

	if !identifiersEqual(got, want) {
		t.Errorf("query param: got %v, want %v", got, want)
	}
}

func TestExtract_NonJSONBody(t *testing.T) {
	t.Parallel()

	e := identifiers.NewExtractor([]string{"trackingId"}, []string{"X-Tracking-Id"}, true)

	headers := []storage.HttpHeader{
		{Name: "X-Tracking-Id", Value: "H1"},
	}
	got := e.Extract([]byte("not json at all"), headers, "https://example.com/hook?trackingId=Q1")

	want := []storage.Identifier{
		{Key: "x-tracking-id", Value: "H1"},
		{Key: "trackingid", Value: "Q1"},
	}

	if !identifiersEqual(got, want) {
		t.Errorf("non-JSON body: got %v, want %v", got, want)
	}
}

func TestExtract_Dedup(t *testing.T) {
	t.Parallel()

	e := identifiers.NewExtractor([]string{"trackingId"}, nil, false)

	// Same key/value appears twice in body
	body := []byte(`[{"trackingId":"T1"},{"trackingId":"T1"}]`)
	got := e.Extract(body, nil, "")

	want := []storage.Identifier{
		{Key: "trackingid", Value: "T1"},
	}

	if !identifiersEqual(got, want) {
		t.Errorf("dedup: got %v, want %v", got, want)
	}
}

func TestExtract_MatchedKeyIsObject(t *testing.T) {
	t.Parallel()

	e := identifiers.NewExtractor([]string{"data"}, nil, false)

	body := []byte(`{"data":{"nested":"value"}}`)
	got := e.Extract(body, nil, "")

	if len(got) != 0 {
		t.Errorf("matched key is object: expected no results, got %v", got)
	}
}

func TestExtract_MatchedKeyIsArray(t *testing.T) {
	t.Parallel()

	e := identifiers.NewExtractor([]string{"items"}, nil, false)

	body := []byte(`{"items":[1,2,3]}`)
	got := e.Extract(body, nil, "")

	if len(got) != 0 {
		t.Errorf("matched key is array: expected no results, got %v", got)
	}
}

func TestExtract_NullValue(t *testing.T) {
	t.Parallel()

	e := identifiers.NewExtractor([]string{"trackingId"}, nil, false)

	body := []byte(`{"trackingId":null}`)
	got := e.Extract(body, nil, "")

	if len(got) != 0 {
		t.Errorf("null value: expected no results, got %v", got)
	}
}

func TestExtract_ScanQueryFalse(t *testing.T) {
	t.Parallel()

	e := identifiers.NewExtractor([]string{"referenceId"}, nil, false)

	got := e.Extract(nil, nil, "https://example.com/hook?referenceId=Q1")

	if len(got) != 0 {
		t.Errorf("ScanQuery=false: expected no results, got %v", got)
	}
}

func TestExtract_CaseInsensitiveKey(t *testing.T) {
	t.Parallel()

	// allowlist uses mixed case; JSON key uses mixed case → should match and emit lower-case key
	e := identifiers.NewExtractor([]string{"TrackingID"}, nil, false)

	body := []byte(`{"trackingID":"X1"}`)
	got := e.Extract(body, nil, "")

	want := []storage.Identifier{
		{Key: "trackingid", Value: "X1"},
	}

	if !identifiersEqual(got, want) {
		t.Errorf("case-insensitive key: got %v, want %v", got, want)
	}
}

func TestExtract_EmptyAllowlists(t *testing.T) {
	t.Parallel()

	e := identifiers.NewExtractor(nil, nil, false)

	body := []byte(`{"trackingId":"T1"}`)
	headers := []storage.HttpHeader{{Name: "X-Tracking-Id", Value: "H1"}}
	got := e.Extract(body, headers, "https://example.com?trackingId=Q1")

	if len(got) != 0 {
		t.Errorf("empty allowlists: expected no results, got %v", got)
	}
}

func TestExtract_MethodValueAssignable(t *testing.T) {
	t.Parallel()

	// Verify that e.Extract is directly assignable to storage.IdentifierExtractor
	e := identifiers.NewExtractor([]string{"trackingId"}, nil, false)

	var _ storage.IdentifierExtractor = e.Extract
}

func TestExtract_MaxNodesCutoff(t *testing.T) {
	t.Parallel()

	e := identifiers.NewExtractor([]string{"trackingId"}, nil, false)
	// Force an aggressively small node budget so traversal stops early.
	e.MaxNodes = 3

	// A long array of matched-key objects. The walker visits: the array node, then
	// the first object + its scalar value (exhausting the 3-node budget) before any
	// later element is reached. So "early" is recorded but "late" is not.
	var sb strings.Builder

	sb.WriteString(`[`)

	const n = 100

	for i := 0; i < n; i++ {
		if i > 0 {
			sb.WriteString(`,`)
		}

		switch i {
		case 0:
			sb.WriteString(`{"trackingId":"early"}`)
		case n - 1:
			sb.WriteString(`{"trackingId":"late"}`)
		default:
			fmt.Fprintf(&sb, `{"trackingId":"v%d"}`, i)
		}
	}

	sb.WriteString(`]`)

	got := e.Extract([]byte(sb.String()), nil, "")

	if !containsIdentifier(got, storage.Identifier{Key: "trackingid", Value: "early"}) {
		t.Errorf("MaxNodes cutoff: expected early identifier to be present, got %v", got)
	}

	if containsIdentifier(got, storage.Identifier{Key: "trackingid", Value: "late"}) {
		t.Errorf("MaxNodes cutoff: late identifier (beyond node budget) should NOT be present, got %v", got)
	}

	if len(got) >= n {
		t.Errorf("MaxNodes cutoff: expected traversal to stop early, got %d identifiers", len(got))
	}
}

func TestExtract_DepthCap(t *testing.T) {
	t.Parallel()

	// The implementation caps recursion depth at 64. A matched key nested within the
	// cap is recorded; the same key nested beyond the cap is not.
	e := identifiers.NewExtractor([]string{"trackingId"}, nil, false)

	// Shallow: the matched-key object sits at nesting depth 10 (well within the cap).
	shallow := e.Extract(nestedJSON(10, "trackingId", "SHALLOW"), nil, "")
	if !containsIdentifier(shallow, storage.Identifier{Key: "trackingid", Value: "SHALLOW"}) {
		t.Errorf("depth cap: identifier within cap should be present, got %v", shallow)
	}

	// Deep: the matched-key object sits at nesting depth 70 (beyond the 64 cap).
	deep := e.Extract(nestedJSON(70, "trackingId", "DEEP"), nil, "")
	if containsIdentifier(deep, storage.Identifier{Key: "trackingid", Value: "DEEP"}) {
		t.Errorf("depth cap: identifier beyond cap should NOT be present, got %v", deep)
	}
}

// nestedJSON builds an object containing {key: value} wrapped in `depth` levels of
// {"wrap": ...}, so the matched-key object sits at JSON nesting depth `depth`.
func nestedJSON(depth int, key, value string) []byte {
	inner := fmt.Sprintf(`{%q:%q}`, key, value)
	for i := 0; i < depth; i++ {
		inner = fmt.Sprintf(`{"wrap":%s}`, inner)
	}

	return []byte(inner)
}

// containsIdentifier reports whether want appears in got.
func containsIdentifier(got []storage.Identifier, want storage.Identifier) bool {
	for _, id := range got {
		if id == want {
			return true
		}
	}

	return false
}

// identifiersEqual checks that got and want contain the same elements (order-independent).
func identifiersEqual(got, want []storage.Identifier) bool {
	if len(got) != len(want) {
		return false
	}

	counts := make(map[storage.Identifier]int, len(want))

	for _, id := range want {
		counts[id]++
	}

	for _, id := range got {
		counts[id]--

		if counts[id] < 0 {
			return false
		}
	}

	return true
}
