package analysis

import (
	"strings"
)

// iso6391 is every ISO 639-1 language code (the language part of hreflang).
var iso6391 = setOf(`aa ab ae af ak am an ar as av ay az ba be bg bh bi bm bn bo br bs ca ce ch co cr cs cu cv cy
da de dv dz ee el en eo es et eu fa ff fi fj fo fr fy ga gd gl gn gu gv ha he hi ho hr ht hu hy hz ia id ie ig ii
ik io is it iu ja jv ka kg ki kj kk kl km kn ko kr ks ku kv kw ky la lb lg li ln lo lt lu lv mg mh mi mk ml mn mr
ms mt my na nb nd ne ng nl nn no nr nv ny oc oj om or os pa pi pl ps pt qu rm rn ro ru rw sa sc sd se sg si sk sl
sm sn so sq sr ss st su sv sw ta te tg th ti tk tl tn to tr ts tt tw ty ug uk ur uz ve vi vo wa wo xh yi yo za zh zu`)

// iso31661 is every ISO 3166-1 alpha-2 country code (the region part).
var iso31661 = setOf(`ad ae af ag ai al am ao aq ar as at au aw ax az ba bb bd be bf bg bh bi bj bl bm bn bo bq br bs
bt bv bw by bz ca cc cd cf cg ch ci ck cl cm cn co cr cu cv cw cx cy cz de dj dk dm do dz ec ee eg eh er es et fi fj
fk fm fo fr ga gb gd ge gf gg gh gi gl gm gn gp gq gr gs gt gu gw gy hk hm hn hr ht hu id ie il im in io iq ir is it
je jm jo jp ke kg kh ki km kn kp kr kw ky kz la lb lc li lk lr ls lt lu lv ly ma mc md me mf mg mh mk ml mm mn mo mp
mq mr ms mt mu mv mw mx my mz na nc ne nf ng ni nl no np nr nu nz om pa pe pf pg ph pk pl pm pn pr ps pt pw py qa re
ro rs ru rw sa sb sc sd se sg sh si sj sk sl sm sn so sr ss st sv sx sy sz tc td tf tg th tj tk tl tm tn to tr tt tv
tw tz ua ug um us uy uz va vc ve vg vi vn vu wf ws ye yt za zm zw`)

// Common mistakes: a country code used as the language, or a region that
// is not an ISO 3166-1 code.
var hreflangLangHints = map[string]string{
	"jp": "ja", "cn": "zh", "dk": "da", "cz": "cs", "gr": "el", "ua": "uk", "vn": "vi",
	"gb": "en", "us": "en", "at": "de", "in": "id", "iw": "he", "jw": "jv", "mo": "ro",
	"rs": "sr", "se": "sv", "si": "sl", "ee": "et", "al": "sq", "il": "he", "ir": "fa",
	"kz": "kk", "by": "be", "me": "sr", "ge": "ka", "pk": "ur", "nz": "en", "au": "en",
	"mx": "es", "ar": "es", "br": "pt", "ch": "de", "lu": "fr", "kr": "ko",
}

var hreflangRegionHints = map[string]string{"uk": "GB", "en": "GB", "eu": "", "la": "", "419": ""}

func setOf(list string) map[string]bool {
	m := make(map[string]bool, 256)
	for _, c := range strings.Fields(list) {
		m[c] = true
	}
	return m
}

// validateHreflangCode checks one hreflang value: an ISO 639-1 language,
// optionally a 4-letter script, optionally an ISO 3166-1 alpha-2 region,
// or x-default. hint says what was probably meant ("en-GB" for "en-UK").
func validateHreflangCode(code string) (valid bool, hint string) {
	c := strings.ToLower(strings.TrimSpace(code))
	if c == "" {
		return false, "empty value"
	}
	if c == "x-default" {
		return true, ""
	}
	if strings.Contains(c, "_") {
		fixed := strings.ReplaceAll(c, "_", "-")
		if ok, _ := validateHreflangCode(fixed); ok {
			return false, "use a hyphen: " + prettyHreflang(fixed)
		}
		return false, "use a hyphen, not an underscore"
	}
	parts := strings.Split(c, "-")
	lang := parts[0]
	rest := parts[1:]
	var region string
	if len(rest) > 0 && len(rest[0]) == 4 && isAlpha(rest[0]) {
		rest = rest[1:] // script, e.g. zh-Hant
	}
	if len(rest) > 1 {
		return false, "too many parts: language[-Script][-REGION]"
	}
	if len(rest) == 1 {
		region = rest[0]
	}

	if !iso6391[lang] {
		// Reversed order: "us-en".
		if region != "" && iso6391[region] && iso31661[lang] {
			return false, "language first: " + prettyHreflang(region+"-"+lang)
		}
		if fix, ok := hreflangLangHints[lang]; ok {
			s := fix
			if region != "" {
				s += "-" + region
			} else if lang != fix && iso31661[lang] {
				s += "-" + lang
			}
			return false, "not a language code, did you mean " + prettyHreflang(s) + "?"
		}
		if len(lang) == 3 {
			return false, "Google expects a 2-letter ISO 639-1 language code"
		}
		return false, "unknown language code " + lang
	}
	if region != "" && !iso31661[region] {
		if fix, ok := hreflangRegionHints[region]; ok && fix != "" {
			return false, "not a country code, did you mean " + prettyHreflang(lang+"-"+fix) + "?"
		}
		if isDigits(region) || region == "eu" || region == "la" {
			return false, "regions must be ISO 3166-1 country codes, not " + strings.ToUpper(region)
		}
		return false, "unknown country code " + strings.ToUpper(region)
	}
	return true, ""
}

// prettyHreflang writes a code the usual way: en-GB, zh-Hant-TW.
func prettyHreflang(c string) string {
	parts := strings.Split(strings.ToLower(c), "-")
	for i := 1; i < len(parts); i++ {
		switch len(parts[i]) {
		case 2:
			parts[i] = strings.ToUpper(parts[i])
		case 4:
			parts[i] = strings.ToUpper(parts[i][:1]) + parts[i][1:]
		}
	}
	return strings.Join(parts, "-")
}

// hreflangLanguage is the language part of a code ("de" for "de-AT").
func hreflangLanguage(code string) string {
	c := strings.ToLower(strings.TrimSpace(code))
	if i := strings.IndexAny(c, "-_"); i >= 0 {
		return c[:i]
	}
	return c
}

func isAlpha(s string) bool {
	for i := 0; i < len(s); i++ {
		if s[i] < 'a' || s[i] > 'z' {
			return false
		}
	}
	return s != ""
}

func isDigits(s string) bool {
	for i := 0; i < len(s); i++ {
		if s[i] < '0' || s[i] > '9' {
			return false
		}
	}
	return s != ""
}
