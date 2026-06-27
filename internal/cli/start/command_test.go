package start

import (
	"context"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"github.com/urfave/cli/v3"
	"go.uber.org/zap"

	"gh.tarampamp.am/webhook-tester/v2/internal/identifiers"
	"gh.tarampamp.am/webhook-tester/v2/internal/storage"
	"gh.tarampamp.am/webhook-tester/v2/internal/storage/hotindex"
)

// findFlag locates a flag on the start command by one of its names.
func findFlag(t *testing.T, cmd *cli.Command, name string) cli.Flag {
	t.Helper()

	for _, f := range cmd.Flags {
		for _, n := range f.Names() {
			if n == name {
				return f
			}
		}
	}

	t.Fatalf("flag %q not found on start command", name)

	return nil
}

func TestNewCommand_Defaults(t *testing.T) {
	t.Parallel()

	cmd := NewCommand(zap.NewNop(), 8080)

	t.Run("storage-driver defaults to sqlite", func(t *testing.T) {
		f, ok := findFlag(t, cmd, "storage-driver").(*cli.StringFlag)
		require.True(t, ok)
		require.Equal(t, "sqlite", f.Value)
	})

	t.Run("identifier-keys default", func(t *testing.T) {
		f, ok := findFlag(t, cmd, "identifier-keys").(*cli.StringSliceFlag)
		require.True(t, ok)
		require.Equal(t, []string{defaultIdentifierKeyTracking, defaultIdentifierKeyReference}, f.Value)
	})

	t.Run("identifier-headers default empty", func(t *testing.T) {
		f, ok := findFlag(t, cmd, "identifier-headers").(*cli.StringSliceFlag)
		require.True(t, ok)
		require.Empty(t, f.Value)
	})

	t.Run("sqlite-path default", func(t *testing.T) {
		f, ok := findFlag(t, cmd, "sqlite-path").(*cli.StringFlag)
		require.True(t, ok)
		require.Equal(t, "./webhook-tester.db", f.Value)
	})

	t.Run("response-script-timeout default 1s", func(t *testing.T) {
		f, ok := findFlag(t, cmd, "response-script-timeout").(*cli.DurationFlag)
		require.True(t, ok)
		require.Equal(t, time.Second, f.Value)
	})

	t.Run("hot-index-window default 168h", func(t *testing.T) {
		f, ok := findFlag(t, cmd, "hot-index-window").(*cli.DurationFlag)
		require.True(t, ok)
		require.Equal(t, 168*time.Hour, f.Value)
	})

	t.Run("auth-token flag exists", func(t *testing.T) {
		f, ok := findFlag(t, cmd, "auth-token").(*cli.StringFlag)
		require.True(t, ok)
		require.Empty(t, f.Value)
	})
}

func TestBuildHotIndexMap(t *testing.T) {
	t.Parallel()

	refs := []storage.IdentifierRef{
		{Key: "Alpha", Value: "ABC", SessionID: "s1", SessionSlug: "slug1", RequestID: "r1", CapturedAtUnixMilli: 100},
		{Key: "alpha", Value: "ABC", SessionID: "s2", SessionSlug: "slug2", RequestID: "r2", CapturedAtUnixMilli: 200},
		{Key: "beta", Value: "XYZ", SessionID: "s3", SessionSlug: "slug3", RequestID: "r3", CapturedAtUnixMilli: 300},
	}

	m := buildHotIndexMap(refs)

	// keys are lower-cased and joined to value by a NUL byte; same composite key collapses
	require.Len(t, m["alpha\x00ABC"], 2)
	require.Len(t, m["beta\x00XYZ"], 1)
	require.Equal(t, "s3", m["beta\x00XYZ"][0].SessionID)
	require.Equal(t, "slug3", m["beta\x00XYZ"][0].SessionSlug)
}

func TestWarmHotIndex_FromSQLite(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	extractor := identifiers.NewExtractor([]string{defaultIdentifierKeyTracking}, nil, true)

	dsn := "file:" + filepath.Join(t.TempDir(), "warm.db")
	db, err := storage.NewSQLite(ctx, dsn, time.Hour, 128, storage.WithSQLiteExtractor(extractor.Extract))
	require.NoError(t, err)
	t.Cleanup(func() { _ = db.Close() })

	sID, err := db.NewSession(ctx, storage.Session{Code: 200, Slug: "bob"})
	require.NoError(t, err)

	_, err = db.NewRequest(ctx, sID, storage.Request{
		Method: "POST",
		Body:   []byte(`{"trackingId":"ABC123"}`),
		URL:    "/bob",
	})
	require.NoError(t, err)

	hi := hotindex.New(168 * time.Hour)
	require.False(t, hi.Warmed(), "index must start un-warmed")

	warmHotIndex(ctx, db, hi, 168*time.Hour, zap.NewNop())

	require.True(t, hi.Warmed(), "index must be warmed after warm-up from a capable driver")

	refs := hi.Lookup(defaultIdentifierKeyTracking, "ABC123", storage.IdentifierMatchExact)
	require.Len(t, refs, 1)
	require.Equal(t, sID, refs[0].SessionID)
	require.Equal(t, "bob", refs[0].SessionSlug)
}

// memOnly is a storage.Storage that does NOT implement recentIdentifierLister,
// proving warm-up leaves such drivers un-warmed (search stays on the scan path).
type memOnly struct{ storage.Storage }

func TestWarmHotIndex_DriverWithoutCapabilityStaysCold(t *testing.T) {
	t.Parallel()

	hi := hotindex.New(time.Hour)
	warmHotIndex(context.Background(), memOnly{storage.NewInMemory(time.Hour, 8)}, hi, time.Hour, zap.NewNop())

	require.False(t, hi.Warmed(), "drivers without ListRecentIdentifiers must not be marked warm")
}
