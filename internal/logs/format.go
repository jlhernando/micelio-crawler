package logs

import (
	"encoding/csv"
	"encoding/json"
	"regexp"
	"strconv"
	"strings"
	"time"
)

type Format string

const (
	FormatApacheCLF      Format = "apache_clf"
	FormatApacheCombined Format = "apache_combined"
	FormatNginxCombined  Format = "nginx_combined"
	FormatCloudFront     Format = "cloudfront"
	FormatCloudflare     Format = "cloudflare"
	FormatALB            Format = "alb"
	FormatW3C            Format = "w3c"
	FormatCustom         Format = "custom"
	FormatUnknown        Format = "unknown"
)

var (
	// Apache/Nginx Combined: host - user [time] "method path proto" status bytes "referer" "ua"
	reCombined = regexp.MustCompile(`^(\S+) \S+ \S+ \[([^\]]+)\] "(\S+) ([^"]*?) \S*" (\d{3}) (\S+) "([^"]*)" "([^"]*)"`)
	// Apache CLF: host - user [time] "method path proto" status bytes
	reCLF = regexp.MustCompile(`^(\S+) \S+ \S+ \[([^\]]+)\] "(\S+) ([^"]*?) \S*" (\d{3}) (\S+)`)
	// ALB: type timestamp elb client:port target:port request_processing_time target_processing_time response_processing_time elb_status_code target_status_code received_bytes sent_bytes "request" "user_agent" ...
	reALB = regexp.MustCompile(`^\S+ (\S+) \S+ (\S+:\d+) \S+:\d+ [\d.-]+ [\d.-]+ [\d.-]+ (\d{3}) \d{3} \d+ (\d+) "(\S+) ([^ ]+) [^"]*" "([^"]*)"`)
)

const timeLayout = "02/Jan/2006:15:04:05 -0700"

// CustomFormatDef defines a user-provided log format with named regex groups.
type CustomFormatDef struct {
	Name    string `json:"name"`
	Pattern string `json:"pattern"`
	re      *regexp.Regexp
}

// CompileCustomFormat validates and compiles a custom format regex.
// Required named groups: ip, timestamp, method, path, status, bytes.
// Optional: referer, useragent, response_time.
func CompileCustomFormat(pattern string) (*CustomFormatDef, error) {
	re, err := regexp.Compile(pattern)
	if err != nil {
		return nil, err
	}
	names := re.SubexpNames()
	required := []string{"ip", "timestamp", "method", "path", "status", "bytes"}
	for _, req := range required {
		found := false
		for _, n := range names {
			if n == req {
				found = true
				break
			}
		}
		if !found {
			return nil, parseError("custom format missing required group: " + req)
		}
	}
	return &CustomFormatDef{Pattern: pattern, re: re}, nil
}

// knownTSVHeaderTokens is a vocabulary of column names commonly found in
// header-row TSV access logs (CloudFront CSV exports, Athena-style dumps, etc.).
// All entries are lower-case.
var knownTSVHeaderTokens = map[string]bool{
	// timestamps
	"date": true, "time": true, "timestamp": true, "datetime": true,
	// client
	"c-ip": true, "client-ip": true, "client_ip": true, "request_ip": true, "remote_ip": true, "remote_addr": true,
	// request line
	"cs-method": true, "method": true, "cs(method)": true,
	"cs-uri-stem": true, "uri": true, "uri_stem": true, "path": true, "url": true,
	"cs-uri-query": true, "query_string": true, "query": true,
	// response
	"sc-status": true, "status": true, "status_code": true,
	"sc-bytes": true, "bytes": true, "response_bytes": true, "bytes_sent": true,
	// headers
	"cs(user-agent)": true, "cs-user-agent": true, "user-agent": true, "user_agent": true, "useragent": true,
	"cs(referer)": true, "cs-referer": true, "referer": true, "referrer": true,
	"cs(host)": true, "host": true, "host_header": true,
	// cloudfront extras
	"x-edge-location": true, "location": true, "edge_location": true,
	"x-edge-result-type": true, "result_type": true,
	"x-edge-request-id": true, "request_id": true,
	"time-taken": true, "time_taken": true,
}

// stripBOM removes a leading UTF-8 byte-order mark, common in CSV/TSV files
// exported from Windows tools (Athena → S3 → download).
func stripBOM(s string) string {
	return strings.TrimPrefix(s, "\ufeff")
}

// detectTSVHeader checks whether a line looks like a tab-separated header row
// containing known column names. Returns the split fields if it's a header,
// or nil otherwise.
func detectTSVHeader(line string) []string {
	line = stripBOM(line)
	if !strings.ContainsRune(line, '\t') {
		return nil
	}
	parts := strings.Split(line, "\t")
	if len(parts) < 6 {
		return nil
	}
	known := 0
	for i, p := range parts {
		normalized := stripBOM(strings.ToLower(strings.TrimSpace(p)))
		parts[i] = stripBOM(strings.TrimSpace(p)) // normalize stored value too
		if knownTSVHeaderTokens[normalized] {
			known++
		}
	}
	// At least 4 recognizable column names, and at least 40% of columns known
	if known < 4 || known*10 < len(parts)*4 {
		return nil
	}
	return parts
}

// DetectFormat tries each format against sample lines, returns the best match.
// If a header row (W3C #Fields, TSV, or CSV) is detected, it's captured on
// the default ParseState for backward compatibility. For concurrent parses,
// use DetectFormatWithState.
func DetectFormat(lines []string) Format {
	return DetectFormatWithState(lines, defaultParseState)
}

// DetectFormatWithState is like DetectFormat but stores captured header fields
// on the provided state, so each concurrent worker keeps its own.
func DetectFormatWithState(lines []string, s *ParseState) Format {
	s = resolveState(s)
	combined, clf, cfJSON, alb, w3c := 0, 0, 0, 0, 0
	hasW3CHeader := false
	tsvHeaderDetected := false
	firstContentSeen := false
	for _, line := range lines {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		// W3C #Fields directive
		if strings.HasPrefix(line, "#Fields:") {
			s.SetW3C(line)
			hasW3CHeader = true
			continue
		}
		// CloudFront version header
		if strings.HasPrefix(line, "#Version:") {
			hasW3CHeader = true
			continue
		}
		if line[0] == '#' {
			continue
		}
		// Header-row TSV / CSV (CloudFront export / Athena-style dumps):
		// first non-comment line is a tab- or comma-separated list of known column names.
		if !firstContentSeen && !hasW3CHeader {
			if fields := detectTSVHeader(line); fields != nil {
				s.SetTSV(fields)
				tsvHeaderDetected = true
				firstContentSeen = true
				continue
			}
			if fields := detectCSVHeader(line); fields != nil {
				s.SetCSV(fields)
				tsvHeaderDetected = true
				firstContentSeen = true
				continue
			}
		}
		firstContentSeen = true
		// JSON line (Cloudflare)
		if line[0] == '{' {
			cfJSON++
			continue
		}
		// ALB format starts with h2/http/https/ws/wss
		if reALB.MatchString(line) {
			alb++
			continue
		}
		// W3C tab/space separated (after #Fields header detected)
		if hasW3CHeader {
			w3c++
			continue
		}
		if reCombined.MatchString(line) {
			combined++
		} else if reCLF.MatchString(line) {
			clf++
		}
	}
	// Header-row TSV/CSV takes precedence — CloudFront CSV exports are common
	if tsvHeaderDetected {
		return FormatCloudFront
	}
	// Pick format with most matches
	best := FormatUnknown
	bestCount := 0
	for f, c := range map[Format]int{
		FormatApacheCombined: combined,
		FormatApacheCLF:      clf,
		FormatCloudflare:     cfJSON,
		FormatALB:            alb,
	} {
		if c > bestCount {
			best = f
			bestCount = c
		}
	}
	if w3c > 0 && hasW3CHeader && w3c >= bestCount {
		return FormatW3C
	}
	return best
}

// ParseLine parses a single log line into a LogEntry using the package-level
// default ParseState. For concurrent parses use ParseLineWithState.
func ParseLine(line string, format Format) (*LogEntry, error) {
	return ParseLineWithState(line, format, defaultParseState)
}

// ParseLineWithState is like ParseLine but uses the caller-provided state
// for CloudFront/W3C header-driven parsing.
func ParseLineWithState(line string, format Format, s *ParseState) (*LogEntry, error) {
	s = resolveState(s)
	switch format {
	case FormatApacheCombined, FormatNginxCombined:
		return parseCombined(line)
	case FormatApacheCLF:
		return parseCLF(line)
	case FormatCloudFront:
		return parseCloudFront(line, s)
	case FormatCloudflare:
		return parseCloudflare(line)
	case FormatALB:
		return parseALB(line)
	case FormatW3C:
		return parseW3C(line, s)
	default:
		if e, err := parseCombined(line); err == nil {
			return e, nil
		}
		return parseCLF(line)
	}
}

// ParseLineCustom parses a line using a custom format definition.
func ParseLineCustom(line string, def *CustomFormatDef) (*LogEntry, error) {
	m := def.re.FindStringSubmatch(line)
	if m == nil {
		return nil, errNoMatch
	}
	names := def.re.SubexpNames()
	fields := make(map[string]string, len(names))
	for i, name := range names {
		if name != "" && i < len(m) {
			fields[name] = m[i]
		}
	}
	t := parseTimestampFlex(fields["timestamp"])
	status, _ := strconv.Atoi(fields["status"])
	bytes := parseBytes(fields["bytes"])
	e := &LogEntry{
		IP:        fields["ip"],
		Timestamp: t,
		Method:    fields["method"],
		Path:      fields["path"],
		Status:    status,
		Bytes:     bytes,
		Referer:   fields["referer"],
		UserAgent: fields["useragent"],
	}
	if rt, ok := fields["response_time"]; ok {
		e.ResponseTime, _ = strconv.ParseFloat(rt, 64)
	}
	return e, nil
}

func parseCombined(line string) (*LogEntry, error) {
	m := reCombined.FindStringSubmatch(line)
	if m == nil {
		return nil, errNoMatch
	}
	t, err := time.Parse(timeLayout, m[2])
	if err != nil {
		return nil, err
	}
	status, _ := strconv.Atoi(m[5])
	bytes := parseBytes(m[6])
	return &LogEntry{
		IP:        m[1],
		Timestamp: t,
		Method:    m[3],
		Path:      m[4],
		Status:    status,
		Bytes:     bytes,
		Referer:   m[7],
		UserAgent: m[8],
	}, nil
}

func parseCLF(line string) (*LogEntry, error) {
	m := reCLF.FindStringSubmatch(line)
	if m == nil {
		return nil, errNoMatch
	}
	t, err := time.Parse(timeLayout, m[2])
	if err != nil {
		return nil, err
	}
	status, _ := strconv.Atoi(m[5])
	bytes := parseBytes(m[6])
	return &LogEntry{
		IP:        m[1],
		Timestamp: t,
		Method:    m[3],
		Path:      m[4],
		Status:    status,
		Bytes:     bytes,
	}, nil
}

// parseCloudFront handles AWS CloudFront tab-separated log format.
// Canonical column order: date time x-edge-location sc-bytes c-ip cs-method cs(Host) cs-uri-stem sc-status cs(Referer) cs(User-Agent) cs-uri-query ...
// If a header row was detected during format sniffing (w3cParser.fields set),
// parses by column name instead — this handles CloudFront CSV exports with
// snake_case headers like "date time location bytes request_ip method host uri status referrer user_agent query_string ...".
func parseCloudFront(line string, s *ParseState) (*LogEntry, error) {
	s = resolveState(s)
	if len(s.Fields) > 0 {
		var parts []string
		switch s.Separator {
		case ',':
			rec, err := parseCSVLine(line)
			if err != nil {
				return nil, err
			}
			parts = rec
		default:
			parts = strings.Split(line, "\t")
		}
		if len(parts) < 4 {
			return nil, errNoMatch
		}
		return parseTSVWithHeader(parts, s)
	}
	// Positional fallback (standard CloudFront order, no header seen).
	fields := strings.Split(line, "\t")
	if len(fields) < 13 {
		return nil, errNoMatch
	}
	t, err := time.Parse("2006-01-02\t15:04:05", fields[0]+"\t"+fields[1])
	if err != nil {
		return nil, err
	}
	status, _ := strconv.Atoi(fields[8])
	bytes := parseBytes(fields[3])
	path := fields[7]
	if fields[11] != "-" {
		path = path + "?" + fields[11]
	}
	ua := decodePlus(fields[10])
	referer := fields[9]
	if referer == "-" {
		referer = ""
	}
	return &LogEntry{
		IP:        fields[4],
		Timestamp: t,
		Method:    fields[5],
		Path:      path,
		Status:    status,
		Bytes:     bytes,
		Referer:   referer,
		UserAgent: ua,
	}, nil
}

// parseTSVWithHeader parses a tab-split line using a previously-captured
// header field list. Accepts both canonical W3C/CloudFront names
// (cs-uri-stem, sc-status, cs(User-Agent)) and snake_case CSV-export names
// (uri, status, user_agent) via coalesce().
func parseTSVWithHeader(parts []string, s *ParseState) (*LogEntry, error) {
	// Use pre-built field index for O(1) column lookups — avoids allocating
	// a temporary map[string]string for every line (~28M allocations per file).
	idx := s.fieldIdx
	field := func(names ...string) string {
		for _, n := range names {
			if i, ok := idx[n]; ok && i < len(parts) {
				return parts[i]
			}
		}
		return ""
	}

	dateStr := field("date", "timestamp", "datetime")
	timeStr := field("time")
	var t time.Time
	if dateStr != "" && timeStr != "" {
		if parsed, err := time.Parse("2006-01-02\t15:04:05", dateStr+"\t"+timeStr); err == nil {
			t = parsed
		} else {
			t = parseTimestampFlex(dateStr + " " + timeStr)
		}
	} else if dateStr != "" {
		t = parseTimestampFlex(dateStr)
	}
	status, _ := strconv.Atoi(field("sc-status", "status", "status_code"))
	bytesStr := field("sc-bytes", "bytes", "response_bytes", "bytes_sent")
	bytes := parseBytes(bytesStr)
	path := field("cs-uri-stem", "uri", "uri_stem", "path", "url")
	query := field("cs-uri-query", "query_string", "query")
	if query != "" && query != "-" {
		path = path + "?" + query
	}
	ua := decodePlus(field("cs(user-agent)", "cs-user-agent", "user_agent", "user-agent", "useragent"))
	referer := decodePlus(field("cs(referer)", "cs-referer", "referer", "referrer"))
	if referer == "-" {
		referer = ""
	}
	ip := field("c-ip", "client-ip", "client_ip", "request_ip", "remote_ip", "remote_addr")
	method := field("cs-method", "method", "cs(method)")
	var respTime float64
	if rt := field("time-taken", "time_taken"); rt != "" && rt != "-" {
		respTime, _ = strconv.ParseFloat(rt, 64)
	}
	if path == "" && ip == "" {
		return nil, errNoMatch
	}
	return &LogEntry{
		IP:           ip,
		Timestamp:    t,
		Method:       method,
		Path:         path,
		Status:       status,
		Bytes:        bytes,
		Referer:      referer,
		UserAgent:    ua,
		ResponseTime: respTime,
	}, nil
}

// decodePlus unescapes common URL-encoded whitespace in CloudFront/W3C fields
// (space → %20 → %2520 depending on re-encoding layers; W3C also uses '+').
func decodePlus(s string) string {
	if s == "" || s == "-" {
		return s
	}
	s = strings.ReplaceAll(s, "%2520", " ")
	s = strings.ReplaceAll(s, "%20", " ")
	s = strings.ReplaceAll(s, "+", " ")
	return s
}

// parseCloudflare handles Cloudflare JSON log format (one JSON object per line).
func parseCloudflare(line string) (*LogEntry, error) {
	var obj struct {
		ClientIP               string `json:"ClientIP"`
		ClientRequestHost      string `json:"ClientRequestHost"`
		ClientRequestMethod    string `json:"ClientRequestMethod"`
		ClientRequestURI       string `json:"ClientRequestURI"`
		ClientRequestPath      string `json:"ClientRequestPath"`
		EdgeResponseStatus     int    `json:"EdgeResponseStatus"`
		EdgeResponseBytes      int64  `json:"EdgeResponseBytes"`
		ClientRequestReferer   string `json:"ClientRequestReferer"`
		ClientRequestUserAgent string `json:"ClientRequestUserAgent"`
		EdgeStartTimestamp     int64  `json:"EdgeStartTimestamp"`
		EdgeEndTimestamp       int64  `json:"EdgeEndTimestamp"`
	}
	if err := json.Unmarshal([]byte(line), &obj); err != nil {
		return nil, err
	}
	var t time.Time
	if obj.EdgeStartTimestamp > 0 {
		// Cloudflare timestamps are nanoseconds
		if obj.EdgeStartTimestamp > 1e15 {
			t = time.Unix(0, obj.EdgeStartTimestamp)
		} else {
			t = time.Unix(obj.EdgeStartTimestamp, 0)
		}
	}
	path := obj.ClientRequestURI
	if path == "" {
		path = obj.ClientRequestPath
	}
	var respTime float64
	if obj.EdgeEndTimestamp > 0 && obj.EdgeStartTimestamp > 0 {
		respTime = cloudflareTimestampDeltaSeconds(obj.EdgeStartTimestamp, obj.EdgeEndTimestamp)
	}
	return &LogEntry{
		IP:           obj.ClientIP,
		Timestamp:    t,
		Method:       obj.ClientRequestMethod,
		Path:         path,
		Status:       obj.EdgeResponseStatus,
		Bytes:        obj.EdgeResponseBytes,
		Referer:      obj.ClientRequestReferer,
		UserAgent:    obj.ClientRequestUserAgent,
		ResponseTime: respTime,
	}, nil
}

func cloudflareTimestampDeltaSeconds(start, end int64) float64 {
	if end <= start {
		return 0
	}
	delta := end - start
	if start > 1e15 || end > 1e15 {
		return float64(delta) / 1e9
	}
	return float64(delta)
}

// parseALB handles AWS ALB/ELB access log format.
func parseALB(line string) (*LogEntry, error) {
	m := reALB.FindStringSubmatch(line)
	if m == nil {
		return nil, errNoMatch
	}
	// m[1]=timestamp m[2]=client:port m[3]=elb_status m[4]=sent_bytes m[5]=method m[6]=url m[7]=user_agent
	t, err := time.Parse("2006-01-02T15:04:05.000000Z", m[1])
	if err != nil {
		t, err = time.Parse(time.RFC3339, m[1])
		if err != nil {
			return nil, err
		}
	}
	clientIP := m[2]
	if idx := strings.LastIndex(clientIP, ":"); idx > 0 {
		clientIP = clientIP[:idx]
	}
	status, _ := strconv.Atoi(m[3])
	bytes := parseBytes(m[4])
	path := m[6]
	// ALB URLs are full URLs, extract path
	if strings.HasPrefix(path, "http") {
		if u := strings.SplitN(path, "/", 4); len(u) >= 4 {
			path = "/" + u[3]
		}
	}
	return &LogEntry{
		IP:        clientIP,
		Timestamp: t,
		Method:    m[5],
		Path:      path,
		Status:    status,
		Bytes:     bytes,
		UserAgent: m[7],
	}, nil
}

// ParseState holds per-parse mutable state (header field list + separator).
// Each concurrent parser must use its own ParseState; passing nil falls back
// to the package-level default for backward compatibility.
type ParseState struct {
	Fields    []string
	Separator rune // defaults to '\t'; set to ',' for CSV exports
	// fieldIdx maps lowercase field names to their column index.
	// Built once during header setup to avoid per-line map allocations.
	fieldIdx map[string]int
}

// defaultParseState is the package-level fallback used by the single-file
// public API (ParseLine, SetW3CFields, etc.). Not safe for concurrent use
// across different parses.
var defaultParseState = &ParseState{}

func resolveState(s *ParseState) *ParseState {
	if s == nil {
		return defaultParseState
	}
	return s
}

// Reset clears captured header fields on this state.
func (s *ParseState) Reset() {
	s.Fields = nil
	s.Separator = 0
	s.fieldIdx = nil
}

// buildFieldIndex creates a lookup map from lowercase field names to
// column indices. Called once during header setup.
func (s *ParseState) buildFieldIndex() {
	s.fieldIdx = make(map[string]int, len(s.Fields))
	for i, name := range s.Fields {
		s.fieldIdx[strings.ToLower(strings.TrimSpace(name))] = i
	}
}

// SetW3C populates this state from a #Fields directive line.
func (s *ParseState) SetW3C(fieldsLine string) {
	fieldsLine = strings.TrimPrefix(fieldsLine, "#Fields:")
	s.Fields = strings.Fields(strings.TrimSpace(fieldsLine))
	s.Separator = '\t'
	s.buildFieldIndex()
}

// SetTSV populates this state from an already-split TSV header row.
func (s *ParseState) SetTSV(fields []string) {
	out := make([]string, 0, len(fields))
	for _, f := range fields {
		out = append(out, strings.TrimSpace(f))
	}
	s.Fields = out
	s.Separator = '\t'
	s.buildFieldIndex()
}

// SetCSV populates this state from a comma-separated CSV header row.
// Subsequent lines are parsed with encoding/csv so quoted fields with
// embedded commas are handled.
func (s *ParseState) SetCSV(fields []string) {
	out := make([]string, 0, len(fields))
	for _, f := range fields {
		out = append(out, strings.TrimSpace(f))
	}
	s.Fields = out
	s.Separator = ','
	s.buildFieldIndex()
}

// SetW3CFields sets the field order on the default (package-level) state
// from a #Fields directive line. Kept for backward compatibility.
func SetW3CFields(fieldsLine string) { defaultParseState.SetW3C(fieldsLine) }

// SetTSVFields sets the field order on the default state (backward compat).
func SetTSVFields(fields []string) { defaultParseState.SetTSV(fields) }

// SetCSVFields sets the field order on the default state (backward compat).
func SetCSVFields(fields []string) { defaultParseState.SetCSV(fields) }

// ResetHeaderFields clears the default state's captured header fields.
func ResetHeaderFields() { defaultParseState.Reset() }

// parseCSVLine splits a single CSV row into fields using encoding/csv so that
// quoted values, escaped quotes (""), and commas inside quotes are handled.
func parseCSVLine(line string) ([]string, error) {
	// Fast path: no quotes → simple split (3-5× faster than encoding/csv).
	if !strings.ContainsRune(line, '"') {
		return strings.Split(line, ","), nil
	}
	// Fast path for simple quoting: every field is "value" with no
	// embedded commas/quotes (typical for CloudFront CSV exports).
	// Detect by checking for escaped quotes ("") or commas inside fields.
	if !strings.Contains(line, `""`) {
		return splitSimpleQuotedCSV(line), nil
	}
	r := csv.NewReader(strings.NewReader(line))
	r.LazyQuotes = true
	r.FieldsPerRecord = -1
	rec, err := r.Read()
	if err != nil {
		return nil, err
	}
	return rec, nil
}

// splitSimpleQuotedCSV splits a CSV line where fields are optionally quoted
// but contain no escaped quotes (""). Handles commas inside quoted fields
// correctly. ~3× faster than encoding/csv for this common case.
func splitSimpleQuotedCSV(line string) []string {
	var fields []string
	i := 0
	for i < len(line) {
		if line[i] == '"' {
			// Quoted field: find closing quote.
			end := strings.IndexByte(line[i+1:], '"')
			if end < 0 {
				fields = append(fields, line[i+1:])
				break
			}
			fields = append(fields, line[i+1:i+1+end])
			i += end + 2 // skip past closing quote
			if i < len(line) && line[i] == ',' {
				i++ // skip comma separator
			}
		} else {
			// Unquoted field: find comma.
			end := strings.IndexByte(line[i:], ',')
			if end < 0 {
				fields = append(fields, line[i:])
				break
			}
			fields = append(fields, line[i:i+end])
			i += end + 1
		}
	}
	return fields
}

// detectCSVHeader checks whether a line looks like a comma-separated header row
// containing known column names. Returns the split fields if so, nil otherwise.
func detectCSVHeader(line string) []string {
	line = stripBOM(line)
	if !strings.ContainsRune(line, ',') {
		return nil
	}
	parts, err := parseCSVLine(line)
	if err != nil || len(parts) < 6 {
		return nil
	}
	known := 0
	for i, p := range parts {
		normalized := stripBOM(strings.ToLower(strings.TrimSpace(p)))
		parts[i] = stripBOM(strings.TrimSpace(p))
		if knownTSVHeaderTokens[normalized] {
			known++
		}
	}
	if known < 4 || known*10 < len(parts)*4 {
		return nil
	}
	return parts
}

func parseW3C(line string, s *ParseState) (*LogEntry, error) {
	s = resolveState(s)
	parts := strings.Fields(line)
	if len(s.Fields) == 0 || len(parts) < 4 {
		return nil, errNoMatch
	}
	fields := make(map[string]string, len(s.Fields))
	for i, name := range s.Fields {
		if i < len(parts) {
			fields[name] = parts[i]
		}
	}
	// W3C field names vary: date, time, c-ip/s-ip, cs-method, cs-uri-stem, sc-status, sc-bytes, cs(User-Agent), cs(Referer)
	dateStr := fields["date"] + " " + fields["time"]
	t, err := time.Parse("2006-01-02 15:04:05", dateStr)
	if err != nil {
		t = parseTimestampFlex(dateStr)
	}
	status, _ := strconv.Atoi(coalesce(fields["sc-status"], fields["status"]))
	bytes := parseBytes(coalesce(fields["sc-bytes"], fields["bytes-sent"], fields["bytes"]))
	ua := coalesce(fields["cs(User-Agent)"], fields["cs-user-agent"], fields["user-agent"])
	ua = strings.ReplaceAll(ua, "+", " ")
	referer := coalesce(fields["cs(Referer)"], fields["cs-referer"], fields["referer"])
	if referer == "-" {
		referer = ""
	}
	return &LogEntry{
		IP:        coalesce(fields["c-ip"], fields["client-ip"], fields["s-ip"]),
		Timestamp: t,
		Method:    coalesce(fields["cs-method"], fields["method"]),
		Path:      coalesce(fields["cs-uri-stem"], fields["uri-stem"], fields["path"]),
		Status:    status,
		Bytes:     bytes,
		Referer:   referer,
		UserAgent: ua,
	}, nil
}

func coalesce(vals ...string) string {
	for _, v := range vals {
		if v != "" && v != "-" {
			return v
		}
	}
	return ""
}

// parseTimestampFlex tries multiple timestamp formats.
var flexLayouts = []string{
	timeLayout,
	"2006-01-02T15:04:05Z",
	"2006-01-02T15:04:05-07:00",
	"2006-01-02T15:04:05.000000Z",
	"2006-01-02 15:04:05",
	time.RFC3339,
	time.RFC3339Nano,
}

func parseTimestampFlex(s string) time.Time {
	for _, layout := range flexLayouts {
		if t, err := time.Parse(layout, s); err == nil {
			return t
		}
	}
	// Try Unix timestamp
	if n, err := strconv.ParseInt(s, 10, 64); err == nil {
		if n > 1e15 {
			return time.Unix(0, n)
		}
		return time.Unix(n, 0)
	}
	return time.Time{}
}

func parseBytes(s string) int64 {
	if s == "-" || s == "" {
		return 0
	}
	n, _ := strconv.ParseInt(s, 10, 64)
	return n
}

type parseError string

func (e parseError) Error() string { return string(e) }

const errNoMatch = parseError("line does not match format")
