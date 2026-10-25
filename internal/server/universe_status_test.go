package server

import (
	"testing"

	"github.com/micelio/micelio/internal/types"
)

func TestPageStatusClassRobotsBlockedIsNotError(t *testing.T) {
	for _, c := range []struct {
		p    types.PageData
		want byte
	}{
		{types.PageData{StatusCode: 200}, 0},
		{types.PageData{StatusCode: 301}, 1},
		{types.PageData{StatusCode: 404}, 2},
		{types.PageData{StatusCode: 0}, 2}, // network failure
		{types.PageData{StatusCode: 0, RobotsBlocked: true}, 0},
	} {
		if got := pageStatusClass(&c.p); got != c.want {
			t.Errorf("status %d blocked %v: got %d, want %d", c.p.StatusCode, c.p.RobotsBlocked, got, c.want)
		}
	}
}
