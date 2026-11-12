package extract

import (
	"math/rand"
	"regexp"
	"strings"
	"testing"
)

// The original regexps, kept verbatim: foldWords must answer exactly like them.
func TestFoldWordsMatchesOriginalRegexps(t *testing.T) {
	cases := []struct {
		m  *foldWords
		re *regexp.Regexp
	}{
		{listingClassPatterns, regexp.MustCompile(`(?i)(search-results|product-list|listing|results-list|item-list|catalog|grid-view|card-list|offers-list|search-list|product-grid)`)},
		{listingDataAttrPatterns, regexp.MustCompile(`(?i)(results|listing|catalog)`)},
		{adDetailClassPatterns, regexp.MustCompile(`(?i)(contact-seller|message-seller|seller-info|seller-profile|seller-card|ad-params|ad-attributes|report-ad|flag-ad|ad-gallery|offer-params|offer-details|listing-details|ad-description|seller-details|vendor-info)`)},
		{ecommerceClassPatterns, regexp.MustCompile(`(?i)(add-to-cart|add-to-bag|add-to-basket|buy-now|buy-it-now|checkout|shopping-cart|cart-icon|quantity-selector|qty-selector|product-variants|size-selector|color-selector)`)},
		{definitionPattern, regexp.MustCompile(`(?i)(is a|is an|refers to|defined as|means|is the)`)},
	}
	fixed := []string{
		"", " ", "LISTING", "x Search-Results y", "ſeller-info", "CHECKOUT", "checKout",
		"seller-inf", "add-to-\xffcart", "listinG", "x listin", "Iſ a", "IS THE", "seller-iNFO\xff", "\xffad-paramS", "listing\xff", "café catalog", strings.Repeat("a", 1500) + "listing",
		"card-list ", "btn btn--primary js-add-to-basket", "product-grid__item",
	}
	pieces := []string{"seller", "-", "info", "LIST", "ing", "cart", "add", "to", "Ca", "talog", " ", "ſ", "K", "é", "\xff", "results", "x", "buy", "now", "ad", "params", "is", "a", "the", "means", "Me"}
	rng := rand.New(rand.NewSource(1))
	for _, c := range cases {
		inputs := append([]string{}, fixed...)
		for _, w := range c.m.words {
			inputs = append(inputs, string(w), strings.ToUpper(string(w)), "pre "+string(w)[1:])
		}
		for i := 0; i < 20000; i++ {
			var b strings.Builder
			for n := rng.Intn(8); n >= 0; n-- {
				b.WriteString(pieces[rng.Intn(len(pieces))])
			}
			inputs = append(inputs, b.String())
		}
		for _, in := range inputs {
			if got, want := c.m.MatchString(in), c.re.MatchString(in); got != want {
				t.Fatalf("%s on %q: got %v, want %v", c.re, in, got, want)
			}
		}
	}
}

// The gated matchers must answer exactly like the original regexps.
func TestGatedMatchersMatchOriginalRegexps(t *testing.T) {
	origItem := regexp.MustCompile(`(?i)/products?/[^/?#]+|/items?/[^/?#]+|/p/[^/?#]+|/pd/[^/?#]+|/sku/|/dp/[^/?#]+|` +
		`/(oferta|offer|annonce|anuncio|anunt|oglas|ilan|inserat)/[^/?#]+|/d/oferta/|/marketplace/item/|/(itm|item|listing)/[^/]*-?\d{5,}|[-_]ID[0-9A-Za-z]{4,}\.html`)
	origDef := regexp.MustCompile(`(?i)(is a|is an|refers to|defined as|means|is the)`)
	urlPieces := []string{"/", "product", "PRODUCTS", "item", "Items", "p", "pd", "ſku", "SKU", "dp", "oferta", "offer", "annonce",
		"anuncio", "anunt", "oglas", "ilan", "inserat", "d", "marketplace", "itm", "listing", "-", "_", "id", "ID", "12345", "678",
		"abcd", ".html", ".HTML", "?", "#", "x", "é", "\xff", "K", "https://www.petsitters.example", "de", "fr"}
	defPieces := []string{"is", " ", "a", "an", "IS", "ſ", "K", "the", "means", "MEANS", "refers", "to", "defined", "as", "é", "\xff", "x", "ı"}
	rng := rand.New(rand.NewSource(2))
	gen := func(pieces []string) string {
		var b strings.Builder
		for n := rng.Intn(10); n >= 0; n-- {
			b.WriteString(pieces[rng.Intn(len(pieces))])
		}
		return b.String()
	}
	for i := 0; i < 100000; i++ {
		u := gen(urlPieces)
		if got, want := isItemURL(u), origItem.MatchString(u); got != want {
			t.Fatalf("item URL %q: got %v, want %v", u, got, want)
		}
		d := gen(defPieces)
		if got, want := definitionPattern.MatchString(d), origDef.MatchString(d); got != want {
			t.Fatalf("definition %q: got %v, want %v", d, got, want)
		}
	}
	classPieces := []string{"product", "PRODUCT", "-", "_", " ", "card", "pod", "item", "tile", "box", "thumb", "s", "ſ", "x", "é"}
	for i := 0; i < 50000; i++ {
		c := gen(classPieces)
		got := containsFoldASCII(c, "product") && productItemClass.MatchString(c)
		if want := productItemClass.MatchString(c); got != want {
			t.Fatalf("class %q: got %v, want %v", c, got, want)
		}
	}
}
