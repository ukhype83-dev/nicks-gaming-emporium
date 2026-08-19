// Package rng provides deterministic PRNG helpers for the simulator.
//
// The core idea: never use a global RNG. Every generator derives its own
// stream from (rootSeed, namespace) so that independent sub-generators can
// run in parallel without stepping on each other's output, and so that
// re-running with the same root seed produces byte-identical results.
//
// §9.10.7 of PROJECT_NOTES.md requires: "no unseeded RNG, no
// datetime.now(), no UUIDs". Derive() is the single entry point that
// enforces this — if you need randomness, take an *rand.Rand from here.
package rng

import (
	"hash/fnv"
	"math/rand/v2"
)

// Derive returns a deterministic *rand.Rand seeded from (rootSeed,
// namespace). Different namespaces under the same rootSeed yield
// independent streams; the same (rootSeed, namespace) pair always
// yields the same stream.
//
// Namespace convention: "<domain>/<entity>/<optional sub-key>"
//   e.g. "shops/GB", "customers/US/1987", "trade_ins/2012".
func Derive(rootSeed uint64, namespace string) *rand.Rand {
	h := fnv.New64a()
	var buf [8]byte
	for i := 0; i < 8; i++ {
		buf[i] = byte(rootSeed >> (8 * i))
	}
	h.Write(buf[:])
	h.Write([]byte(namespace))
	s1 := h.Sum64()
	s2 := ^s1 // second seed uncorrelated with first
	return rand.New(rand.NewPCG(s1, s2))
}
