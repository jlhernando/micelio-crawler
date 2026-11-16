// Official provider examples/tokens, reviewed 2026-11-16. Versions can change.
// These presets simulate the User-Agent header, not a verified provider identity.
const openai = 'https://developers.openai.com/api/docs/bots';
const anthropic = 'https://support.claude.com/en/articles/8896518-does-anthropic-crawl-data-from-the-web-and-how-can-site-owners-block-the-crawler';
const perplexity = 'https://docs.perplexity.ai/docs/resources/perplexity-crawlers';

export const aiUserAgentPresets = [
  { group: 'AI agents', label: 'GPTBot — OpenAI (training)', value: 'Mozilla/5.0 AppleWebKit/537.36 (KHTML, like Gecko); compatible; GPTBot/1.4; +https://openai.com/gptbot', source: openai },
  { group: 'AI agents', label: 'OAI-SearchBot — OpenAI (search)', value: 'Mozilla/5.0 (Macintosh; Intel Mac OS X 10_15_7) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/131.0.0.0 Safari/537.36; compatible; OAI-SearchBot/1.4; +https://openai.com/searchbot', source: openai },
  { group: 'AI agents', label: 'ChatGPT-User — OpenAI (user request)', value: 'Mozilla/5.0 AppleWebKit/537.36 (KHTML, like Gecko); compatible; ChatGPT-User/1.0; +https://openai.com/bot', source: openai },
  // Anthropic documents product tokens rather than a canonical full header.
  { group: 'AI agents', label: 'ClaudeBot — Anthropic (training)', value: 'ClaudeBot', source: anthropic },
  { group: 'AI agents', label: 'Claude-SearchBot — Anthropic (search)', value: 'Claude-SearchBot', source: anthropic },
  { group: 'AI agents', label: 'Claude-User — Anthropic (user request)', value: 'Claude-User', source: anthropic },
  { group: 'AI agents', label: 'PerplexityBot — Perplexity (search)', value: 'Mozilla/5.0 (compatible; PerplexityBot/1.0; +https://perplexity.ai/perplexitybot)', source: perplexity },
  { group: 'AI agents', label: 'Perplexity-User — Perplexity (user request)', value: 'Mozilla/5.0 (compatible; Perplexity-User/1.0; +https://perplexity.ai/perplexity-user)', source: perplexity },
];
