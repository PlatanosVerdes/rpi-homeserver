package main

import (
	"testing"
	"time"
)

func TestNext(t *testing.T) {
	at := func(h, m int) time.Time { return time.Date(2026, 10, 27, h, m, 0, 0, time.UTC) }
	cases := []struct {
		now, want time.Time
	}{
		{at(0, 0), at(0, 47)},
		{at(0, 47), at(6, 47)},
		{at(5, 59), at(6, 47)},
		{at(13, 10), at(18, 47)},
		{at(18, 48), time.Date(2026, 10, 28, 0, 47, 0, 0, time.UTC)},
	}
	for _, c := range cases {
		if got := next(c.now, 6, 47); !got.Equal(c.want) {
			t.Errorf("next(%s) = %s, want %s", c.now.Format("15:04"), got.Format("Jan 2 15:04"), c.want.Format("Jan 2 15:04"))
		}
	}
}
