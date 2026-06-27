// Package slug generates short, human-readable, URL-safe session slugs of the
// form "adjective-noun-<4hex>" (for example "calm-otter-3f9a").
//
// The random adjective, noun and hex suffix are drawn from crypto/rand so the
// output is unpredictable. Every generated slug matches the contract regexp
// ^[a-z0-9][a-z0-9-]{1,48}$ (2..49 lower-case chars), which is the same shape
// the storage layer and the OpenAPI SessionUUIDInPath parameter accept.
package slug

import (
	"crypto/rand"
	"encoding/binary"
	"fmt"
)

// adjectives and nouns are short, lower-case, ASCII-only words. They are kept
// deliberately small and free of hyphens/digits so the composed slug always
// satisfies the slug regexp and stays well under the 49-char limit.
var adjectives = []string{ //nolint:gochecknoglobals // static wordlist
	"calm", "brave", "swift", "quiet", "bright", "lucky", "shy", "bold",
	"eager", "fancy", "gentle", "happy", "jolly", "kind", "lively", "merry",
	"neat", "proud", "rapid", "snug", "tidy", "vivid", "warm", "witty",
	"zesty", "amber", "azure", "coral", "crisp", "dapper", "fuzzy", "glossy",
	"husky", "icy", "jazzy", "keen", "loyal", "mellow", "nimble", "plucky",
	"quaint", "rustic", "silky", "tame", "upbeat", "velvet", "wily", "young",
	"agile", "breezy", "cosmic", "dusty", "elated", "feisty", "golden", "hazy",
	"ideal", "jaunty", "frosty", "lunar", "misty", "noble", "ornate", "polar",
}

var nouns = []string{ //nolint:gochecknoglobals // static wordlist
	"otter", "falcon", "maple", "river", "comet", "ember", "willow", "pebble",
	"lynx", "heron", "cedar", "harbor", "meadow", "canyon", "glacier", "marsh",
	"badger", "finch", "thistle", "quartz", "raven", "salmon", "tundra", "viper",
	"walrus", "yak", "zebra", "anchor", "beacon", "cactus", "dolphin", "eagle",
	"ferret", "gecko", "hawk", "ibis", "jaguar", "koala", "lemur", "mantis",
	"newt", "ocelot", "panda", "quail", "robin", "stork", "tiger", "urchin",
	"vulture", "weasel", "bison", "crane", "drake", "egret", "fox", "gull",
	"hare", "iguana", "jay", "kestrel", "lark", "moose", "narwhal", "puffin",
}

// Generate returns a new random slug of the form "adjective-noun-<4hex>".
//
// It draws every component from crypto/rand. In the astronomically unlikely
// event the entropy source fails, it falls back to deterministic first-word
// choices so it never panics; the caller is still expected to enforce
// uniqueness (e.g. by checking storage and regenerating on collision).
func Generate() string {
	adj := adjectives[randIndex(len(adjectives))]
	noun := nouns[randIndex(len(nouns))]

	var suffix [2]byte
	if _, err := rand.Read(suffix[:]); err != nil {
		suffix = [2]byte{0, 0} // fallback; uniqueness is enforced by the caller
	}

	return fmt.Sprintf("%s-%s-%04x", adj, noun, binary.BigEndian.Uint16(suffix[:]))
}

// randIndex returns a uniformly distributed integer in [0, n) using crypto/rand.
// On entropy failure it returns 0 (the caller enforces uniqueness regardless).
func randIndex(n int) int {
	if n <= 0 {
		return 0
	}

	var b [8]byte
	if _, err := rand.Read(b[:]); err != nil {
		return 0
	}

	// Modulo bias is negligible here: n is a small wordlist length and the
	// source is a full 64-bit value.
	return int(binary.BigEndian.Uint64(b[:]) % uint64(n)) //nolint:gosec // n>0 checked above
}
