package crawler

import (
	"context"
	"net/http"

	"github.com/micelio/micelio/internal/urlutil"
)

// newRequest builds an outgoing request with the query escaped like a
// browser would, so links with raw spaces or accents in the query work.
func newRequest(ctx context.Context, method, rawURL string) (*http.Request, error) {
	req, err := http.NewRequestWithContext(ctx, method, rawURL, nil)
	if err != nil {
		return nil, err
	}
	req.URL.RawQuery = urlutil.EscapeQuery(req.URL.RawQuery)
	return req, nil
}
