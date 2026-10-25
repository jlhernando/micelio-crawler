package extract

import (
	"strings"
	"testing"

	"github.com/PuerkitoBio/goquery"
)

func TestAdDetailSignalsIgnoreSiteChrome(t *testing.T) {
	doc := func(html string) *goquery.Document {
		d, err := goquery.NewDocumentFromReader(strings.NewReader(html))
		if err != nil {
			t.Fatal(err)
		}
		return d
	}
	// A help page whose only shop signal is the cart link in the header.
	help := doc(`<html><body><header><nav><a class="navbar__shopping-cart-link" href="/cart">Cart</a></nav></header>
		<main><h1>FAQ</h1><p>Answers</p></main><footer><a class="checkout-help">Checkout help</a></footer></body></html>`)
	if got := extractAdDetailSignals(help); got != 0 {
		t.Errorf("help page: got %d, want 0 (header/footer cart links ignored)", got)
	}
	product := doc(`<html><body><header><a class="cart-icon">Cart</a></header>
		<main><h1>Chair</h1><button class="add-to-cart">Add to cart</button></main></body></html>`)
	if got := extractAdDetailSignals(product); got != -1 {
		t.Errorf("product page: got %d, want -1", got)
	}
}
