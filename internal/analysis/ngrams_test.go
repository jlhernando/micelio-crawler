package analysis

import (
	"testing"
)

func TestNgramAnalyzerBasic(t *testing.T) {
	analyzer := NewNgramAnalyzer("en")

	analyzer.AddPage("Golang is a great programming language for building web applications and microservices and distributed systems")
	analyzer.AddPage("Python is a great programming language for machine learning and data science applications")

	results := analyzer.GetResults(10)

	if results.TotalPages != 2 {
		t.Errorf("TotalPages = %d, want 2", results.TotalPages)
	}
	if results.TotalTokens == 0 {
		t.Error("TotalTokens should be > 0")
	}
	if len(results.Unigrams) == 0 {
		t.Error("Expected unigrams")
	}
	if len(results.Bigrams) == 0 {
		t.Error("Expected bigrams")
	}

	// "programming" and "language" should appear as shared terms
	found := false
	for _, entry := range results.Unigrams {
		if entry.Term == "programming" || entry.Term == "language" || entry.Term == "great" {
			if entry.Pages == 2 {
				found = true
			}
		}
	}
	if !found {
		t.Error("Expected shared terms across 2 pages")
	}
}

func TestNgramStopwords(t *testing.T) {
	analyzer := NewNgramAnalyzer("en")
	analyzer.AddPage("the quick brown fox jumps over the lazy dog and the cat sat on the mat near the big tree")

	results := analyzer.GetResults(50)
	for _, entry := range results.Unigrams {
		if entry.Term == "the" || entry.Term == "and" || entry.Term == "on" {
			t.Errorf("Stopword %q should be filtered", entry.Term)
		}
	}
}

func TestNgramTFIDF(t *testing.T) {
	analyzer := NewNgramAnalyzer("en")
	// Term that appears on all pages should have lower TF-IDF than rare terms
	analyzer.AddPage("golang performance benchmark testing results show impressive improvements in speed and memory allocation patterns")
	analyzer.AddPage("golang performance benchmark testing results demonstrate significant gains in throughput and latency optimization")
	analyzer.AddPage("rust systems programming offers memory safety guarantees without garbage collection overhead or runtime costs")

	results := analyzer.GetResults(20)
	if len(results.Unigrams) == 0 {
		t.Fatal("Expected unigrams")
	}

	// All entries should have non-negative TF-IDF
	for _, entry := range results.Unigrams {
		if entry.TFIDF < 0 {
			t.Errorf("TF-IDF for %q = %f, should be >= 0", entry.Term, entry.TFIDF)
		}
	}
}

func TestNgramShortText(t *testing.T) {
	analyzer := NewNgramAnalyzer("en")
	analyzer.AddPage("hi") // Too short
	results := analyzer.GetResults(10)
	if results.TotalPages != 0 {
		t.Errorf("Short text should be skipped, got totalPages = %d", results.TotalPages)
	}
}

func TestNgramSetLanguage(t *testing.T) {
	analyzer := NewNgramAnalyzer("en")
	analyzer.SetLanguage("es")
	// Spanish stopword "para" should be filtered
	analyzer.AddPage("este programa es para desarrolladores que trabajan con tecnología moderna y necesitan herramientas eficientes para desarrollo")
	results := analyzer.GetResults(50)
	for _, entry := range results.Unigrams {
		if entry.Term == "para" || entry.Term == "que" || entry.Term == "con" {
			t.Errorf("Spanish stopword %q should be filtered", entry.Term)
		}
	}
}

func TestTokenize(t *testing.T) {
	stopwords := map[string]struct{}{"the": {}, "is": {}, "a": {}}
	tokens := tokenize("The fox is a quick 123 animal!", stopwords)
	// Should filter "the", "is", "a", "123"
	for _, tok := range tokens {
		if tok == "the" || tok == "is" || tok == "a" || tok == "123" {
			t.Errorf("Token %q should be filtered", tok)
		}
	}
	if len(tokens) != 3 { // "fox", "quick", "animal"
		t.Errorf("Expected 3 tokens, got %d: %v", len(tokens), tokens)
	}
}
