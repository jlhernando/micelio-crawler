package logs


type botSignature struct {
	Pattern  string
	Name     string
	Category string
	Mobile   bool
}

// Ordered most-specific first. Patterns matched against lowercased UA.
var botSignatures = []botSignature{
	// Google specialized
	{"googlebot-image", "Googlebot-Image", "search_media", false},
	{"googlebot-video", "Googlebot-Video", "search_media", false},
	{"googlebot-news", "Googlebot-News", "search_media", false},
	{"storebot-google", "StoreBot-Google", "search_engine", false},
	{"google-inspectiontool", "Google-InspectionTool", "search_engine", false},
	{"googleother", "GoogleOther", "search_engine", false},
	{"google-extended", "Google-Extended", "ai_training", false},
	{"googlebot", "Googlebot", "search_engine", false},
	// Google mobile (check after base Googlebot)
	{"googlebot/2.1; +http://www.google.com/bot.html", "Googlebot", "search_engine", false},

	// Bing
	{"bingbot", "Bingbot", "search_engine", false},

	// AI crawlers (before generic bots)
	{"gptbot", "GPTBot", "ai_training", false},
	{"oai-searchbot", "OAI-SearchBot", "ai_search", false},
	{"chatgpt-user", "ChatGPT-User", "ai_search", false},
	{"claudebot", "ClaudeBot", "ai_training", false},
	{"claude-searchbot", "Claude-SearchBot", "ai_search", false},
	{"claude-user", "Claude-User", "ai_search", false},
	{"perplexitybot", "PerplexityBot", "ai_search", false},
	{"bytespider", "Bytespider", "ai_training", false},
	{"ccbot", "CCBot", "ai_training", false},
	{"meta-externalagent", "Meta-ExternalAgent", "ai_training", false},
	{"amazonbot", "Amazonbot", "ai_training", false},
	{"applebot-extended", "Applebot-Extended", "ai_training", false},
	{"duckassistbot", "DuckAssistBot", "ai_search", false},
	{"gemini-deep-research", "Gemini-Deep-Research", "ai_search", false},
	{"diffbot", "Diffbot", "ai_training", false},
	{"mistralai-user", "MistralAI-User", "ai_search", false},
	{"cohere-ai", "Cohere-AI", "ai_training", false},

	// Other search engines
	{"yandexbot", "YandexBot", "search_engine", false},
	{"baiduspider", "Baiduspider", "search_engine", false},
	{"duckduckbot", "DuckDuckBot", "search_engine", false},
	{"applebot", "Applebot", "search_engine", false},
	{"sogou", "Sogou", "search_engine", false},
	{"seznambot", "SeznamBot", "search_engine", false},
	{"naverbot", "NaverBot", "search_engine", false},
	{"yeti/", "Yeti", "search_engine", false},

	// SEO/monitoring tools
	{"ahrefs", "AhrefsBot", "seo_tool", false},
	{"semrushbot", "SemrushBot", "seo_tool", false},
	{"mj12bot", "MJ12Bot", "seo_tool", false},
	{"dotbot", "DotBot", "seo_tool", false},
	{"screaming frog", "Screaming Frog", "seo_tool", false},

	// Social
	{"facebookexternalhit", "Facebook", "social_media", false},
	{"twitterbot", "TwitterBot", "social_media", false},
	{"linkedinbot", "LinkedInBot", "social_media", false},
	{"pinterestbot", "PinterestBot", "social_media", false},
	{"slackbot", "SlackBot", "social_media", false},
	{"telegrambot", "TelegramBot", "social_media", false},
	{"discordbot", "DiscordBot", "social_media", false},
	{"whatsapp", "WhatsApp", "social_media", false},

	// Feed
	{"feedfetcher", "FeedFetcher", "feed_crawler", false},
	{"feedly", "Feedly", "feed_crawler", false},

	// Monitoring
	{"uptimerobot", "UptimeRobot", "monitoring", false},
	{"pingdom", "Pingdom", "monitoring", false},
	{"statuscake", "StatusCake", "monitoring", false},
}

// IdentifyBot returns bot info if the UA matches a known bot, nil otherwise.
// Uses zero-allocation case-insensitive matching to avoid 66M+ string
// allocations that strings.ToLower() would create.
func IdentifyBot(ua string) *BotInfo {
	mobile := containsCI(ua, "mobile")
	for i := range botSignatures {
		if containsCI(ua, botSignatures[i].Pattern) {
			return &BotInfo{
				Name:     botSignatures[i].Name,
				Category: botSignatures[i].Category,
				Mobile:   botSignatures[i].Mobile || (mobile && botSignatures[i].Category == "search_engine"),
			}
		}
	}
	// Generic bot detection
	if containsCI(ua, "bot/") || containsCI(ua, "spider") ||
		containsCI(ua, "crawler") || containsCI(ua, "scraper") {
		return &BotInfo{Name: "Other Bot", Category: "other_bot"}
	}
	return nil
}

// containsCI performs case-insensitive substring search without allocating.
// Pattern must be lowercase ASCII (all bot patterns are).
func containsCI(s, pattern string) bool {
	pLen := len(pattern)
	if pLen == 0 {
		return true
	}
	if pLen > len(s) {
		return false
	}
	// Fast first-char check to skip most positions.
	p0 := pattern[0]
	for i := 0; i <= len(s)-pLen; i++ {
		c := s[i]
		if c >= 'A' && c <= 'Z' {
			c += 32
		}
		if c != p0 {
			continue
		}
		match := true
		for j := 1; j < pLen; j++ {
			c = s[i+j]
			if c >= 'A' && c <= 'Z' {
				c += 32
			}
			if c != pattern[j] {
				match = false
				break
			}
		}
		if match {
			return true
		}
	}
	return false
}
