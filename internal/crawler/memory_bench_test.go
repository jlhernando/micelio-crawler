package crawler

import (
	"fmt"
	"runtime"
	"testing"

	"github.com/micelio/micelio/internal/types"
)

// memStats captures heap metrics at a point in time.
func memStats() runtime.MemStats {
	runtime.GC()
	var m runtime.MemStats
	runtime.ReadMemStats(&m)
	return m
}

// --- 1. externalLinks map accumulation ---

// BenchmarkExternalLinksMap_1K simulates accumulating external links for 1,000 pages.
func BenchmarkExternalLinksMap_1K(b *testing.B) {
	benchExternalLinksMap(b, 1000, 30)
}

func BenchmarkExternalLinksMap_10K(b *testing.B) {
	benchExternalLinksMap(b, 10000, 30)
}

func BenchmarkExternalLinksMap_50K(b *testing.B) {
	benchExternalLinksMap(b, 50000, 30)
}

func benchExternalLinksMap(b *testing.B, pages, extLinksPerPage int) {
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		externalLinks := make(map[string][]string)
		for p := 0; p < pages; p++ {
			pageURL := fmt.Sprintf("https://en.wikipedia.org/wiki/Page_%d", p)
			for j := 0; j < extLinksPerPage; j++ {
				extURL := fmt.Sprintf("https://external-%d.com/article/%d", j%500, j)
				externalLinks[extURL] = append(externalLinks[extURL], pageURL)
			}
		}
	}
}

// BenchmarkExternalLinksMap_MemSize measures actual heap bytes for the map.
// Uses 10K unique external URLs across 2K pages (realistic for Wikipedia).
func BenchmarkExternalLinksMap_MemSize(b *testing.B) {
	pages := 2000
	extLinksPerPage := 30

	before := memStats()

	externalLinks := make(map[string][]string)
	for p := 0; p < pages; p++ {
		pageURL := fmt.Sprintf("https://en.wikipedia.org/wiki/Page_%d", p)
		for j := 0; j < extLinksPerPage; j++ {
			// Wikipedia pages link to many unique external domains
			extURL := fmt.Sprintf("https://external-%d.com/article/%d", p*extLinksPerPage+j, j)
			externalLinks[extURL] = append(externalLinks[extURL], pageURL)
		}
	}

	after := memStats()
	heapMB := float64(after.HeapAlloc-before.HeapAlloc) / 1024 / 1024
	b.ReportMetric(heapMB, "heap-MB")
	b.ReportMetric(float64(len(externalLinks)), "unique-ext-urls")
	b.ReportMetric(0, "ns/op") // suppress default

	// Prevent compiler from optimizing away
	runtime.KeepAlive(externalLinks)
}

// --- 2. resourceRefs map accumulation ---

func BenchmarkResourceRefsMap_1K(b *testing.B) {
	benchResourceRefsMap(b, 1000, 40)
}

func BenchmarkResourceRefsMap_10K(b *testing.B) {
	benchResourceRefsMap(b, 10000, 40)
}

func BenchmarkResourceRefsMap_50K(b *testing.B) {
	benchResourceRefsMap(b, 50000, 40)
}

func benchResourceRefsMap(b *testing.B, pages, refsPerPage int) {
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		resourceRefs := make(map[string][]types.ResourceEntry)
		for p := 0; p < pages; p++ {
			pageURL := fmt.Sprintf("https://en.wikipedia.org/wiki/Page_%d", p)
			refs := make([]types.ResourceEntry, refsPerPage)
			for j := 0; j < refsPerPage; j++ {
				refs[j] = types.ResourceEntry{
					URL:  fmt.Sprintf("https://upload.wikimedia.org/image_%d_%d.jpg", p, j),
					Type: types.ResourceType([]string{"image", "script", "stylesheet", "font"}[j%4]),
				}
			}
			resourceRefs[pageURL] = refs
		}
	}
}

// BenchmarkResourceRefsMap_MemSize measures actual heap bytes for the map.
func BenchmarkResourceRefsMap_MemSize(b *testing.B) {
	pages := 2000
	refsPerPage := 40

	before := memStats()

	resourceRefs := make(map[string][]types.ResourceEntry)
	for p := 0; p < pages; p++ {
		pageURL := fmt.Sprintf("https://en.wikipedia.org/wiki/Page_%d", p)
		refs := make([]types.ResourceEntry, refsPerPage)
		for j := 0; j < refsPerPage; j++ {
			refs[j] = types.ResourceEntry{
				URL:  fmt.Sprintf("https://upload.wikimedia.org/image_%d_%d.jpg", p, j),
				Type: types.ResourceType([]string{"image", "script", "stylesheet", "font"}[j%4]),
			}
		}
		resourceRefs[pageURL] = refs
	}

	after := memStats()
	heapMB := float64(after.HeapAlloc-before.HeapAlloc) / 1024 / 1024
	b.ReportMetric(heapMB, "heap-MB")
	b.ReportMetric(float64(len(resourceRefs)), "pages")
	b.ReportMetric(0, "ns/op")

	runtime.KeepAlive(resourceRefs)
}

// --- Disk-backed comparisons ---

// BenchmarkExternalLinksDisk_MemSize measures heap during disk-backed accumulation.
func BenchmarkExternalLinksDisk_MemSize(b *testing.B) {
	pages := 2000
	extLinksPerPage := 30

	disk, err := newExternalLinksDisk()
	if err != nil {
		b.Fatal(err)
	}

	before := memStats()

	for p := 0; p < pages; p++ {
		pageURL := fmt.Sprintf("https://en.wikipedia.org/wiki/Page_%d", p)
		for j := 0; j < extLinksPerPage; j++ {
			extURL := fmt.Sprintf("https://external-%d.com/article/%d", p*extLinksPerPage+j, j)
			disk.Add(extURL, pageURL)
		}
	}

	after := memStats()
	heapMB := float64(after.HeapAlloc-before.HeapAlloc) / 1024 / 1024
	b.ReportMetric(heapMB, "crawl-heap-MB")

	// Load back into map (post-crawl phase)
	result, err := disk.Load()
	if err != nil {
		b.Fatal(err)
	}
	b.ReportMetric(float64(len(result)), "unique-ext-urls")
	b.ReportMetric(0, "ns/op")

	runtime.KeepAlive(result)
}

// BenchmarkResourceRefsDisk_MemSize measures heap during disk-backed accumulation.
func BenchmarkResourceRefsDisk_MemSize(b *testing.B) {
	pages := 2000
	refsPerPage := 40

	disk, err := newResourceRefsDisk()
	if err != nil {
		b.Fatal(err)
	}

	before := memStats()

	for p := 0; p < pages; p++ {
		pageURL := fmt.Sprintf("https://en.wikipedia.org/wiki/Page_%d", p)
		refs := make([]types.ResourceEntry, refsPerPage)
		for j := 0; j < refsPerPage; j++ {
			refs[j] = types.ResourceEntry{
				URL:  fmt.Sprintf("https://upload.wikimedia.org/image_%d_%d.jpg", p, j),
				Type: types.ResourceType([]string{"image", "script", "stylesheet", "font"}[j%4]),
			}
		}
		disk.Add(pageURL, refs)
	}

	after := memStats()
	heapMB := float64(after.HeapAlloc-before.HeapAlloc) / 1024 / 1024
	b.ReportMetric(heapMB, "crawl-heap-MB")

	result, err := disk.Load()
	if err != nil {
		b.Fatal(err)
	}
	b.ReportMetric(float64(len(result)), "pages")
	b.ReportMetric(0, "ns/op")

	runtime.KeepAlive(result)
}

// --- 3. PageData struct size ---

// BenchmarkPageDataSize_Realistic measures memory for realistic PageData accumulation.
func BenchmarkPageDataSize_Realistic(b *testing.B) {
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		pages := make([]*types.PageData, 0, 1000)
		for p := 0; p < 1000; p++ {
			page := buildRealisticPageData(p)
			pages = append(pages, &page)
		}
		runtime.KeepAlive(pages)
	}
}

// BenchmarkPageDataSize_MemSize measures actual heap for 2000 PageData structs.
func BenchmarkPageDataSize_MemSize(b *testing.B) {
	before := memStats()

	pages := make([]*types.PageData, 0, 2000)
	for p := 0; p < 2000; p++ {
		page := buildRealisticPageData(p)
		pages = append(pages, &page)
	}

	after := memStats()
	heapMB := float64(after.HeapAlloc-before.HeapAlloc) / 1024 / 1024
	b.ReportMetric(heapMB, "heap-MB")
	b.ReportMetric(float64(after.HeapAlloc-before.HeapAlloc)/2000, "bytes/page")
	b.ReportMetric(0, "ns/op")

	runtime.KeepAlive(pages)
}

// --- 4. Queue bloom filter at scale ---

// BenchmarkBloomFilter_MemSize measures actual heap for 100K URL bloom filter.
func BenchmarkBloomFilter_MemSize(b *testing.B) {
	n := 100000
	before := memStats()

	q := NewCrawlQueue("https://en.wikipedia.org", 10, n, &QueueOptions{EnforceInternal: false})
	for j := 0; j < n; j++ {
		q.Enqueue(fmt.Sprintf("https://en.wikipedia.org/wiki/Page_%d", j), 0, nil)
	}

	after := memStats()
	heapMB := float64(after.HeapAlloc-before.HeapAlloc) / 1024 / 1024
	b.ReportMetric(heapMB, "heap-MB")
	b.ReportMetric(float64(after.HeapAlloc-before.HeapAlloc)/float64(n), "bytes/url")
	b.ReportMetric(0, "ns/op")

	runtime.KeepAlive(q)
}

// --- Helper: build realistic PageData ---

func buildRealisticPageData(idx int) types.PageData {
	title := fmt.Sprintf("Wikipedia Article %d — A Comprehensive Guide", idx)
	desc := fmt.Sprintf("This is the meta description for article %d, which covers various topics in depth.", idx)
	tl := types.TextLength{Text: title, Length: len(title)}
	dl := types.TextLength{Text: desc, Length: len(desc)}

	internalLinks := make([]string, 15)
	for j := 0; j < 15; j++ {
		internalLinks[j] = fmt.Sprintf("https://en.wikipedia.org/wiki/Related_%d_%d", idx, j)
	}
	externalLinks := make([]string, 8)
	for j := 0; j < 8; j++ {
		externalLinks[j] = fmt.Sprintf("https://source-%d.com/ref/%d", j, idx)
	}
	images := make([]types.ImageData, 5)
	for j := 0; j < 5; j++ {
		alt := fmt.Sprintf("Image %d for article %d", j, idx)
		images[j] = types.ImageData{
			Src: fmt.Sprintf("https://upload.wikimedia.org/image_%d_%d.jpg", idx, j),
			Alt: &alt,
		}
	}
	anchors := make([]types.AnchorData, 20)
	for j := 0; j < 20; j++ {
		anchors[j] = types.AnchorData{
			Href: fmt.Sprintf("/wiki/Anchor_%d_%d", idx, j),
			Text: fmt.Sprintf("Anchor text %d", j),
		}
	}

	bodyText := ""
	for j := 0; j < 10; j++ {
		bodyText += fmt.Sprintf("This is paragraph %d of article %d with enough content to be realistic. ", j, idx)
	}

	return types.PageData{
		URL:           fmt.Sprintf("https://en.wikipedia.org/wiki/Article_%d", idx),
		FinalURL:      fmt.Sprintf("https://en.wikipedia.org/wiki/Article_%d", idx),
		StatusCode:    200,
		Depth:         idx % 5,
		Title:         &tl,
		MetaDescription: &dl,
		InternalLinks: internalLinks,
		ExternalLinks: externalLinks,
		Images:        images,
		Anchors:       anchors,
		BodyText:      bodyText,
		WordCount:     150,
		ResponseTimeMs: 250,
		Headings: types.HeadingData{
			H1: []string{title},
			H2: []string{"Section 1", "Section 2", "Section 3"},
		},
		Security: types.SecurityData{
			IsHTTPS: true,
		},
	}
}
