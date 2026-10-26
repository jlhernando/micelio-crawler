package extract

import (
	"strings"
	"testing"

	"github.com/PuerkitoBio/goquery"
)

func TestDetectSoft404Text(t *testing.T) {
	for _, c := range []struct{ html, body, want string }{
		{`<title>Nie znaleziono strony</title>`, "", "title"},
		{`<title>BMW X5</title><h1>0 ogłoszeń</h1>`, "", "h1"},
		{`<title>Coches</title><h1>Não encontrámos resultados</h1>`, "", "h1"},
		{`<title>Autoturisme</title><h1>Nu am găsit niciun anunț</h1>`, "", "h1"},
		{`<title>Ofertas</title><h1>No hay resultados</h1>`, "", "h1"},
		{`<title>BMW</title><h1>10 ogłoszeń</h1>`, "", ""},        // 10, not 0
		{`<title>BMW</title><h1>1 250 ogłoszeń</h1>`, "", ""},     // not a zero count
		{`<title>Guide</title><h1>Sofas</h1>`, "This product is not found in stores.", "body"},
		{`<title>Guide</title><h1>Sofas</h1>`, strings.Repeat("long text ", 80) + "not found", ""},
	} {
		doc, _ := goquery.NewDocumentFromReader(strings.NewReader("<html><head>" + c.html + "</head><body></body></html>"))
		if got := detectSoft404Text(doc, c.body); got != c.want {
			t.Errorf("%s / %.30q: got %q, want %q", c.html, c.body, got, c.want)
		}
	}
}

func TestCountItemLinks(t *testing.T) {
	links := []string{
		"https://cars.example/osobowe/oferta/bmw-x5-ID6HabCD.html",
		"https://autos.example/autoturisme/anunt/dacia-logan-ID7Gxyz1.html",
		"https://home.example/es/p/ektorp-sofa-s89628682/",
		"https://shop.example/products/chair",
		"https://shop.example/c/sofas/",
		"https://shop.example/about",
	}
	if got := countItemLinks(links); got != 4 {
		t.Errorf("item links = %d, want 4", got)
	}
}
