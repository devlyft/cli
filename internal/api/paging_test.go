package api

import (
	"context"
	"errors"
	"slices"
	"testing"
)

// pagedServer serves n items honouring limit and offset.
func pagedServer(n int, calls *[]Page) PageFunc[int] {
	return func(_ context.Context, p Page) ([]int, int, error) {
		*calls = append(*calls, p)
		var items []int
		for i := p.Offset; i < n && len(items) < p.Limit; i++ {
			items = append(items, i)
		}
		return items, n, nil
	}
}

func TestPaged(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name      string
		total     int
		limit     int
		wantLen   int
		wantCalls []Page
	}{
		{"limit within one page", 500, 50, 50, []Page{{50, 0}}},
		{"limit across pages", 500, 150, 150, []Page{{100, 0}, {50, 100}}},
		{"all items", 230, 0, 230, []Page{{100, 0}, {100, 100}, {100, 200}}},
		{"limit above total", 30, 50, 30, []Page{{50, 0}}},
		{"empty", 0, 0, 0, []Page{{100, 0}}},
		{"exact page boundary", 200, 0, 200, []Page{{100, 0}, {100, 100}}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			var calls []Page
			got, err := Paged(t.Context(), tt.limit, pagedServer(tt.total, &calls))
			if err != nil {
				t.Fatal(err)
			}
			if len(got) != tt.wantLen {
				t.Errorf("len = %d, want %d", len(got), tt.wantLen)
			}
			for i, v := range got {
				if v != i {
					t.Fatalf("item %d = %d: items out of order or duplicated", i, v)
				}
			}
			if !slices.Equal(calls, tt.wantCalls) {
				t.Errorf("calls = %v, want %v", calls, tt.wantCalls)
			}
		})
	}
}

// A server that ignores limit and offset returns everything every time;
// Paged must stop and trim instead of looping.
func TestPagedServerIgnoresPaging(t *testing.T) {
	t.Parallel()

	calls := 0
	all := []int{0, 1, 2, 3, 4, 5, 6}
	got, err := Paged(t.Context(), 5, func(context.Context, Page) ([]int, int, error) {
		calls++
		return all, len(all), nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(got, all[:5]) || calls != 1 {
		t.Errorf("got %v after %d calls", got, calls)
	}
}

func TestPagedError(t *testing.T) {
	t.Parallel()

	boom := errors.New("boom")
	_, err := Paged(t.Context(), 0, func(context.Context, Page) ([]int, int, error) { return nil, 0, boom })
	if !errors.Is(err, boom) {
		t.Errorf("err = %v", err)
	}
}

func TestTrim(t *testing.T) {
	t.Parallel()

	items := []string{"a", "b", "c"}
	if got := Trim(items, 2); !slices.Equal(got, []string{"a", "b"}) {
		t.Errorf("Trim(2) = %v", got)
	}
	if got := Trim(items, 0); !slices.Equal(got, items) {
		t.Errorf("Trim(0) = %v", got)
	}
	if got := Trim(items, 10); !slices.Equal(got, items) {
		t.Errorf("Trim(10) = %v", got)
	}
}
