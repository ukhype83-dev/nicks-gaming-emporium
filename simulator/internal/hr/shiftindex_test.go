package hr

import (
	"testing"
	"time"

	"emporium/internal/rng"
)

func mkPerson(spellID int64, shopID int64, shifts ...[2]string) Person {
	p := Person{Spell: EmploymentSpell{SpellID: spellID}}
	for i, s := range shifts {
		p.Shifts = append(p.Shifts, StaffShift{
			ShiftID:    int64(i + 1),
			ShopID:     shopID,
			ShiftStart: s[0],
			ShiftEnd:   s[1],
		})
	}
	return p
}

func TestPickOnDutyBasics(t *testing.T) {
	ix := NewShiftIndex()
	ix.AddPerson(mkPerson(101, 5,
		[2]string{"2010-06-01T09:00:00.000Z", "2010-06-01T17:00:00.000Z"}))
	ix.AddPerson(mkPerson(102, 5,
		[2]string{"2010-06-01T12:00:00.000Z", "2010-06-01T20:00:00.000Z"}))
	ix.AddPerson(mkPerson(103, 9, // different shop
		[2]string{"2010-06-01T09:00:00.000Z", "2010-06-01T17:00:00.000Z"}))
	ix.Finalize()

	r := rng.Derive(42, "test/staff")

	at := time.Date(2010, 6, 1, 10, 0, 0, 0, time.UTC)
	id, ok := ix.PickOnDuty(5, at, r)
	if !ok || id != 101 {
		t.Errorf("10:00 at shop 5: want (101,true), got (%d,%v)", id, ok)
	}

	// Overlap window: both on duty — must return one of them.
	at = time.Date(2010, 6, 1, 14, 0, 0, 0, time.UTC)
	id, ok = ix.PickOnDuty(5, at, r)
	if !ok || (id != 101 && id != 102) {
		t.Errorf("14:00 at shop 5: want 101 or 102, got (%d,%v)", id, ok)
	}

	// Boundary semantics: start inclusive, end exclusive.
	at = time.Date(2010, 6, 1, 12, 0, 0, 0, time.UTC)
	found := map[int64]bool{}
	for i := 0; i < 50; i++ {
		id, ok = ix.PickOnDuty(5, at, r)
		if !ok {
			t.Fatal("12:00 exactly: expected candidates")
		}
		found[id] = true
	}
	if !found[101] || !found[102] {
		t.Errorf("12:00 start-inclusive: expected both 101 and 102 reachable, got %v", found)
	}
	at = time.Date(2010, 6, 1, 17, 0, 0, 0, time.UTC)
	id, ok = ix.PickOnDuty(5, at, r)
	if !ok || id != 102 {
		t.Errorf("17:00 end-exclusive: want only 102, got (%d,%v)", id, ok)
	}

	// Nobody rostered.
	at = time.Date(2010, 6, 2, 3, 0, 0, 0, time.UTC)
	if _, ok = ix.PickOnDuty(5, at, r); ok {
		t.Error("03:00 next day: expected no candidates")
	}
	// Unknown shop.
	if _, ok = ix.PickOnDuty(777, at, r); ok {
		t.Error("unknown shop: expected no candidates")
	}
}

func TestPickOnDutyNilSafe(t *testing.T) {
	var ix *ShiftIndex
	r := rng.Derive(1, "x")
	if _, ok := ix.PickOnDuty(1, time.Now(), r); ok {
		t.Error("nil index must return false")
	}
	ix.AddPerson(Person{}) // must not panic
}
