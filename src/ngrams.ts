/**
 * N-Gram analysis engine for keyword discovery and topic coverage.
 * Produces unigrams (1-word), bigrams (2-word), and trigrams (3-word) frequency analysis.
 */

export interface NgramEntry {
  term: string;
  count: number;   // total occurrences across all pages
  pages: number;   // number of pages the term appears on
  tfidf: number;   // TF-IDF score (relative importance)
}

export interface NgramStats {
  unigrams: NgramEntry[];
  bigrams: NgramEntry[];
  trigrams: NgramEntry[];
  totalPages: number;
  totalTokens: number;
}

// Multilingual stopword lists keyed by ISO 639-1 language code
const STOPWORDS: Record<string, string[]> = {
  en: [
    'a', 'about', 'above', 'after', 'again', 'against', 'all', 'also', 'am', 'an',
    'and', 'any', 'are', 'as', 'at', 'be', 'because', 'been', 'before', 'being',
    'below', 'between', 'both', 'but', 'by', 'can', 'could', 'did', 'do', 'does',
    'doing', 'down', 'during', 'each', 'even', 'few', 'for', 'from', 'further',
    'get', 'got', 'had', 'has', 'have', 'having', 'he', 'her', 'here', 'hers',
    'herself', 'him', 'himself', 'his', 'how', 'i', 'if', 'in', 'into', 'is',
    'it', 'its', 'itself', 'just', 'know', 'let', 'like', 'll', 'make', 'may',
    'me', 'might', 'more', 'most', 'much', 'must', 'my', 'myself', 'need', 'no',
    'nor', 'not', 'now', 'of', 'off', 'on', 'once', 'one', 'only', 'or', 'other',
    'our', 'ours', 'ourselves', 'out', 'over', 'own', 'per', 'really', 're',
    's', 'said', 'same', 'say', 'she', 'should', 'since', 'so', 'some', 'still',
    'such', 'take', 't', 'than', 'that', 'the', 'their', 'theirs', 'them',
    'themselves', 'then', 'there', 'these', 'they', 'this', 'those', 'through',
    'to', 'too', 'under', 'until', 'up', 'us', 've', 'very', 'want', 'was',
    'we', 'well', 'were', 'what', 'when', 'where', 'which', 'while', 'who',
    'whom', 'why', 'will', 'with', 'won', 'would', 'you', 'your', 'yours',
    'yourself', 'yourselves',
  ],
  es: [
    'a', 'al', 'algo', 'algunas', 'algunos', 'ante', 'antes', 'como', 'con', 'contra',
    'cual', 'cuando', 'de', 'del', 'desde', 'donde', 'durante', 'e', 'el', 'ella',
    'ellas', 'ellos', 'en', 'entre', 'era', 'esa', 'esas', 'ese', 'eso', 'esos',
    'esta', 'estaba', 'estado', 'estar', 'estas', 'este', 'esto', 'estos', 'fue',
    'ha', 'hace', 'hacia', 'hasta', 'hay', 'la', 'las', 'le', 'les', 'lo', 'los',
    'mas', 'me', 'mi', 'muy', 'na', 'ni', 'no', 'nos', 'nosotros', 'nuestro',
    'nuestra', 'o', 'os', 'otra', 'otras', 'otro', 'otros', 'para', 'pero', 'por',
    'que', 'quien', 'se', 'ser', 'si', 'sin', 'sino', 'sobre', 'somos', 'son',
    'soy', 'su', 'sus', 'te', 'ti', 'tiene', 'todo', 'todos', 'tu', 'tus', 'un',
    'una', 'unas', 'uno', 'unos', 'usted', 'ustedes', 'y', 'ya', 'yo',
  ],
  fr: [
    'a', 'ai', 'au', 'aux', 'avec', 'avons', 'c', 'ce', 'ces', 'comme', 'd',
    'dans', 'de', 'des', 'du', 'elle', 'elles', 'en', 'es', 'est', 'et', 'eu',
    'eux', 'fait', 'il', 'ils', 'j', 'je', 'l', 'la', 'le', 'les', 'leur',
    'leurs', 'lui', 'ma', 'mais', 'me', 'mes', 'mon', 'n', 'ne', 'ni', 'nos',
    'notre', 'nous', 'on', 'ont', 'ou', 'par', 'pas', 'plus', 'pour', 'qu',
    'que', 'qui', 's', 'sa', 'se', 'ses', 'si', 'son', 'sont', 'sur', 'ta',
    'te', 'tes', 'ton', 'tu', 'un', 'une', 'vos', 'votre', 'vous', 'y',
  ],
  de: [
    'aber', 'alle', 'allem', 'allen', 'aller', 'als', 'also', 'am', 'an', 'ander',
    'andere', 'anderem', 'anderen', 'anderer', 'anderes', 'auch', 'auf', 'aus',
    'bei', 'bin', 'bis', 'bist', 'da', 'damit', 'dann', 'das', 'dass', 'dazu',
    'dem', 'den', 'denn', 'der', 'des', 'die', 'dies', 'diese', 'diesem', 'diesen',
    'dieser', 'dieses', 'doch', 'dort', 'du', 'durch', 'ein', 'eine', 'einem',
    'einen', 'einer', 'er', 'es', 'etwas', 'euch', 'euer', 'eure', 'eurem',
    'euren', 'eurer', 'für', 'gegen', 'hab', 'habe', 'haben', 'hat', 'hatte',
    'ich', 'ihm', 'ihn', 'ihnen', 'ihr', 'ihre', 'ihrem', 'ihren', 'ihrer',
    'im', 'in', 'ist', 'jede', 'jedem', 'jeden', 'jeder', 'jedes', 'kann',
    'kein', 'keine', 'keinem', 'keinen', 'keiner', 'man', 'mein', 'meine',
    'meinem', 'meinen', 'meiner', 'mit', 'muss', 'müssen', 'nach', 'nicht', 'nichts',
    'noch', 'nun', 'nur', 'ob', 'oder', 'ohne', 'sehr', 'sein', 'seine',
    'seinem', 'seinen', 'seiner', 'sich', 'sie', 'sind', 'so', 'soll', 'und',
    'uns', 'unser', 'unsere', 'unserem', 'unseren', 'unserer', 'unter', 'um',
    'von', 'vor', 'was', 'weil', 'welch', 'welche', 'welchem', 'welchen',
    'welcher', 'wenn', 'wer', 'werde', 'wie', 'will', 'wir', 'wird', 'wo',
    'würde', 'zu', 'zum', 'zur', 'über',
  ],
  pt: [
    'a', 'ao', 'aos', 'aquela', 'aquelas', 'aquele', 'aqueles', 'aquilo', 'as',
    'com', 'como', 'da', 'das', 'de', 'dela', 'delas', 'dele', 'deles', 'depois',
    'do', 'dos', 'e', 'ela', 'elas', 'ele', 'eles', 'em', 'entre', 'era', 'essa',
    'essas', 'esse', 'esses', 'esta', 'estas', 'este', 'estes', 'eu', 'foi',
    'há', 'isso', 'isto', 'já', 'lhe', 'lhes', 'lo', 'mais', 'mas', 'me', 'mesmo',
    'meu', 'minha', 'muito', 'na', 'nas', 'nem', 'no', 'nos', 'nossa', 'nossas',
    'nosso', 'nossos', 'num', 'numa', 'nuns', 'o', 'os', 'ou', 'para', 'pela',
    'pelas', 'pelo', 'pelos', 'por', 'qual', 'quando', 'que', 'quem', 'se', 'sem',
    'ser', 'seu', 'seus', 'sua', 'suas', 'também', 'te', 'tem', 'teu', 'teus',
    'tu', 'tua', 'tuas', 'um', 'uma', 'umas', 'uns', 'você', 'vocês', 'vós',
  ],
  it: [
    'a', 'abbiamo', 'ad', 'ai', 'al', 'alla', 'alle', 'allo', 'anche', 'avere',
    'aveva', 'che', 'chi', 'ci', 'come', 'con', 'contro', 'cui', 'da', 'dal',
    'dalla', 'dalle', 'dallo', 'degli', 'dei', 'del', 'dell', 'della', 'delle',
    'dello', 'di', 'dopo', 'e', 'era', 'essere', 'fatto', 'fra', 'fu', 'gli',
    'ha', 'hai', 'hanno', 'ho', 'i', 'il', 'in', 'io', 'l', 'la', 'le', 'lei',
    'li', 'lo', 'loro', 'lui', 'ma', 'me', 'mi', 'mia', 'mie', 'miei', 'mio',
    'ne', 'nei', 'nel', 'nella', 'nelle', 'nello', 'no', 'noi', 'non', 'nostra',
    'nostre', 'nostri', 'nostro', 'o', 'per', 'piu', 'quale', 'quando', 'quello',
    'questa', 'queste', 'questi', 'questo', 'se', 'si', 'sia', 'sono', 'su',
    'sua', 'sue', 'sui', 'sul', 'sulla', 'sulle', 'sullo', 'suo', 'suoi', 'ti',
    'tra', 'tu', 'tua', 'tue', 'tuo', 'tuoi', 'tutti', 'tutto', 'un', 'una',
    'uno', 'vi', 'voi', 'vostra', 'vostre', 'vostri', 'vostro',
  ],
  nl: [
    'aan', 'al', 'alles', 'als', 'bij', 'daar', 'dan', 'dat', 'de', 'der', 'die',
    'dit', 'doch', 'doen', 'door', 'dus', 'een', 'en', 'er', 'ge', 'geen', 'haar',
    'had', 'heeft', 'hem', 'het', 'hier', 'hij', 'hoe', 'hun', 'iets', 'ik', 'in',
    'is', 'ja', 'je', 'kan', 'kon', 'maar', 'me', 'meer', 'men', 'met', 'mij',
    'mijn', 'moet', 'na', 'naar', 'niet', 'niets', 'nog', 'nu', 'of', 'om', 'omdat',
    'ook', 'op', 'over', 'reeds', 'te', 'tegen', 'toch', 'toen', 'tot', 'u', 'uit',
    'uw', 'van', 'veel', 'voor', 'want', 'waren', 'was', 'wat', 'we', 'wel',
    'werd', 'wij', 'wil', 'worden', 'wordt', 'zal', 'ze', 'zelf', 'zich', 'zij',
    'zijn', 'zo', 'zou',
  ],
  pl: [
    'a', 'ale', 'ani', 'az', 'bardzo', 'bez', 'bo', 'by', 'byla', 'byli', 'bylo',
    'byl', 'byc', 'co', 'czy', 'dla', 'do', 'go', 'i', 'ich', 'jak', 'jako', 'ja',
    'je', 'jednak', 'jego', 'jej', 'jest', 'jeszcze', 'juz', 'kiedy', 'ku', 'ma',
    'mi', 'mnie', 'moze', 'mozna', 'mu', 'na', 'nad', 'nam', 'nas', 'nie', 'nich',
    'nic', 'nim', 'no', 'o', 'od', 'on', 'ona', 'oni', 'ono', 'oraz', 'po', 'pod',
    'przez', 'przy', 'sa', 'sie', 'so', 'ta', 'tak', 'tam', 'te', 'tego', 'tej',
    'ten', 'to', 'tu', 'tylko', 'ty', 'tych', 'tym', 'w', 'wie', 'wiele', 'wszystko',
    'z', 'za', 'ze',
  ],
  sv: [
    'alla', 'allt', 'att', 'av', 'bara', 'bli', 'blivit', 'då', 'dag', 'de', 'den',
    'denna', 'det', 'detta', 'dig', 'din', 'dina', 'dit', 'dock', 'du', 'efter',
    'eller', 'en', 'er', 'era', 'ett', 'finns', 'för', 'från', 'gav', 'ger',
    'göra', 'ha', 'hade', 'han', 'hans', 'har', 'hen', 'hennes', 'hon', 'honom',
    'hur', 'i', 'in', 'ingen', 'inte', 'ja', 'jag', 'kan', 'kunde', 'man', 'med',
    'men', 'mig', 'min', 'mina', 'mot', 'mycket', 'ni', 'någon', 'något', 'nu',
    'när', 'och', 'om', 'oss', 'på', 'så', 'sig', 'sin', 'sina', 'ska', 'skall',
    'som', 'till', 'under', 'upp', 'ur', 'ut', 'vad', 'var', 'vara', 'vi', 'vid',
    'vill', 'är',
  ],
  da: [
    'ad', 'af', 'aldrig', 'alle', 'alt', 'anden', 'andet', 'andre', 'at', 'bare',
    'begge', 'blev', 'da', 'de', 'dem', 'den', 'denne', 'der', 'deres', 'det',
    'dette', 'dig', 'din', 'dine', 'disse', 'dit', 'dog', 'du', 'efter', 'eller',
    'en', 'end', 'er', 'et', 'for', 'fordi', 'fra', 'gik', 'gøre', 'han', 'hans',
    'har', 'havde', 'hen', 'hende', 'hendes', 'her', 'hos', 'hun', 'hvad',
    'hvem', 'hver', 'hvilken', 'hvis', 'hvor', 'i', 'igen', 'ikke', 'ind', 'ingen',
    'ja', 'jeg', 'kan', 'kom', 'kunne', 'lidt', 'man', 'mange', 'med', 'meget',
    'men', 'mig', 'min', 'mine', 'mit', 'mod', 'ned', 'nej', 'noget', 'nogle',
    'nu', 'når', 'og', 'op', 'os', 'over', 'på', 'så', 'selv', 'sig', 'sin',
    'sine', 'sit', 'skal', 'skulle', 'som', 'til', 'ud', 'var',
  ],
  tr: [
    'acaba', 'ama', 'ancak', 'bana', 'bir', 'biri', 'birisi', 'biz', 'bu', 'bunu',
    'da', 'daha', 'de', 'defa', 'diye', 'eger', 'en', 'gibi', 'hem', 'henuz',
    'hep', 'hepsi', 'her', 'herkes', 'hi', 'ic', 'icin', 'ile', 'ise', 'ki',
    'kim', 'kimi', 'mu', 'nasil', 'ne', 'neden', 'nerde', 'nerede', 'nereye',
    'niye', 'o', 'ona', 'onlar', 'onu', 'onun', 'sana', 'sen', 'siz', 'su',
    've', 'veya', 'ya', 'yani',
  ],
  ja: [], // Japanese doesn't use whitespace tokenization — skip stopwords
  zh: [], // Chinese doesn't use whitespace tokenization — skip stopwords
  ko: [], // Korean may need morphological analysis — skip stopwords
};

// Common web/navigation terms to filter in all languages
const WEB_STOPWORDS = [
  'home', 'page', 'click', 'read', 'next', 'previous', 'back', 'top',
  'menu', 'close', 'open', 'search', 'login', 'sign', 'submit', 'loading',
];

// Build a combined stopword set for a given language
function getStopwords(language?: string): Set<string> {
  const words = new Set<string>();
  // Always include English stopwords as baseline (many sites mix languages)
  for (const w of STOPWORDS.en) words.add(w);
  // Add language-specific stopwords if available
  if (language && language !== 'en' && STOPWORDS[language]) {
    for (const w of STOPWORDS[language]) words.add(w);
  }
  // Add web navigation terms
  for (const w of WEB_STOPWORDS) words.add(w);
  return words;
}

/** Supported language codes for stopwords */
export const SUPPORTED_LANGUAGES = Object.keys(STOPWORDS).filter(k => STOPWORDS[k].length > 0);

// Maximum unique terms to track per gram size (memory cap)
const MAX_ENTRIES = 50_000;
/** Characters of body text read per page; the rest is ignored (hostile pages can be huge). */
export const MAX_PAGE_CHARS = 200_000;
/** Tokens used per page after stopword filtering. */
export const MAX_PAGE_TOKENS = 20_000;

/**
 * Tokenize text: lowercase, strip punctuation, split on whitespace.
 * Returns only tokens with 2+ characters that are not stop words.
 * Supports accented characters for multilingual text.
 * Reads at most MAX_PAGE_CHARS characters and returns at most MAX_PAGE_TOKENS tokens.
 */
function tokenize(text: string, stopwords: Set<string>): string[] {
  const cleaned = text
    .slice(0, MAX_PAGE_CHARS)
    .toLowerCase()
    .replace(/[^\p{L}\p{N}\s'-]/gu, ' ');  // keep letters (any script), numbers, apostrophes, hyphens
  const tokens: string[] = [];
  for (const raw of cleaned.split(/\s+/)) {
    const w = raw.replace(/^['-]+|['-]+$/g, '');  // strip leading/trailing punctuation
    if (w.length >= 2 && !stopwords.has(w) && !/^\d+$/.test(w)) {
      tokens.push(w);
      if (tokens.length >= MAX_PAGE_TOKENS) break;
    }
  }
  return tokens;
}

/**
 * Incremental n-gram analyzer. Call addPage() for each page during crawl,
 * then getResults() at the end for site-wide statistics.
 */
export class NgramAnalyzer {
  private unigrams = new Map<string, { count: number; pages: number }>();
  private bigrams = new Map<string, { count: number; pages: number }>();
  private trigrams = new Map<string, { count: number; pages: number }>();
  private totalPages = 0;
  private totalTokens = 0;
  private stopwords: Set<string>;
  private language: string;

  constructor(language?: string) {
    this.language = language || 'en';
    this.stopwords = getStopwords(this.language);
  }

  /** Update language (e.g., after detecting from first page's <html lang>) */
  setLanguage(lang: string): void {
    if (lang !== this.language && STOPWORDS[lang]) {
      this.language = lang;
      this.stopwords = getStopwords(lang);
    }
  }

  getLanguage(): string {
    return this.language;
  }

  /**
   * Process a page's body text and update n-gram counts.
   */
  addPage(bodyText: string): void {
    if (!bodyText || bodyText.length < 50) return;

    const tokens = tokenize(bodyText, this.stopwords);
    if (tokens.length < 3) return;

    this.totalPages++;
    this.totalTokens += tokens.length;

    // Count this page's terms locally (bounded by MAX_PAGE_TOKENS), then
    // merge into the site-wide maps, pruning as soon as a map hits its cap.
    const pageUnigrams = new Map<string, number>();
    const pageBigrams = new Map<string, number>();
    const pageTrigrams = new Map<string, number>();
    for (let i = 0; i < tokens.length; i++) {
      bumpLocal(pageUnigrams, tokens[i]);
      if (i + 1 < tokens.length) bumpLocal(pageBigrams, `${tokens[i]} ${tokens[i + 1]}`);
      if (i + 2 < tokens.length) bumpLocal(pageTrigrams, `${tokens[i]} ${tokens[i + 1]} ${tokens[i + 2]}`);
    }
    this.merge(this.unigrams, pageUnigrams);
    this.merge(this.bigrams, pageBigrams);
    this.merge(this.trigrams, pageTrigrams);
  }

  private merge(
    map: Map<string, { count: number; pages: number }>,
    page: Map<string, number>,
  ): void {
    for (const [term, count] of page) {
      const entry = map.get(term);
      if (entry) {
        entry.count += count;
        entry.pages++;
        continue;
      }
      if (map.size >= MAX_ENTRIES) this.prune(map);
      map.set(term, { count, pages: 1 });
    }
  }

  /**
   * Get the final n-gram analysis results.
   */
  getResults(topN = 50): NgramStats {
    return {
      unigrams: this.buildEntries(this.unigrams, topN),
      bigrams: this.buildEntries(this.bigrams, topN),
      trigrams: this.buildEntries(this.trigrams, topN),
      totalPages: this.totalPages,
      totalTokens: this.totalTokens,
    };
  }

  /**
   * Free memory after results have been extracted.
   */
  clear(): void {
    this.unigrams.clear();
    this.bigrams.clear();
    this.trigrams.clear();
    this.totalPages = 0;
    this.totalTokens = 0;
  }

  private buildEntries(
    map: Map<string, { count: number; pages: number }>,
    topN: number,
  ): NgramEntry[] {
    const entries: NgramEntry[] = [];

    for (const [term, data] of map) {
      // Skip terms that appear on fewer than 2 pages (likely page-specific noise)
      if (data.pages < 2 && this.totalPages > 5) continue;

      // TF-IDF: (term frequency) * log(total docs / doc frequency)
      const tf = data.count / Math.max(this.totalTokens, 1);
      const idf = Math.log(Math.max(this.totalPages, 1) / Math.max(data.pages, 1));
      const tfidf = Math.round(tf * idf * 100000) / 100000;

      entries.push({
        term,
        count: data.count,
        pages: data.pages,
        tfidf,
      });
    }

    // Sort by count (most frequent first)
    entries.sort((a, b) => b.count - a.count);
    return entries.slice(0, topN);
  }

  /**
   * Shrink a full map to 75% of MAX_ENTRIES by removing lowest-count entries.
   * Called before an insert would go past MAX_ENTRIES, so the map (and the
   * sort here) never grows beyond the cap.
   */
  private prune(map: Map<string, { count: number; pages: number }>): void {
    if (map.size < MAX_ENTRIES) return;
    const entries = Array.from(map.entries())
      .sort(([, a], [, b]) => b.count - a.count);

    const keepCount = Math.floor(MAX_ENTRIES * 0.75);
    map.clear();
    for (let i = 0; i < keepCount && i < entries.length; i++) {
      map.set(entries[i][0], entries[i][1]);
    }
  }

  /** Largest number of terms held for any gram size (for tests and diagnostics). */
  get maxTrackedTerms(): number {
    return Math.max(this.unigrams.size, this.bigrams.size, this.trigrams.size);
  }
}

function bumpLocal(map: Map<string, number>, term: string): void {
  map.set(term, (map.get(term) ?? 0) + 1);
}
