package server

import (
	"context"

	"github.com/micelio/micelio/internal/analysis"
	"github.com/micelio/micelio/internal/storage"
	"github.com/micelio/micelio/internal/types"
)

// analysisReloadStore is the crawl store a dashboard crawl hands the
// crawler: its post-crawl reload decodes pages the way the dashboard
// analysis reads them (GetPagesForAnalysis: StructuredData without the
// JSON-LD Raw text, Images folded into ImageSummary). Dashboard crawls never
// write reloaded pages back; the CLI keeps full pages (it rewrites output).
// Anchors stored by --full-anchors crawls go into anchors page by page and
// are dropped: the report reads the exact results from there.
type analysisReloadStore struct {
	*storage.CrawlStore
	anchors **analysis.AnchorStream
}

func (s analysisReloadStore) GetAllPagesWithProgress(ctx context.Context, onProgress func(loaded int)) ([]*types.PageData, error) {
	if s.anchors == nil {
		return s.GetPagesForAnalysis(ctx, onProgress, nil, false)
	}
	st := analysis.NewAnchorStream()
	pages, err := s.GetPagesForAnalysis(ctx, onProgress, func(pg *types.PageData) {
		st.AddPage(pg)
		pg.Anchors = nil
	}, false)
	if err == nil {
		*s.anchors = st
	}
	return pages, err
}
