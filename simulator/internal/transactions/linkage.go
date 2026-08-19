// V1.18.0 — deterministic era-graded fill for till_id / device_id /
// processor_ref, plus the channel/method eligibility tables the
// linkage features share. Everything here is a pure function of ids
// already assigned — no RNG streams, so adding or changing these
// columns can never reshuffle unrelated output.

package transactions

import (
	"fmt"
	"strconv"
)

// detToken hashes (salt, id) with FNV-1a 64 — a tiny deterministic
// token source in the same spirit as rng.Derive, for columns that
// want opaque-looking but reproducible identifiers.
func detToken(id int64, salt string) uint64 {
	const (
		offset64 = 14695981039346656037
		prime64  = 1099511628211
	)
	h := uint64(offset64)
	for i := 0; i < len(salt); i++ {
		h ^= uint64(salt[i])
		h *= prime64
	}
	v := uint64(id)
	for i := 0; i < 8; i++ {
		h ^= (v >> (8 * i)) & 0xff
		h *= prime64
	}
	return h
}

// tillIDFor returns the till identifier for an in-store sale, or nil
// before till capture began (2008, mid-life of the transitional POS).
// 2008-2015 rows carry bare digits "1"-"4" — numeric-looking NVARCHAR,
// the implicit-conversion training exhibit — while the 2016 native POS
// writes "T1"-"T4". Till count varies 2-4 by shop.
func tillIDFor(txID, shopID int64, year int) *string {
	if year < 2008 {
		return nil
	}
	n := 1 + txID%(2+shopID%3)
	var s string
	if year >= 2016 {
		s = fmt.Sprintf("T%d", n)
	} else {
		s = strconv.FormatInt(n, 10)
	}
	return &s
}

// deviceIDFor returns the web/app device token for remote-channel
// sales from 2010 (the channel's modern instrumentation era).
func deviceIDFor(txID int64, channel string, year int) *string {
	if year < 2010 {
		return nil
	}
	var prefix string
	switch channel {
	case "online":
		prefix = "web"
	case "mobile_app":
		prefix = "app"
	default:
		return nil
	}
	s := fmt.Sprintf("%s-%08x", prefix, detToken(txID, "device")&0xffffffff)
	return &s
}

// processorRefMethods — payment methods that ride an external
// processor rail and therefore carry a processor reference from 2008.
var processorRefMethods = map[string]bool{
	"card_magstripe":        true,
	"card_emv":              true,
	"card_contactless":      true,
	"third_party_online":    true,
	"bnpl":                  true,
	"mobile_wallet_ios":     true,
	"mobile_wallet_android": true,
}

// processorRefFor returns the processor reference for an eligible
// payment from 2008, else nil. Fixed 13-char format "PR-XXXXXXXXXX".
func processorRefFor(paymentID int64, method string, year int) *string {
	if year < 2008 || !processorRefMethods[method] {
		return nil
	}
	s := fmt.Sprintf("PR-%010X", detToken(paymentID, "procref")&0xFFFFFFFFFF)
	return &s
}

// remoteChannels — channels whose orders ship to a customer address
// and whose payments can reuse a stored method.
var remoteChannels = map[string]bool{
	"online":            true,
	"mobile_app":        true,
	"click_and_collect": true,
}

// savedPayMethods — methods a customer can have on file
// (mirrors customers.savedMethodEnum / pickSavedMethodForYear).
var savedPayMethods = map[string]bool{
	"card_emv":              true,
	"third_party_online":    true,
	"card_contactless":      true,
	"mobile_wallet_ios":     true,
	"mobile_wallet_android": true,
	"bnpl":                  true,
}
