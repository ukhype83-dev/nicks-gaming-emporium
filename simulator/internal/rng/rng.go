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

// Mix64 is a deterministic, allocation-free hash of a root seed and a list of
// uint64 keys (a splitmix64 chain). The same inputs always yield the same
// output — on every run and every machine. Use it for an order-INDEPENDENT
// selection key (e.g. bottom-k sampling), where a full Derive() stream would
// be order-dependent and too costly to build per element. It is a hash, not a
// stream: it obeys the "seed everything from rootSeed" rule without an RNG.
func Mix64(rootSeed uint64, keys ...uint64) uint64 {
	h := splitmix(rootSeed)
	for _, k := range keys {
		h = splitmix(h ^ k)
	}
	return h
}

func splitmix(x uint64) uint64 {
	x += 0x9e3779b97f4a7c15
	x = (x ^ (x >> 30)) * 0xbf58476d1ce4e5b9
	x = (x ^ (x >> 27)) * 0x94d049bb133111eb
	return x ^ (x >> 31)
}

// FoldString folds a string into a uint64 (FNV-1a) for use as a Mix64 key,
// without allocating.
func FoldString(s string) uint64 {
	const (
		offset = 1469598103934665603
		prime  = 1099511628211
	)
	var h uint64 = offset
	for i := 0; i < len(s); i++ {
		h ^= uint64(s[i])
		h *= prime
	}
	return h
}
