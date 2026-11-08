package integration

import (
	"errors"
	"fmt"
	"io"
)

// errResponseTooLarge means an API answer did not fit the body cap. Paged
// clients (GSC, GA4) answer it by asking again for fewer rows.
var errResponseTooLarge = errors.New("response larger than the size limit")

// minPageRows is the smallest page a paged client shrinks to before it gives
// up on an answer that still does not fit the body cap.
const minPageRows = 250

// readCappedBody reads at most max bytes. A body longer than max returns
// errResponseTooLarge instead of a silently cut (and unparsable) prefix.
func readCappedBody(r io.Reader, max int64) ([]byte, error) {
	body, err := io.ReadAll(io.LimitReader(r, max+1))
	if err != nil {
		return nil, err
	}
	if int64(len(body)) > max {
		return nil, fmt.Errorf("%w (%d bytes)", errResponseTooLarge, max)
	}
	return body, nil
}

// shrinkPage halves a page size after errResponseTooLarge. ok is false when
// the page is already at the floor and the request cannot get smaller.
func shrinkPage(size int) (int, bool) {
	if size <= minPageRows {
		return size, false
	}
	size /= 2
	if size < minPageRows {
		size = minPageRows
	}
	return size, true
}
