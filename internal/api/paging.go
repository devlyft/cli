package api

import "context"

// MaxPageSize is the largest page the paged list endpoints accept.
const MaxPageSize = 100

// Page selects a page of a paged list endpoint.
type Page struct {
	Limit  int
	Offset int
}

// PageFunc fetches one page and reports the total number of items the
// endpoint has.
type PageFunc[T any] func(ctx context.Context, p Page) (items []T, total int, err error)

// maxPages stops a server that ignores offset from looping forever.
const maxPages = 10000

// Paged collects up to limit items (all items when limit <= 0) from an
// endpoint that supports limit and offset, fetching as few pages as needed.
func Paged[T any](ctx context.Context, limit int, fetch PageFunc[T]) ([]T, error) {
	var out []T
	for range maxPages {
		size := MaxPageSize
		if limit > 0 {
			size = min(size, limit-len(out))
		}
		items, total, err := fetch(ctx, Page{Limit: size, Offset: len(out)})
		if err != nil {
			return nil, err
		}
		out = append(out, items...)
		if len(items) == 0 || len(items) < size || len(out) >= total || (limit > 0 && len(out) >= limit) {
			break
		}
	}
	return Trim(out, limit), nil
}

// Trim returns at most limit items (all items when limit <= 0). Use it for
// endpoints without server-side paging, which return everything at once.
func Trim[T any](items []T, limit int) []T {
	if limit > 0 && len(items) > limit {
		return items[:limit]
	}
	return items
}
