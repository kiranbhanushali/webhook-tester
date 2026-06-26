package storage

import (
	"context"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

// TestSQLite_CascadeDeletesIdentifiers is a white-box test (package storage) that
// proves ON DELETE CASCADE actually removes request_identifiers rows when their
// parent request or session is deleted. It queries the table directly rather than
// via SearchRequests, whose INNER JOIN would hide an orphaned identifier even if
// foreign keys were off — so this is the real CASCADE proof.
func TestSQLite_CascadeDeletesIdentifiers(t *testing.T) {
	t.Parallel()

	var (
		ctx = context.Background()
		dsn = "file:" + filepath.Join(t.TempDir(), "cascade.db")
	)

	s, err := NewSQLite(ctx, dsn, time.Hour, 16, WithSQLiteExtractor(
		func(_ []byte, _ []HttpHeader, _ string) []Identifier {
			return []Identifier{{Key: "trackingId", Value: "ABC"}}
		},
	))
	require.NoError(t, err)

	t.Cleanup(func() { _ = s.Close() })

	countByRequest := func(reqID string) int {
		var n int

		require.NoError(t, s.db.QueryRowContext(ctx,
			`SELECT COUNT(*) FROM request_identifiers WHERE request_id = ?`, reqID).Scan(&n))

		return n
	}

	countBySession := func(sessID string) int {
		var n int

		require.NoError(t, s.db.QueryRowContext(ctx,
			`SELECT COUNT(*) FROM request_identifiers WHERE session_id = ?`, sessID).Scan(&n))

		return n
	}

	sID, err := s.NewSession(ctx, Session{Slug: "casc"})
	require.NoError(t, err)

	// deleting a request CASCADEs to its identifiers
	rID, err := s.NewRequest(ctx, sID, Request{Body: []byte(`{"trackingId":"ABC"}`)})
	require.NoError(t, err)
	require.Equal(t, 1, countByRequest(rID))

	require.NoError(t, s.DeleteRequest(ctx, sID, rID))
	require.Equal(t, 0, countByRequest(rID))

	// deleting a session CASCADEs through requests to their identifiers
	_, err = s.NewRequest(ctx, sID, Request{Body: []byte(`{"trackingId":"ABC"}`)})
	require.NoError(t, err)
	require.Equal(t, 1, countBySession(sID))

	require.NoError(t, s.DeleteSession(ctx, sID))
	require.Equal(t, 0, countBySession(sID))
}
