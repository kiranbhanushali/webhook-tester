package response

import (
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"math/big"
	"reflect"
	"strings"
	"text/template"
	"time"
)

// funcMap returns the template.FuncMap used by the response engine.
//
// Available template functions:
//
//   - json val              – marshal val to a JSON string (empty string on error).
//   - jsonPath val path     – dotted-path lookup into val; supports map[string]any and []any traversal.
//     E.g. {{ jsonPath .JSON "data.items.0.id" }}.
//   - uuid                  – generate a crypto/rand UUID v4.
//   - now [layout]          – current time formatted with layout (default: time.RFC3339).
//   - randInt min max       – crypto-random integer in [min, max).
//   - randHex n             – n random bytes encoded as a hex string (2n hex characters).
//   - base64 s              – base64 standard-encode s.
//   - sha256 s              – hex-encoded SHA-256 of s.
//   - hmacSHA256 key msg    – hex-encoded HMAC-SHA256 of msg signed with key.
//   - upper s               – strings.ToUpper.
//   - lower s               – strings.ToLower.
//   - default def val       – return val if non-zero/non-nil, else def.
//   - seq n                 – return a []struct{} of length n for use in range loops.
func funcMap() template.FuncMap {
	return template.FuncMap{
		"json":       tmplJSON,
		"jsonPath":   tmplJSONPath,
		"uuid":       tmplUUID,
		"now":        tmplNow,
		"randInt":    tmplRandInt,
		"randHex":    tmplRandHex,
		"base64":     tmplBase64,
		"sha256":     tmplSHA256,
		"hmacSHA256": tmplHMACSHA256,
		"upper":      strings.ToUpper,
		"lower":      strings.ToLower,
		"default":    tmplDefault,
		"seq":        tmplSeq,
	}
}

// tmplJSON marshals v to a JSON string; returns empty string on error.
func tmplJSON(v any) string {
	b, err := json.Marshal(v)
	if err != nil {
		return ""
	}

	return string(b)
}

// tmplJSONPath performs a dotted-path lookup into v (typically .JSON).
// Each segment navigates into a map[string]any by key or into a []any by numeric index.
// Returns nil if any segment is missing or the type is not traversable.
func tmplJSONPath(v any, path string) any {
	parts := strings.Split(path, ".")
	cur := v

	for _, part := range parts {
		if part == "" {
			continue
		}

		switch c := cur.(type) {
		case map[string]any:
			val, ok := c[part]
			if !ok {
				return nil
			}

			cur = val

		case []any:
			var idx int
			if _, err := fmt.Sscanf(part, "%d", &idx); err != nil || idx < 0 || idx >= len(c) {
				return nil
			}

			cur = c[idx]

		default:
			return nil
		}
	}

	return cur
}

const (
	uuidByteLen     = 16
	uuidVersionMask = 0x0f
	uuidVersionBits = 0x40 // version 4
	uuidVariantMask = 0x3f
	uuidVariantBits = 0x80 // RFC 4122 variant
)

// tmplUUID generates a random UUID v4 string using crypto/rand.
func tmplUUID() (string, error) {
	var b [uuidByteLen]byte

	if _, err := rand.Read(b[:]); err != nil {
		return "", err
	}

	b[6] = (b[6] & uuidVersionMask) | uuidVersionBits
	b[8] = (b[8] & uuidVariantMask) | uuidVariantBits

	return fmt.Sprintf(
		"%08x-%04x-%04x-%04x-%012x",
		b[0:4], b[4:6], b[6:8], b[8:10], b[10:16],
	), nil
}

// tmplNow returns the current time formatted with the given layout.
// If no layout is provided, time.RFC3339 is used.
func tmplNow(layout ...string) string {
	l := time.RFC3339
	if len(layout) > 0 && layout[0] != "" {
		l = layout[0]
	}

	return time.Now().Format(l)
}

// tmplRandInt returns a cryptographically random integer in [min, max).
func tmplRandInt(min, max int) (int, error) {
	if max <= min {
		return 0, fmt.Errorf("randInt: max (%d) must be greater than min (%d)", max, min)
	}

	n, err := rand.Int(rand.Reader, big.NewInt(int64(max-min)))
	if err != nil {
		return 0, err
	}

	return min + int(n.Int64()), nil
}

// tmplRandHex returns n random bytes hex-encoded as a 2n-character string.
func tmplRandHex(n int) (string, error) {
	if n < 0 {
		return "", errors.New("randHex: n must be non-negative")
	}

	b := make([]byte, n)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}

	return hex.EncodeToString(b), nil
}

// tmplBase64 returns the base64 standard encoding of s.
func tmplBase64(s string) string {
	return base64.StdEncoding.EncodeToString([]byte(s))
}

// tmplSHA256 returns the hex-encoded SHA-256 digest of s.
func tmplSHA256(s string) string {
	h := sha256.Sum256([]byte(s))

	return hex.EncodeToString(h[:])
}

// tmplHMACSHA256 returns the hex-encoded HMAC-SHA256 of message signed with key.
func tmplHMACSHA256(key, message string) string {
	mac := hmac.New(sha256.New, []byte(key))
	mac.Write([]byte(message))

	return hex.EncodeToString(mac.Sum(nil))
}

// tmplDefault returns val if it is non-nil and non-zero, otherwise def.
func tmplDefault(def, val any) any {
	if val == nil {
		return def
	}

	if reflect.ValueOf(val).IsZero() {
		return def
	}

	return val
}

// tmplSeq returns a []struct{} of length n for use in range loops.
// Since struct{} has zero size, even large n allocates negligible memory.
func tmplSeq(n int) []struct{} {
	return make([]struct{}, n)
}
