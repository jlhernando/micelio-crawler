package analysis

import (
	"fmt"
	"testing"

	"github.com/micelio/micelio/internal/types"
)

// shopPage builds a page with a shop's mega-menu: many internal links and
// little text, which on its own scores as a listing.
func shopPage(url, h1 string) *types.PageData {
	p := &types.PageData{URL: url, FinalURL: url, StatusCode: 200, WordCount: 320, Depth: 2}
	p.Headings.H1 = []string{h1}
	for i := 0; i < 70; i++ {
		p.InternalLinks = append(p.InternalLinks, fmt.Sprintf("https://shop.example/es/es/cat/c%d/", i))
	}
	for i := 0; i < 11; i++ {
		p.Images = append(p.Images, types.ImageData{})
	}
	return p
}

func TestDetectTemplateUtilityPagesOnShops(t *testing.T) {
	const seed = "https://shop.example/es/es/"
	for _, c := range []struct{ url, h1, want string }{
		{"https://shop.example/es/es/customer-service/faq/", "Preguntas frecuentes", TemplateFAQ},
		{"https://shop.example/es/es/customer-service/contact-us/", "Contáctanos", TemplateContact},
		{"https://shop.example/es/es/customer-service/", "Atención al cliente", TemplateContact},
		{"https://shop.example/es/es/customer-service/privacy-security/", "Privacidad y seguridad", TemplateLegal},
		{"https://shop.example/es/es/customer-service/terms-conditions/", "Términos y condiciones", TemplateLegal},
		{"https://shop.example/es/legal/politica-de-privacidad", "Política de privacidad", TemplateLegal},
		{"https://shop.example/pt/perguntas-frequentes", "Perguntas frequentes", TemplateFAQ},
		{"https://shop.example/de/impressum", "Impressum", TemplateLegal},
		// URL and H1 must agree: a guide that mentions FAQ stays as scored.
		{"https://shop.example/es/es/customer-service/mobile-apps/", "Nuestras apps, por un mejor día a día", TemplateListing},
		{"https://shop.example/es/es/ideas/faq-style-guide/", "Cómo elegir un sofá", TemplateListing},
	} {
		if got := DetectTemplateType(shopPage(c.url, c.h1), seed); got != c.want {
			t.Errorf("%s (%q): got %s, want %s", c.url, c.h1, got, c.want)
		}
	}

	// A product named after a legal word keeps its product signals.
	p := shopPage("https://shop.example/es/es/p/cookie-jar-123/", "Cookie jar")
	p.ProductSignals = 1
	p.StructuredData = []types.StructuredDataEntry{{Type: "Product"}}
	if got := DetectTemplateType(p, seed); got != TemplateProduct {
		t.Errorf("cookie jar product: got %s, want product", got)
	}
}
