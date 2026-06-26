// Package response implements a Go text/template-based response generation engine.
// A session may carry a script; at webhook-capture time Render executes the script
// against the incoming request and returns a Result that the caller applies as the
// HTTP response body (and optional status code).
//
// All helper functions are stdlib-only (text/template, crypto/hmac, crypto/rand, etc.)
// and the package has no non-stdlib dependencies.
package response

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"text/template"
	"time"
)

const (
	maxOutputBytes = 1 << 20 // 1 MiB cap on rendered output
	minHTTPStatus  = 100
	maxHTTPStatus  = 599
	statusPrefix   = "@status "
)

// Request carries the information about an incoming webhook request that is
// available inside a template script.
type Request struct {
	// Method is the HTTP method (e.g. "GET", "POST").
	Method string

	// Path is the request URL path.
	Path string

	// Slug is the human-readable session slug from the URL.
	Slug string

	// Body is the raw request body string.
	Body string

	// Query holds URL query parameters (first value per key).
	Query map[string]string

	// Header holds canonical HTTP header names mapped to their first value.
	Header map[string]string

	// Now is the time at which the request was captured.
	Now time.Time
}

// Result holds the rendered response produced by the template engine.
// Status == 0 means the caller should use the session's configured static code.
// Headers is reserved for future use; Task 8 applies session and security headers.
type Result struct {
	// Status is the HTTP status code parsed from a leading "@status NNN" directive,
	// or 0 if no such directive was found.
	Status int

	// Headers is currently always nil; header injection is handled by Task 8.
	Headers map[string]string

	// Body is the rendered template output, with the @status line stripped if present.
	Body []byte
}

// templateData is the dot value passed to template.Execute.
type templateData struct {
	Method string
	Path   string
	Slug   string
	Body   string
	Query  map[string]string
	Header map[string]string
	Now    time.Time
	// JSON is the request body parsed as any (map/slice/scalar),
	// or nil when the body is absent or not valid JSON.
	JSON any
}

// Render parses script as a Go text/template, executes it against req, and returns
// the rendered Result.
//
// Execution is bounded by both ctx and timeout (whichever fires first). Rendered
// output is capped at 1 MiB; exceeding the cap returns an error. Parse or execute
// errors are returned so the caller can fall back to the session's static response.
//
// Status directive: if the FIRST line of rendered output is exactly "@status NNN"
// (100 ≤ NNN ≤ 599), that line is parsed into Result.Status and stripped from Body.
// Otherwise Result.Status is 0.
func Render(ctx context.Context, script string, req Request, timeout time.Duration) (Result, error) {
	tmpl, err := template.New("response").Funcs(funcMap()).Parse(script)
	if err != nil {
		return Result{}, fmt.Errorf("template parse: %w", err)
	}

	data := buildData(req)

	execCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	type execResult struct {
		buf []byte
		err error
	}

	ch := make(chan execResult, 1)

	go func() {
		w := &limitedBuffer{ctx: execCtx, limit: maxOutputBytes}

		execErr := tmpl.Execute(w, data)
		ch <- execResult{buf: w.b.Bytes(), err: execErr}
	}()

	select {
	case <-execCtx.Done():
		return Result{}, fmt.Errorf("template execution: %w", execCtx.Err())

	case res := <-ch:
		if res.err != nil {
			return Result{}, fmt.Errorf("template execution: %w", res.err)
		}

		return parseResult(res.buf), nil
	}
}

// buildData converts a Request into the template dot value, parsing the body as JSON
// when possible.
func buildData(req Request) templateData {
	d := templateData{
		Method: req.Method,
		Path:   req.Path,
		Slug:   req.Slug,
		Body:   req.Body,
		Query:  req.Query,
		Header: req.Header,
		Now:    req.Now,
	}

	if req.Body != "" {
		var v any

		if err := json.Unmarshal([]byte(req.Body), &v); err == nil {
			d.JSON = v
		}
	}

	return d
}

// parseResult inspects rendered output for a leading @status directive.
func parseResult(out []byte) Result {
	s := string(out)

	firstNL := strings.IndexByte(s, '\n')

	var firstLine, rest string

	if firstNL >= 0 {
		firstLine = s[:firstNL]
		rest = s[firstNL+1:]
	} else {
		firstLine = s
	}

	if code, ok := parseStatusDirective(firstLine); ok {
		return Result{Status: code, Body: []byte(rest)}
	}

	return Result{Body: out}
}

// parseStatusDirective returns (code, true) when line is "@status NNN" with a valid
// HTTP status code (100–599).
func parseStatusDirective(line string) (int, bool) {
	if !strings.HasPrefix(line, statusPrefix) {
		return 0, false
	}

	numStr := line[len(statusPrefix):]

	code, err := strconv.Atoi(numStr)
	if err != nil {
		return 0, false
	}

	if code < minHTTPStatus || code > maxHTTPStatus {
		return 0, false
	}

	return code, true
}

// limitedBuffer is an io.Writer that checks ctx on every write and enforces a byte cap.
// It is only ever written to from a single goroutine so no mutex is required.
type limitedBuffer struct {
	ctx   context.Context
	b     bytes.Buffer
	limit int
}

// errOutputTooLarge is returned when rendered output exceeds maxOutputBytes.
var errOutputTooLarge = errors.New("template output exceeded 1 MiB limit")

// Write implements io.Writer.
func (w *limitedBuffer) Write(p []byte) (int, error) {
	if err := w.ctx.Err(); err != nil {
		return 0, err
	}

	if w.b.Len()+len(p) > w.limit {
		return 0, errOutputTooLarge
	}

	return w.b.Write(p)
}
