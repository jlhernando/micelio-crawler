package analysis

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"time"
)

const (
	aiMaxBodyLen    = 2000
	aiMaxTokens     = 500
	aiTemperature   = 0.3
	aiRateLimitMs   = 1000
	aiDefaultPrompt = "Analyze this page for SEO issues and opportunities. Provide 3-5 actionable recommendations."
	aiSystemPrompt  = "You are an SEO expert analyzing web pages. Be concise and actionable."
)

// AIProvider specifies the AI analysis provider.
type AIProvider string

const (
	AIOpenAI    AIProvider = "openai"
	AIAnthropic AIProvider = "anthropic"
	AIOllama    AIProvider = "ollama"
)

// AIConfig configures AI-powered page analysis.
type AIConfig struct {
	Provider AIProvider
	APIKey   string
	Model    string
	Prompt   string
}

// PageContext holds the relevant page data for AI analysis.
type PageContext struct {
	URL             string
	Title           string
	MetaDescription string
	H1              string
	BodyText        string
}

// ResolveAIKey resolves the API key from explicit value or environment.
func ResolveAIKey(provider AIProvider, explicitKey string) (string, error) {
	if explicitKey != "" {
		return explicitKey, nil
	}
	switch provider {
	case AIOpenAI:
		if key := os.Getenv("OPENAI_API_KEY"); key != "" {
			return key, nil
		}
		return "", fmt.Errorf("OPENAI_API_KEY environment variable not set")
	case AIAnthropic:
		if key := os.Getenv("ANTHROPIC_API_KEY"); key != "" {
			return key, nil
		}
		return "", fmt.Errorf("ANTHROPIC_API_KEY environment variable not set")
	case AIOllama:
		return "", nil // Ollama doesn't need a key
	}
	return "", fmt.Errorf("unknown AI provider: %s", provider)
}

// AnalyzeWithAI runs AI analysis on a page's content.
func AnalyzeWithAI(ctx context.Context, cfg AIConfig, page PageContext) (string, error) {
	if cfg.Model == "" {
		switch cfg.Provider {
		case AIOpenAI:
			cfg.Model = "gpt-4o-mini"
		case AIAnthropic:
			cfg.Model = "claude-haiku-4-5-20251001"
		case AIOllama:
			cfg.Model = "llama3.2"
		}
	}
	if cfg.Prompt == "" {
		cfg.Prompt = aiDefaultPrompt
	}

	bodyText := page.BodyText
	if len(bodyText) > aiMaxBodyLen {
		bodyText = bodyText[:aiMaxBodyLen]
	}

	message := fmt.Sprintf("%s\n\nPage URL: %s\nTitle: %s\nMeta Description: %s\nH1: %s\nBody Text (first %d chars): %s",
		cfg.Prompt, page.URL, page.Title, page.MetaDescription, page.H1, len(bodyText), bodyText)

	switch cfg.Provider {
	case AIOpenAI:
		return callOpenAIChat(ctx, cfg.APIKey, cfg.Model, message)
	case AIAnthropic:
		return callAnthropicChat(ctx, cfg.APIKey, cfg.Model, message)
	case AIOllama:
		return callOllamaGenerate(ctx, cfg.Model, message)
	}
	return "", fmt.Errorf("unknown AI provider: %s", cfg.Provider)
}

func callOpenAIChat(ctx context.Context, apiKey, model, message string) (string, error) {
	body := map[string]interface{}{
		"model": model,
		"messages": []map[string]string{
			{"role": "system", "content": aiSystemPrompt},
			{"role": "user", "content": message},
		},
		"max_tokens":  aiMaxTokens,
		"temperature": aiTemperature,
	}
	payload, err := json.Marshal(body)
	if err != nil {
		return "", fmt.Errorf("marshal OpenAI request: %w", err)
	}

	req, err := http.NewRequestWithContext(ctx, "POST", "https://api.openai.com/v1/chat/completions", bytes.NewReader(payload))
	if err != nil {
		return "", err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+apiKey)

	client := &http.Client{Timeout: 30 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()

	if resp.StatusCode != 200 {
		b, _ := io.ReadAll(io.LimitReader(resp.Body, 8192))
		return "", fmt.Errorf("OpenAI API error %d: %s", resp.StatusCode, string(b))
	}

	var result struct {
		Choices []struct {
			Message struct {
				Content string `json:"content"`
			} `json:"message"`
		} `json:"choices"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return "", err
	}
	if len(result.Choices) == 0 {
		return "", fmt.Errorf("no response from OpenAI")
	}
	return result.Choices[0].Message.Content, nil
}

func callAnthropicChat(ctx context.Context, apiKey, model, message string) (string, error) {
	body := map[string]interface{}{
		"model":      model,
		"max_tokens": aiMaxTokens,
		"system":     aiSystemPrompt,
		"messages": []map[string]string{
			{"role": "user", "content": message},
		},
	}
	payload, err := json.Marshal(body)
	if err != nil {
		return "", fmt.Errorf("marshal Anthropic request: %w", err)
	}

	req, err := http.NewRequestWithContext(ctx, "POST", "https://api.anthropic.com/v1/messages", bytes.NewReader(payload))
	if err != nil {
		return "", err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("x-api-key", apiKey)
	req.Header.Set("anthropic-version", "2023-06-01")

	client := &http.Client{Timeout: 30 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()

	if resp.StatusCode != 200 {
		b, _ := io.ReadAll(io.LimitReader(resp.Body, 8192))
		return "", fmt.Errorf("Anthropic API error %d: %s", resp.StatusCode, string(b))
	}

	var result struct {
		Content []struct {
			Text string `json:"text"`
		} `json:"content"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return "", err
	}
	if len(result.Content) == 0 {
		return "", fmt.Errorf("no response from Anthropic")
	}
	return result.Content[0].Text, nil
}

func callOllamaGenerate(ctx context.Context, model, message string) (string, error) {
	prompt := aiSystemPrompt + "\n\n" + message
	body := map[string]interface{}{
		"model":  model,
		"prompt": prompt,
		"stream": false,
	}
	payload, err := json.Marshal(body)
	if err != nil {
		return "", fmt.Errorf("marshal Ollama request: %w", err)
	}

	req, err := http.NewRequestWithContext(ctx, "POST", "http://localhost:11434/api/generate", bytes.NewReader(payload))
	if err != nil {
		return "", err
	}
	req.Header.Set("Content-Type", "application/json")

	client := &http.Client{Timeout: 60 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()

	if resp.StatusCode != 200 {
		b, _ := io.ReadAll(io.LimitReader(resp.Body, 8192))
		return "", fmt.Errorf("Ollama API error %d: %s", resp.StatusCode, string(b))
	}

	var result struct {
		Response string `json:"response"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return "", err
	}
	return result.Response, nil
}
