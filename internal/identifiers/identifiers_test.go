package identifiers_test

import (
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
