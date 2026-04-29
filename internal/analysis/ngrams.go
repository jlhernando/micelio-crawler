package analysis

import (
	"math"
	"regexp"
	"sort"
	"strings"
	"unicode"
)

// NgramEntry represents a single n-gram with frequency metrics.
type NgramEntry struct {
	Term  string  `json:"term"`
	Count int     `json:"count"` // total occurrences across all pages
	Pages int     `json:"pages"` // number of pages the term appears on
	TFIDF float64 `json:"tfidf"` // TF-IDF score
}

// NgramStats holds the aggregated n-gram results.
type NgramStats struct {
	Unigrams    []NgramEntry `json:"unigrams"`
	Bigrams     []NgramEntry `json:"bigrams"`
	Trigrams    []NgramEntry `json:"trigrams"`
	TotalPages  int          `json:"totalPages"`
	TotalTokens int          `json:"totalTokens"`
}

const maxEntries = 10000

var nonWordRe = regexp.MustCompile(`[^\p{L}\p{N}\s'-]`)

// NgramAnalyzer incrementally computes n-gram statistics across pages.
type NgramAnalyzer struct {
	unigrams    map[string]*ngramData
	bigrams     map[string]*ngramData
	trigrams    map[string]*ngramData
	stopwords   map[string]struct{}
	language    string
	totalPages  int
	totalTokens int
}

type ngramData struct {
	count int
	pages int
}

// NewNgramAnalyzer creates a new analyzer for the given language.
func NewNgramAnalyzer(language string) *NgramAnalyzer {
	if language == "" {
		language = "en"
	}
	return &NgramAnalyzer{
		unigrams:  make(map[string]*ngramData),
		bigrams:   make(map[string]*ngramData),
		trigrams:  make(map[string]*ngramData),
		stopwords: getStopwords(language),
		language:  language,
	}
}

// SetLanguage updates the language for stopword filtering.
func (a *NgramAnalyzer) SetLanguage(lang string) {
	if lang != a.language {
		if _, ok := stopwordLists[lang]; ok {
			a.language = lang
			a.stopwords = getStopwords(lang)
		}
	}
}

// AddPage processes a page's body text and updates n-gram counts.
func (a *NgramAnalyzer) AddPage(bodyText string) {
	if len(bodyText) < 50 {
		return
	}

	tokens := tokenize(bodyText, a.stopwords)
	if len(tokens) < 3 {
		return
	}

	a.totalPages++
	a.totalTokens += len(tokens)

	pageUnigrams := make(map[string]struct{})
	pageBigrams := make(map[string]struct{})
	pageTrigrams := make(map[string]struct{})

	// Unigrams
	for _, tok := range tokens {
		if d, ok := a.unigrams[tok]; ok {
			d.count++
		} else {
			a.unigrams[tok] = &ngramData{count: 1}
		}
		pageUnigrams[tok] = struct{}{}
	}

	// Bigrams
	var sb strings.Builder
	for i := 0; i < len(tokens)-1; i++ {
		sb.Reset()
		sb.Grow(len(tokens[i]) + 1 + len(tokens[i+1]))
		sb.WriteString(tokens[i])
		sb.WriteByte(' ')
		sb.WriteString(tokens[i+1])
		bg := sb.String()
		if d, ok := a.bigrams[bg]; ok {
			d.count++
		} else {
			a.bigrams[bg] = &ngramData{count: 1}
		}
		pageBigrams[bg] = struct{}{}
	}

	// Trigrams
	for i := 0; i < len(tokens)-2; i++ {
		sb.Reset()
		sb.Grow(len(tokens[i]) + 1 + len(tokens[i+1]) + 1 + len(tokens[i+2]))
		sb.WriteString(tokens[i])
		sb.WriteByte(' ')
		sb.WriteString(tokens[i+1])
		sb.WriteByte(' ')
		sb.WriteString(tokens[i+2])
		tg := sb.String()
		if d, ok := a.trigrams[tg]; ok {
			d.count++
		} else {
			a.trigrams[tg] = &ngramData{count: 1}
		}
		pageTrigrams[tg] = struct{}{}
	}

	// Update page counts
	for term := range pageUnigrams {
		a.unigrams[term].pages++
	}
	for term := range pageBigrams {
		a.bigrams[term].pages++
	}
	for term := range pageTrigrams {
		a.trigrams[term].pages++
	}

	// Memory cap
	pruneIfNeeded(a.unigrams)
	pruneIfNeeded(a.bigrams)
	pruneIfNeeded(a.trigrams)
}

// GetResults returns the final n-gram analysis.
func (a *NgramAnalyzer) GetResults(topN int) NgramStats {
	if topN == 0 {
		topN = 50
	}
	return NgramStats{
		Unigrams:    buildEntries(a.unigrams, a.totalPages, a.totalTokens, topN),
		Bigrams:     buildEntries(a.bigrams, a.totalPages, a.totalTokens, topN),
		Trigrams:    buildEntries(a.trigrams, a.totalPages, a.totalTokens, topN),
		TotalPages:  a.totalPages,
		TotalTokens: a.totalTokens,
	}
}

func buildEntries(m map[string]*ngramData, totalPages, totalTokens, topN int) []NgramEntry {
	entries := make([]NgramEntry, 0, len(m))
	for term, data := range m {
		if data.pages < 2 && totalPages > 5 {
			continue
		}
		tf := float64(data.count) / math.Max(float64(totalTokens), 1)
		idf := math.Log(math.Max(float64(totalPages), 1) / math.Max(float64(data.pages), 1))
		tfidf := math.Round(tf*idf*100000) / 100000

		entries = append(entries, NgramEntry{
			Term:  term,
			Count: data.count,
			Pages: data.pages,
			TFIDF: tfidf,
		})
	}

	sort.Slice(entries, func(i, j int) bool {
		return entries[i].Count > entries[j].Count
	})

	if len(entries) > topN {
		entries = entries[:topN]
	}
	return entries
}

func pruneIfNeeded(m map[string]*ngramData) {
	if len(m) <= maxEntries {
		return
	}
	type kv struct {
		key   string
		count int
	}
	items := make([]kv, 0, len(m))
	for k, v := range m {
		items = append(items, kv{k, v.count})
	}
	sort.Slice(items, func(i, j int) bool {
		return items[i].count > items[j].count
	})

	keepCount := maxEntries * 3 / 4
	// Clear map and re-add top entries
	keep := make(map[string]struct{}, keepCount)
	for i := 0; i < keepCount && i < len(items); i++ {
		keep[items[i].key] = struct{}{}
	}
	for k := range m {
		if _, ok := keep[k]; !ok {
			delete(m, k)
		}
	}
}

// tokenize lowercases text, strips punctuation, splits on whitespace,
// filters stopwords and numbers. Supports multilingual text via Unicode.
func tokenize(text string, stopwords map[string]struct{}) []string {
	lower := strings.ToLower(text)
	cleaned := nonWordRe.ReplaceAllString(lower, " ")
	words := strings.Fields(cleaned)

	tokens := make([]string, 0, len(words))
	for _, w := range words {
		w = strings.Trim(w, "'-")
		if len(w) < 2 {
			continue
		}
		if _, stop := stopwords[w]; stop {
			continue
		}
		if isAllDigits(w) {
			continue
		}
		tokens = append(tokens, w)
	}
	return tokens
}

func isAllDigits(s string) bool {
	for _, r := range s {
		if !unicode.IsDigit(r) {
			return false
		}
	}
	return true
}

func getStopwords(language string) map[string]struct{} {
	words := make(map[string]struct{})
	// Always include English
	for _, w := range stopwordLists["en"] {
		words[w] = struct{}{}
	}
	if language != "en" {
		if list, ok := stopwordLists[language]; ok {
			for _, w := range list {
				words[w] = struct{}{}
			}
		}
	}
	for _, w := range webStopwords {
		words[w] = struct{}{}
	}
	return words
}

var webStopwords = []string{
	"home", "page", "click", "read", "next", "previous", "back", "top",
	"menu", "close", "open", "search", "login", "sign", "submit", "loading",
}

var stopwordLists = map[string][]string{
	"en": {
		"a", "about", "above", "after", "again", "against", "all", "also", "am", "an",
		"and", "any", "are", "as", "at", "be", "because", "been", "before", "being",
		"below", "between", "both", "but", "by", "can", "could", "did", "do", "does",
		"doing", "down", "during", "each", "even", "few", "for", "from", "further",
		"get", "got", "had", "has", "have", "having", "he", "her", "here", "hers",
		"herself", "him", "himself", "his", "how", "i", "if", "in", "into", "is",
		"it", "its", "itself", "just", "know", "let", "like", "ll", "make", "may",
		"me", "might", "more", "most", "much", "must", "my", "myself", "need", "no",
		"nor", "not", "now", "of", "off", "on", "once", "one", "only", "or", "other",
		"our", "ours", "ourselves", "out", "over", "own", "per", "really", "re",
		"s", "said", "same", "say", "she", "should", "since", "so", "some", "still",
		"such", "take", "t", "than", "that", "the", "their", "theirs", "them",
		"themselves", "then", "there", "these", "they", "this", "those", "through",
		"to", "too", "under", "until", "up", "us", "ve", "very", "want", "was",
		"we", "well", "were", "what", "when", "where", "which", "while", "who",
		"whom", "why", "will", "with", "won", "would", "you", "your", "yours",
		"yourself", "yourselves",
	},
	"es": {
		"a", "al", "algo", "algunas", "algunos", "ante", "antes", "como", "con", "contra",
		"cual", "cuando", "de", "del", "desde", "donde", "durante", "e", "el", "ella",
		"ellas", "ellos", "en", "entre", "era", "esa", "esas", "ese", "eso", "esos",
		"esta", "estaba", "estado", "estar", "estas", "este", "esto", "estos", "fue",
		"ha", "hace", "hacia", "hasta", "hay", "la", "las", "le", "les", "lo", "los",
		"mas", "me", "mi", "muy", "na", "ni", "no", "nos", "nosotros", "nuestro",
		"nuestra", "o", "os", "otra", "otras", "otro", "otros", "para", "pero", "por",
		"que", "quien", "se", "ser", "si", "sin", "sino", "sobre", "somos", "son",
		"soy", "su", "sus", "te", "ti", "tiene", "todo", "todos", "tu", "tus", "un",
		"una", "unas", "uno", "unos", "usted", "ustedes", "y", "ya", "yo",
	},
	"fr": {
		"a", "ai", "au", "aux", "avec", "avons", "c", "ce", "ces", "comme", "d",
		"dans", "de", "des", "du", "elle", "elles", "en", "es", "est", "et", "eu",
		"eux", "fait", "il", "ils", "j", "je", "l", "la", "le", "les", "leur",
		"leurs", "lui", "ma", "mais", "me", "mes", "mon", "n", "ne", "ni", "nos",
		"notre", "nous", "on", "ont", "ou", "par", "pas", "plus", "pour", "qu",
		"que", "qui", "s", "sa", "se", "ses", "si", "son", "sont", "sur", "ta",
		"te", "tes", "ton", "tu", "un", "une", "vos", "votre", "vous", "y",
	},
	"de": {
		"aber", "alle", "allem", "allen", "aller", "als", "also", "am", "an", "ander",
		"andere", "anderem", "anderen", "anderer", "anderes", "auch", "auf", "aus",
		"bei", "bin", "bis", "bist", "da", "damit", "dann", "das", "dass", "dazu",
		"dem", "den", "denn", "der", "des", "die", "dies", "diese", "diesem", "diesen",
		"dieser", "dieses", "doch", "dort", "du", "durch", "ein", "eine", "einem",
		"einen", "einer", "er", "es", "etwas", "euch", "euer", "eure", "eurem",
		"euren", "eurer", "für", "gegen", "hab", "habe", "haben", "hat", "hatte",
		"ich", "ihm", "ihn", "ihnen", "ihr", "ihre", "ihrem", "ihren", "ihrer",
		"im", "in", "ist", "jede", "jedem", "jeden", "jeder", "jedes", "kann",
		"kein", "keine", "keinem", "keinen", "keiner", "man", "mein", "meine",
		"meinem", "meinen", "meiner", "mit", "muss", "müssen", "nach", "nicht", "nichts",
		"noch", "nun", "nur", "ob", "oder", "ohne", "sehr", "sein", "seine",
		"seinem", "seinen", "seiner", "sich", "sie", "sind", "so", "soll", "und",
		"uns", "unser", "unsere", "unserem", "unseren", "unserer", "unter", "um",
		"von", "vor", "was", "weil", "welch", "welche", "welchem", "welchen",
		"welcher", "wenn", "wer", "werde", "wie", "will", "wir", "wird", "wo",
		"würde", "zu", "zum", "zur", "über",
	},
	"pt": {
		"a", "ao", "aos", "aquela", "aquelas", "aquele", "aqueles", "aquilo", "as",
		"com", "como", "da", "das", "de", "dela", "delas", "dele", "deles", "depois",
		"do", "dos", "e", "ela", "elas", "ele", "eles", "em", "entre", "era", "essa",
		"essas", "esse", "esses", "esta", "estas", "este", "estes", "eu", "foi",
		"isso", "isto", "lhe", "lhes", "lo", "mais", "mas", "me", "mesmo",
		"meu", "minha", "muito", "na", "nas", "nem", "no", "nos", "nossa", "nossas",
		"nosso", "nossos", "num", "numa", "nuns", "o", "os", "ou", "para", "pela",
		"pelas", "pelo", "pelos", "por", "qual", "quando", "que", "quem", "se", "sem",
		"ser", "seu", "seus", "sua", "suas", "te", "tem", "teu", "teus",
		"tu", "tua", "tuas", "um", "uma", "umas", "uns",
	},
	"it": {
		"a", "abbiamo", "ad", "ai", "al", "alla", "alle", "allo", "anche", "avere",
		"aveva", "che", "chi", "ci", "come", "con", "contro", "cui", "da", "dal",
		"dalla", "dalle", "dallo", "degli", "dei", "del", "dell", "della", "delle",
		"dello", "di", "dopo", "e", "era", "essere", "fatto", "fra", "fu", "gli",
		"ha", "hai", "hanno", "ho", "i", "il", "in", "io", "l", "la", "le", "lei",
		"li", "lo", "loro", "lui", "ma", "me", "mi", "mia", "mie", "miei", "mio",
		"ne", "nei", "nel", "nella", "nelle", "nello", "no", "noi", "non", "nostra",
		"nostre", "nostri", "nostro", "o", "per", "piu", "quale", "quando", "quello",
		"questa", "queste", "questi", "questo", "se", "si", "sia", "sono", "su",
		"sua", "sue", "sui", "sul", "sulla", "sulle", "sullo", "suo", "suoi", "ti",
		"tra", "tu", "tua", "tue", "tuo", "tuoi", "tutti", "tutto", "un", "una",
		"uno", "vi", "voi", "vostra", "vostre", "vostri", "vostro",
	},
	"nl": {
		"aan", "al", "alles", "als", "bij", "daar", "dan", "dat", "de", "der", "die",
		"dit", "doch", "doen", "door", "dus", "een", "en", "er", "ge", "geen", "haar",
		"had", "heeft", "hem", "het", "hier", "hij", "hoe", "hun", "iets", "ik", "in",
		"is", "ja", "je", "kan", "kon", "maar", "me", "meer", "men", "met", "mij",
		"mijn", "moet", "na", "naar", "niet", "niets", "nog", "nu", "of", "om", "omdat",
		"ook", "op", "over", "reeds", "te", "tegen", "toch", "toen", "tot", "u", "uit",
		"uw", "van", "veel", "voor", "want", "waren", "was", "wat", "we", "wel",
		"werd", "wij", "wil", "worden", "wordt", "zal", "ze", "zelf", "zich", "zij",
		"zijn", "zo", "zou",
	},
	"pl": {
		"a", "ale", "ani", "az", "bardzo", "bez", "bo", "by", "byla", "byli", "bylo",
		"byl", "byc", "co", "czy", "dla", "do", "go", "i", "ich", "jak", "jako", "ja",
		"je", "jednak", "jego", "jej", "jest", "jeszcze", "juz", "kiedy", "ku", "ma",
		"mi", "mnie", "moze", "mozna", "mu", "na", "nad", "nam", "nas", "nie", "nich",
		"nic", "nim", "no", "o", "od", "on", "ona", "oni", "ono", "oraz", "po", "pod",
		"przez", "przy", "sa", "sie", "so", "ta", "tak", "tam", "te", "tego", "tej",
		"ten", "to", "tu", "tylko", "ty", "tych", "tym", "w", "wie", "wiele", "wszystko",
		"z", "za", "ze",
	},
	"sv": {
		"alla", "allt", "att", "av", "bara", "bli", "blivit", "de", "den",
		"denna", "det", "detta", "dig", "din", "dina", "dit", "dock", "du", "efter",
		"eller", "en", "er", "era", "ett", "finns", "från", "gav", "ger",
		"ha", "hade", "han", "hans", "har", "hen", "hennes", "hon", "honom",
		"hur", "i", "in", "ingen", "inte", "ja", "jag", "kan", "kunde", "man", "med",
		"men", "mig", "min", "mina", "mot", "mycket", "ni", "nu",
		"och", "om", "oss", "som", "till", "under", "upp", "ur", "ut", "vad", "var", "vara", "vi", "vid",
		"vill",
	},
	"da": {
		"ad", "af", "aldrig", "alle", "alt", "anden", "andet", "andre", "at", "bare",
		"begge", "blev", "da", "de", "dem", "den", "denne", "der", "deres", "det",
		"dette", "dig", "din", "dine", "disse", "dit", "dog", "du", "efter", "eller",
		"en", "end", "er", "et", "for", "fordi", "fra", "gik", "han", "hans",
		"har", "havde", "hen", "hende", "hendes", "her", "hos", "hun", "hvad",
		"hvem", "hver", "hvilken", "hvis", "hvor", "i", "igen", "ikke", "ind", "ingen",
		"ja", "jeg", "kan", "kom", "kunne", "lidt", "man", "mange", "med", "meget",
		"men", "mig", "min", "mine", "mit", "mod", "ned", "nej", "noget", "nogle",
		"nu", "og", "op", "os", "over", "selv", "sig", "sin",
		"sine", "sit", "skal", "skulle", "som", "til", "ud", "var",
	},
	"tr": {
		"acaba", "ama", "ancak", "bana", "bir", "biri", "birisi", "biz", "bu", "bunu",
		"da", "daha", "de", "defa", "diye", "eger", "en", "gibi", "hem", "henuz",
		"hep", "hepsi", "her", "herkes", "hi", "ic", "icin", "ile", "ise", "ki",
		"kim", "kimi", "mu", "nasil", "ne", "neden", "nerde", "nerede", "nereye",
		"niye", "o", "ona", "onlar", "onu", "onun", "sana", "sen", "siz", "su",
		"ve", "veya", "ya", "yani",
	},
}
