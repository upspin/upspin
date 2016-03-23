package upspin

import (
	"testing"
	"time"
)

func TestTime(t *testing.T) {
	// The zero value is the Unix epoch.
	epoch := Time(0)
	// Verify that it's what we expect and prints properly.
	if epoch.Go().Unix() != 0 {
		t.Fatalf("Unix epoch should be zero in Unix seconds, is %d", epoch.Go().Unix())
	}
	str := epoch.String()
	const unixDate = "1970-01-01T00:00:00 UTC"
	if str != unixDate {
		t.Errorf("expected Unix epoch to be %q; got %q", unixDate, str)
	}
	// Do one by hand.
	goTime := time.Date(2001, 3, 15, 17, 39, 12, 0, time.UTC)
	t.Log(goTime)
	theTime := TimeFromGo(goTime)
	const theDate = "2001-03-15T17:39:12 UTC"
	str = theTime.String()
	if str != theDate {
		t.Errorf("expected the date to be %q; got %q", theDate, str)
	}

}
