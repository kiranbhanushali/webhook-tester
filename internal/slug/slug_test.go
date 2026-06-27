package slug_test

import (
	"regexp"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"gh.tarampamp.am/webhook-tester/v2/internal/slug"
)

// slugFormat mirrors the contract documented for Generate: a lower-case,
// hyphen-separated identifier between 2 and 49 characters long.
var slugFormat = regexp.MustCompile(`^[a-z0-9][a-z0-9-]{1,48}$`)

func TestGenerate_Format(t *testing.T) {
	t.Parallel()

	for range 1_000 {
		s := slug.Generate()

		require.Truef(t, slugFormat.MatchString(s), "slug %q must match %s", s, slugFormat)
		assert.LessOrEqual(t, len(s), 49, "slug %q must be at most 49 chars", s)
	}
}

func TestGenerate_Shape_AdjectiveNounHex(t *testing.T) {
	t.Parallel()

	// adjective-noun-<4hex>
	shape := regexp.MustCompile(`^[a-z]+-[a-z]+-[0-9a-f]{4}$`)

	for range 100 {
		s := slug.Generate()
		assert.Truef(t, shape.MatchString(s), "slug %q must look like adjective-noun-<4hex>", s)
	}
}

func TestGenerate_MostlyUnique(t *testing.T) {
	t.Parallel()

	const n = 5_000

	seen := make(map[string]struct{}, n)
	for range n {
		seen[slug.Generate()] = struct{}{}
	}

	// With a 4-hex suffix (65 536 combinations) plus wordlists, collisions over
	// 5 000 draws must be rare. Require the vast majority to be distinct.
	assert.Greaterf(t, len(seen), int(float64(n)*0.95),
		"expected >95%% unique slugs, got %d/%d", len(seen), n)
}
