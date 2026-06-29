package household

import (
	"testing"
	"time"
)

func TestHouseholdLocation(t *testing.T) {
	h := &Household{Timezone: "America/New_York"}
	loc := h.Location()
	if loc.String() != "America/New_York" {
		t.Errorf("Location() = %q, want America/New_York", loc.String())
	}

	h2 := &Household{Timezone: ""}
	loc2 := h2.Location()
	if loc2 != time.UTC {
		t.Errorf("Location() for empty timezone = %q, want UTC", loc2.String())
	}

	h3 := &Household{Timezone: "Fake/Zone"}
	loc3 := h3.Location()
	if loc3 != time.UTC {
		t.Errorf("Location() for invalid timezone = %q, want UTC", loc3.String())
	}

	var nilHH *Household
	if loc4 := nilHH.Location(); loc4 != time.UTC {
		t.Errorf("Location() on nil receiver = %q, want UTC", loc4.String())
	}
}
