package ai

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"
)

var (
	ErrGemini = fmt.Errorf("gemini api error")
)

// AIClient is an interface that abstracts AI model operations.
type AIClient interface {
	GetTextEmbedding(ctx context.Context, text string) ([]float32, error)
	GenerateImageTags(ctx context.Context, imageBytes []byte) ([]string, error)
	GenerateDocumentSummary(ctx context.Context, text string) (summary string, tags []string, err error)
}

type GeminiClient struct {
	apiKey     string
	httpClient *http.Client
}

func NewGeminiClient(apiKey string) *GeminiClient {
	return &GeminiClient{
		apiKey:     apiKey,
		httpClient: &http.Client{Timeout: 60 * time.Second},
	}
}

// GetTextEmbedding uses text-embedding-004 to return 768-dimensional vector.
func (c *GeminiClient) GetTextEmbedding(ctx context.Context, text string) ([]float32, error) {
	if text == "" {
		return nil, nil
	}

	url := fmt.Sprintf("https://generativelanguage.googleapis.com/v1beta/models/gemini-embedding-2:embedContent?key=%s", c.apiKey)

	reqBody, _ := json.Marshal(map[string]interface{}{
		"model": "models/gemini-embedding-2",
		"content": map[string]interface{}{
			"parts": []map[string]interface{}{
				{"text": text},
			},
		},
	})

	req, err := http.NewRequestWithContext(ctx, "POST", url, bytes.NewReader(reqBody))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("%w: embeddings: status %d: %s", ErrGemini, resp.StatusCode, string(body))
	}

	var result struct {
		Embedding struct {
			Values []float32 `json:"values"`
		} `json:"embedding"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return nil, fmt.Errorf("decode embeddings: %w", err)
	}

	return result.Embedding.Values, nil
}

// GenerateDocumentSummary uses gemini-1.5-flash to generate a summary and tags.
func (c *GeminiClient) GenerateDocumentSummary(ctx context.Context, text string) (summary string, tags []string, err error) {
	if len(text) > 1000000 {
		text = text[:1000000] // safety limit
	}

	url := fmt.Sprintf("https://generativelanguage.googleapis.com/v1beta/models/gemini-2.5-flash:generateContent?key=%s", c.apiKey)

	prompt := fmt.Sprintf(`You are a metadata extraction utility. Analyze the following document text. Return exactly 5 highly specific keywords/tags (comma-separated) and a dense, 2-sentence conceptual summary of the entire document. Do not include any introductory or conversational text. Format your output strictly as:
Tags: tag1, tag2, tag3
Summary: summary text

Document text:
%s`, text)

	reqBody, _ := json.Marshal(map[string]interface{}{
		"contents": []map[string]interface{}{
			{
				"parts": []map[string]interface{}{
					{"text": prompt},
				},
			},
		},
	})

	req, err := http.NewRequestWithContext(ctx, "POST", url, bytes.NewReader(reqBody))
	if err != nil {
		return "", nil, err
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return "", nil, err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		return "", nil, fmt.Errorf("%w: summary: status %d: %s", ErrGemini, resp.StatusCode, string(body))
	}

	var result struct {
		Candidates []struct {
			Content struct {
				Parts []struct {
					Text string `json:"text"`
				} `json:"parts"`
			} `json:"content"`
		} `json:"candidates"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return "", nil, fmt.Errorf("decode summary: %w", err)
	}

	if len(result.Candidates) == 0 || len(result.Candidates[0].Content.Parts) == 0 {
		return "", nil, fmt.Errorf("no content generated")
	}

	outText := result.Candidates[0].Content.Parts[0].Text

	lines := strings.Split(outText, "\n")
	for _, line := range lines {
		line = strings.TrimSpace(line)
		if strings.HasPrefix(strings.ToLower(line), "tags:") {
			tagStr := strings.TrimSpace(line[5:])
			rawTags := strings.Split(tagStr, ",")
			for _, t := range rawTags {
				tags = append(tags, strings.ToLower(strings.TrimSpace(t)))
			}
		} else if strings.HasPrefix(strings.ToLower(line), "summary:") {
			summary = strings.TrimSpace(line[8:])
		}
	}

	if summary == "" {
		summary = outText
	}

	return summary, tags, nil
}

// GenerateImageTags uses gemini-1.5-flash for vision to describe the image, and extracts keywords.
func (c *GeminiClient) GenerateImageTags(ctx context.Context, imageBytes []byte) ([]string, error) {
	url := fmt.Sprintf("https://generativelanguage.googleapis.com/v1beta/models/gemini-2.5-flash:generateContent?key=%s", c.apiKey)

	b64Img := base64.StdEncoding.EncodeToString(imageBytes)
	prompt := "Provide exactly 5 highly descriptive keywords (comma-separated) describing this image. Format strictly as: Tags: tag1, tag2, tag3"

	reqBody, _ := json.Marshal(map[string]interface{}{
		"contents": []map[string]interface{}{
			{
				"parts": []map[string]interface{}{
					{"text": prompt},
					{
						"inlineData": map[string]interface{}{
							"mimeType": "image/jpeg",
							"data":     b64Img,
						},
					},
				},
			},
		},
	})

	req, err := http.NewRequestWithContext(ctx, "POST", url, bytes.NewReader(reqBody))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("%w: vision: status %d: %s", ErrGemini, resp.StatusCode, string(body))
	}

	var result struct {
		Candidates []struct {
			Content struct {
				Parts []struct {
					Text string `json:"text"`
				} `json:"parts"`
			} `json:"content"`
		} `json:"candidates"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return nil, fmt.Errorf("decode vision: %w", err)
	}

	if len(result.Candidates) == 0 || len(result.Candidates[0].Content.Parts) == 0 {
		return nil, nil
	}

	outText := result.Candidates[0].Content.Parts[0].Text
	var tags []string
	lines := strings.Split(outText, "\n")
	for _, line := range lines {
		line = strings.TrimSpace(line)
		if strings.HasPrefix(strings.ToLower(line), "tags:") {
			tagStr := strings.TrimSpace(line[5:])
			rawTags := strings.Split(tagStr, ",")
			for _, t := range rawTags {
				tags = append(tags, strings.ToLower(strings.TrimSpace(t)))
			}
		}
	}

	if len(tags) == 0 {
		rawTags := strings.Split(outText, ",")
		for _, t := range rawTags {
			tags = append(tags, strings.ToLower(strings.TrimSpace(t)))
		}
	}

	return tags, nil
}
