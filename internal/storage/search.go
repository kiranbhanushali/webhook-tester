package storage

import "strings"

// identifierMatches reports whether the given key matches q.Key (if set) and the value
// satisfies the q.Match condition against q.Value. It is shared by the local scanning
// drivers (inmemory, fs) and is reusable by the SQLite driver.
func identifierMatches(q IdentifierQuery, key, value string) bool {
	if q.Key != "" && key != q.Key {
		return false
	}

	switch q.Match {
	case IdentifierMatchPrefix:
		return strings.HasPrefix(value, q.Value)
	default: // IdentifierMatchExact or zero value
		return value == q.Value
	}
}
