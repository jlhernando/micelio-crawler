package browser

import (
	"bytes"
	"context"
	"image/jpeg"
	"strings"
	"time"

	"github.com/chromedp/cdproto/cdp"
	"github.com/chromedp/cdproto/page"
	"github.com/chromedp/chromedp"
	"golang.org/x/net/html"
)

// PreviewFrame is a frozen image of the crawler's document. Only image data,
// never untrusted HTML, is returned to the dashboard.
type PreviewFrame struct {
	JPEG       []byte    `json:"jpeg"`
	URL        string    `json:"url"`
	BrowserURL string    `json:"browserUrl,omitempty"`
	Title      string    `json:"title"`
	StatusCode int       `json:"statusCode"`
	Width      int       `json:"width"`
	Height     int       `json:"height"`
	Mode       string    `json:"mode"`
	UserAgent  string    `json:"userAgent"`
	CapturedAt time.Time `json:"capturedAt"`
}

func capturePreview(ctx context.Context) (*PreviewFrame, error) {
	f := &PreviewFrame{CapturedAt: time.Now().UTC()}
	var err error
	f.BrowserURL, err = chromedp.Run(ctx, chromedp.Location())
	if err != nil {
		return nil, err
	}
	f.Title, err = chromedp.Run(ctx, chromedp.Title())
	if err != nil {
		return nil, err
	}
	quality := int64(75)
	f.JPEG, err = chromedp.Run(ctx, chromedp.Action[[]byte](func(ctx context.Context, t *chromedp.Target) ([]byte, error) {
		shot, err := cdp.Call(ctx, t, page.CaptureScreenshot, page.CaptureScreenshotParams{Format: page.CaptureScreenshotFormatJpeg, Quality: &quality})
		return shot.Data, err
	}))
	if err != nil {
		return nil, err
	}
	size, err := jpeg.DecodeConfig(bytes.NewReader(f.JPEG))
	if err != nil {
		return nil, err
	}
	f.Width, f.Height = size.Width, size.Height
	return f, nil
}

// Snapshot captures the existing native crawl tab without navigating or
// emulating another device. The caller closes Chrome after receiving the image.
func (n *Native) Snapshot(request context.Context) (*PreviewFrame, error) {
	n.mu.Lock()
	defer n.mu.Unlock()
	ctx, cancel := context.WithTimeout(n.ctx, 15*time.Second)
	stop := context.AfterFunc(request, cancel)
	defer func() { stop(); cancel() }()
	return capturePreview(ctx)
}

// A browser follows Refresh directives even with JavaScript disabled. The HTTP
// crawler does not; suppress those invisible directives in the visual copy.
// Tokenization preserves every other source byte, including noscript content.
func withoutPreviewRefresh(source string) string {
	var out strings.Builder
	tokens := html.NewTokenizer(strings.NewReader(source))
	for {
		kind := tokens.Next()
		if kind == html.ErrorToken {
			out.Write(tokens.Raw())
			return out.String()
		}
		raw := append([]byte(nil), tokens.Raw()...)
		skip := false
		if kind == html.StartTagToken || kind == html.SelfClosingTagToken {
			token := tokens.Token()
			if token.Data == "meta" {
				for _, attribute := range token.Attr {
					if strings.EqualFold(attribute.Key, "http-equiv") && strings.EqualFold(strings.TrimSpace(attribute.Val), "refresh") {
						skip = true
					}
				}
			}
		}
		if !skip {
			out.Write(raw)
		}
	}
}
