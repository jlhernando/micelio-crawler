import { formatError } from './utils.js';

export interface AiConfig {
  provider: 'openai' | 'anthropic' | 'ollama';
  model: string;
  apiKey: string;
  prompt: string;
}

const DEFAULT_MODELS: Record<string, string> = {
  openai: 'gpt-4o-mini',
  anthropic: 'claude-haiku-4-5-20251001',
  ollama: 'llama3.2',
};

// B1 fix: Use promise chain for concurrency-safe rate limiting
const MIN_API_INTERVAL_MS = 1000;
let pending: Promise<void> = Promise.resolve();

/**
 * Run AI analysis on page content using the configured provider.
 * Requests are serialized via a promise chain to enforce rate limiting under concurrency.
 */
export function analyzeWithAi(
  config: AiConfig,
  pageContext: { url: string; title: string; description: string; h1: string; bodyText: string },
): Promise<string> {
  return new Promise<string>((resolve) => {
    pending = pending.then(async () => {
      await new Promise((r) => setTimeout(r, MIN_API_INTERVAL_MS));
      resolve(await doAnalyze(config, pageContext));
    });
  });
}

async function doAnalyze(
  config: AiConfig,
  pageContext: { url: string; title: string; description: string; h1: string; bodyText: string },
): Promise<string> {
  // Truncate body text to keep token usage reasonable
  const truncatedBody = pageContext.bodyText.substring(0, 2000);

  const userMessage = `${config.prompt}

Page URL: ${pageContext.url}
Title: ${pageContext.title}
Meta Description: ${pageContext.description}
H1: ${pageContext.h1}
Body Text (first 2000 chars): ${truncatedBody}`;

  const model = config.model || DEFAULT_MODELS[config.provider] || DEFAULT_MODELS.openai;

  try {
    switch (config.provider) {
      case 'openai':
        return await callOpenAi(config.apiKey, model, userMessage);
      case 'anthropic':
        return await callAnthropic(config.apiKey, model, userMessage);
      case 'ollama':
        return await callOllama(model, userMessage);
      default:
        return `Error: Unknown AI provider "${config.provider}"`;
    }
  } catch (err) {
    return `Error: ${formatError(err)}`;
  }
}

async function callOpenAi(apiKey: string, model: string, message: string): Promise<string> {
  const response = await fetch('https://api.openai.com/v1/chat/completions', {
    method: 'POST',
    headers: {
      'Content-Type': 'application/json',
      'Authorization': `Bearer ${apiKey}`,
    },
    body: JSON.stringify({
      model,
      messages: [
        { role: 'system', content: 'You are an SEO expert analyzing web pages. Be concise and actionable.' },
        { role: 'user', content: message },
      ],
      max_tokens: 500,
      temperature: 0.3,
    }),
    signal: AbortSignal.timeout(30000),
  });

  if (!response.ok) {
    const text = await response.text();
    throw new Error(`OpenAI API error: ${response.status} ${text.substring(0, 200)}`);
  }

  const data = await response.json();
  return data.choices?.[0]?.message?.content?.trim() || 'No response';
}

async function callAnthropic(apiKey: string, model: string, message: string): Promise<string> {
  const response = await fetch('https://api.anthropic.com/v1/messages', {
    method: 'POST',
    headers: {
      'Content-Type': 'application/json',
      'x-api-key': apiKey,
      'anthropic-version': '2023-06-01',
    },
    body: JSON.stringify({
      model,
      max_tokens: 500,
      messages: [
        { role: 'user', content: message },
      ],
      system: 'You are an SEO expert analyzing web pages. Be concise and actionable.',
    }),
    signal: AbortSignal.timeout(30000),
  });

  if (!response.ok) {
    const text = await response.text();
    throw new Error(`Anthropic API error: ${response.status} ${text.substring(0, 200)}`);
  }

  const data = await response.json();
  return data.content?.[0]?.text?.trim() || 'No response';
}

async function callOllama(model: string, message: string): Promise<string> {
  const response = await fetch('http://localhost:11434/api/generate', {
    method: 'POST',
    headers: { 'Content-Type': 'application/json' },
    body: JSON.stringify({
      model,
      prompt: `You are an SEO expert analyzing web pages. Be concise and actionable.\n\n${message}`,
      stream: false,
    }),
    signal: AbortSignal.timeout(60000),
  });

  if (!response.ok) {
    const text = await response.text();
    throw new Error(`Ollama API error: ${response.status} ${text.substring(0, 200)}`);
  }

  const data = await response.json();
  return data.response?.trim() || 'No response';
}

/**
 * Resolve AI API key from explicit flag, env var, or fail.
 */
export function resolveAiKey(provider: string, explicitKey: string): string {
  if (explicitKey) return explicitKey;

  const envKeys: Record<string, string> = {
    openai: 'OPENAI_API_KEY',
    anthropic: 'ANTHROPIC_API_KEY',
  };

  const envName = envKeys[provider];
  if (envName) {
    const fromEnv = process.env[envName];
    if (fromEnv) return fromEnv;
    throw new Error(`No API key provided. Set ${envName} or use --ai-key`);
  }

  // Ollama doesn't need a key
  return '';
}
