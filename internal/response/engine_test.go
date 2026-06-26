// Package response_test exercises the response template engine.
package response_test

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"gh.tarampamp.am/webhook-tester/v2/internal/response"
)

func TestRender_Echo(t *testing.T) {
	t.Parallel()

	got, err := response.Render(
		context.Background(),
		`{{ .JSON.trackingId }}`,
		response.Request{Body: `{"trackingId":"T1"}`},
		time.Second,
	)

	require.NoError(t, err)
	assert.Equal(t, "T1", string(got.Body))
	assert.Equal(t, 0, got.Status)
}

func TestRender_StatusDirective(t *testing.T) {
	t.Parallel()

	got, err := response.Render(
		context.Background(),
		"@status 201\n{\"ok\":true}",
		response.Request{},
		time.Second,
	)

	require.NoError(t, err)
	assert.Equal(t, 201, got.Status)
	assert.Equal(t, `{"ok":true}`, string(got.Body))
}

func TestRender_HmacSHA256_Deterministic(t *testing.T) {
	t.Parallel()

	const (
		key  = "secret"
		body = "hello world"
	)

	// Compute expected value in test using stdlib.
	mac := hmac.New(sha256.New, []byte(key))
	mac.Write([]byte(body))

	expected := hex.EncodeToString(mac.Sum(nil))

	got, err := response.Render(
		context.Background(),
		`{{ hmacSHA256 "secret" .Body }}`,
		response.Request{Body: body},
		time.Second,
	)

	require.NoError(t, err)
	assert.Equal(t, expected, string(got.Body))
	assert.Equal(t, 0, got.Status)
}

func TestRender_InvalidTemplate(t *testing.T) {
	t.Parallel()

	_, err := response.Render(
		context.Background(),
		`{{ invalid syntax`,
		response.Request{},
		time.Second,
	)

	require.Error(t, err)
	assert.True(t, strings.Contains(err.Error(), "template parse"), "error should mention parse: %v", err)
}

func TestRender_Timeout(t *testing.T) {
	t.Parallel()

	start := time.Now()

	_, err := response.Render(
		context.Background(),
		`{{ range seq 1000000000 }}.{{ end }}`,
		response.Request{},
		5*time.Millisecond,
	)

	elapsed := time.Since(start)

	require.Error(t, err, "expected a timeout error")
	assert.Less(t, elapsed, time.Second, "execution should abort well within 1 second, took %v", elapsed)
}

func TestRender_StatusDirective_InvalidCode_NotStripped(t *testing.T) {
	t.Parallel()

	// @status with an invalid code is treated as regular body text.
	got, err := response.Render(
		context.Background(),
		"@status 99\nfoo",
		response.Request{},
		time.Second,
	)

	require.NoError(t, err)
	assert.Equal(t, 0, got.Status)
	assert.Equal(t, "@status 99\nfoo", string(got.Body))
}

func TestRender_JSONPath(t *testing.T) {
	t.Parallel()

	got, err := response.Render(
		context.Background(),
		`{{ jsonPath .JSON "data.trackingId" }}`,
		response.Request{Body: `{"data":{"trackingId":"DEEP1"}}`},
		time.Second,
	)

	require.NoError(t, err)
	assert.Equal(t, "DEEP1", string(got.Body))
}

func TestRender_HelperFuncs_Smoke(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name   string
		script string
		check  func(t *testing.T, body string)
	}{
		{
			name:   "uuid length",
			script: `{{ uuid }}`,
			check: func(t *testing.T, body string) {
				t.Helper()
				assert.Len(t, body, 36)
			},
		},
		{
			name:   "base64",
			script: `{{ base64 "hello" }}`,
			check: func(t *testing.T, body string) {
				t.Helper()
				assert.Equal(t, "aGVsbG8=", body)
			},
		},
		{
			name:   "sha256",
			script: `{{ sha256 "abc" }}`,
			check: func(t *testing.T, body string) {
				t.Helper()
				// Compute the expected value using the stdlib so the test stays
				// correct regardless of platform and does not embed a hardcoded vector.
				h := sha256.Sum256([]byte("abc"))
				assert.Equal(t, hex.EncodeToString(h[:]), body)
			},
		},
		{
			name:   "upper",
			script: `{{ upper "hello" }}`,
			check: func(t *testing.T, body string) {
				t.Helper()
				assert.Equal(t, "HELLO", body)
			},
		},
		{
			name:   "lower",
			script: `{{ lower "HELLO" }}`,
			check: func(t *testing.T, body string) {
				t.Helper()
				assert.Equal(t, "hello", body)
			},
		},
		{
			name:   "default with nil JSON",
			script: `{{ default "fallback" .JSON }}`,
			check: func(t *testing.T, body string) {
				t.Helper()
				assert.Equal(t, "fallback", body)
			},
		},
		{
			name:   "randHex length",
			script: `{{ randHex 8 }}`,
			check: func(t *testing.T, body string) {
				t.Helper()
				assert.Len(t, body, 16) // 8 bytes → 16 hex chars
			},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			got, err := response.Render(context.Background(), tc.script, response.Request{}, time.Second)

			require.NoError(t, err)
			tc.check(t, string(got.Body))
		})
	}
}
