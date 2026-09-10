package customers

import (
	"testing"
	"time"
)

func TestSavedPaymentFor(t *testing.T) {
	day := func(s string) int32 {
		tm, err := time.Parse("2006-01-02", s)
		if err != nil {
			t.Fatal(err)
		}
		return int32(tm.Unix() / 86400)
	}
	idx := &Index{savedPay: []savedPayRef{
		{customerID: 10, firstID: 501, addedDay: day("2010-05-01"), method: savedMethodEnum["card_emv"]},
		{customerID: 25, firstID: 502, addedDay: day("2012-01-15"), method: savedMethodEnum["third_party_online"]},
		{customerID: 99, firstID: 503, addedDay: day("2015-08-20"), method: savedMethodEnum["mobile_wallet_ios"]},
	}}

	at := time.Date(2011, 1, 1, 12, 0, 0, 0, time.UTC)

	// Hit: right customer, right method, window open.
	if id, ok := idx.SavedPaymentFor(10, at, "card_emv"); !ok || id != 501 {
		t.Errorf("customer 10 card_emv: want (501,true), got (%d,%v)", id, ok)
	}
	// Wrong method.
	if _, ok := idx.SavedPaymentFor(10, at, "bnpl"); ok {
		t.Error("customer 10 bnpl: expected miss")
	}
	// Before added date.
	early := time.Date(2010, 4, 1, 0, 0, 0, 0, time.UTC)
	if _, ok := idx.SavedPaymentFor(10, early, "card_emv"); ok {
		t.Error("before added_at: expected miss")
	}
	// V1.18.1: the added day itself does NOT count (day-granular index;
	// same-day availability allowed intra-day time inversions). The
	// day after does.
	sameDay := time.Date(2010, 5, 1, 9, 0, 0, 0, time.UTC)
	if _, ok := idx.SavedPaymentFor(10, sameDay, "card_emv"); ok {
		t.Error("same-day add: expected miss (next-day availability rule)")
	}
	nextDay := time.Date(2010, 5, 2, 0, 0, 1, 0, time.UTC)
	if id, ok := idx.SavedPaymentFor(10, nextDay, "card_emv"); !ok || id != 501 {
		t.Errorf("next-day add: want (501,true), got (%d,%v)", id, ok)
	}
	// Customer with no saved methods (between indexed ids).
	if _, ok := idx.SavedPaymentFor(11, at, "card_emv"); ok {
		t.Error("customer 11: expected miss")
	}
	// Customer above the indexed range.
	if _, ok := idx.SavedPaymentFor(1000, at, "card_emv"); ok {
		t.Error("customer 1000: expected miss")
	}
	// Unknown method string.
	if _, ok := idx.SavedPaymentFor(10, at, "carrier_pigeon"); ok {
		t.Error("unknown method: expected miss")
	}
	// Nil index.
	var nilIdx *Index
	if _, ok := nilIdx.SavedPaymentFor(10, at, "card_emv"); ok {
		t.Error("nil index: expected miss")
	}
}
