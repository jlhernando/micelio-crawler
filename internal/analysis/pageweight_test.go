package analysis

import (
	"context"
	"fmt"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/micelio/micelio/internal/types"
)

func TestComputePageWeightPerHostLimit(t *testing.T) {
	refs := map[string][]types.ResourceEntry{}
	var entries []types.ResourceEntry
	for i := 0; i < 20; i++ {
		entries = append(entries, types.ResourceEntry{URL: fmt.Sprintf("https://cdn.example.com/r%d.js", i), Type: "script"})
		entries = append(entries, types.ResourceEntry{URL: fmt.Sprintf("https://img.example.com/r%d.png", i), Type: "image"})
	}
	refs["https://example.com/"] = entries
	pages := []*types.PageData{{URL: "https://example.com/", BodySize: 100}}

	var mu sync.Mutex
	inFlight := map[string]int{}
	maxInFlight := map[string]int{}
	var calls atomic.Int32
	sizer := func(ctx context.Context, u string) (int64, bool) {
		host := u[len("https://"):]
		host = host[:len("cdn.example.com")]
		mu.Lock()
		inFlight[host]++
		if inFlight[host] > maxInFlight[host] {
			maxInFlight[host] = inFlight[host]
		}
		mu.Unlock()
		time.Sleep(5 * time.Millisecond)
		mu.Lock()
		inFlight[host]--
		mu.Unlock()
		calls.Add(1)
		return 10, true
	}

	ComputePageWeight(context.Background(), pages, refs, nil, 16, sizer)

	if calls.Load() != 40 {
		t.Fatalf("sizer called %d times, want 40", calls.Load())
	}
	for host, m := range maxInFlight {
		if m > pageWeightPerHost {
			t.Errorf("host %s had %d concurrent requests, limit %d", host, m, pageWeightPerHost)
		}
	}
	if pages[0].PageWeight == nil || pages[0].PageWeight.TotalBytes != 100+40*10 {
		t.Fatalf("unexpected page weight: %+v", pages[0].PageWeight)
	}
}

func TestComputePageWeightCancelled(t *testing.T) {
	refs := map[string][]types.ResourceEntry{}
	for i := 0; i < 50; i++ {
		refs["https://example.com/"] = append(refs["https://example.com/"], types.ResourceEntry{URL: fmt.Sprintf("https://cdn.example.com/%d", i)})
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	var calls atomic.Int32
	ComputePageWeight(ctx, []*types.PageData{{URL: "https://example.com/"}}, refs, nil, 4, func(ctx context.Context, u string) (int64, bool) {
		calls.Add(1)
		return 1, true
	})
	if calls.Load() > 4 {
		t.Errorf("cancelled context still issued %d requests", calls.Load())
	}
}
