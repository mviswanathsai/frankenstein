package openai

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"strings"

	"frankenstein/internal/modelinvocation"
	"frankenstein/internal/session"
)

// Adapter implements modelinvocation.ProviderAdapter for OpenAI-compatible
// APIs (DeepSeek specifically, but the protocol is generic).
type Adapter struct {
	apiKey  string
	baseURL string
}

// NewAdapter creates an adapter. apiKey and baseURL are required.
// If baseURL is empty, defaults to "https://api.deepseek.com".
func NewAdapter(apiKey, baseURL string) *Adapter {
	if baseURL == "" {
		baseURL = "https://api.deepseek.com"
	}
	// Allow API key from environment if empty string is passed.
	if apiKey == "" {
		apiKey = os.Getenv("DEEPSEEK_API_KEY")
	}
	return &Adapter{
		apiKey:  apiKey,
		baseURL: baseURL,
	}
}

// chatRequest is the JSON body sent to the chat/completions endpoint.
type chatRequest struct {
	Model    string         `json:"model"`
	Messages []chatMessage  `json:"messages"`
	Stream   bool           `json:"stream"`
	StreamOpts streamOpts   `json:"stream_options"`
	Tools     []chatTool    `json:"tools,omitempty"`
	MaxTokens int           `json:"max_tokens,omitempty"`
}

type streamOpts struct {
	IncludeUsage bool `json:"include_usage"`
}

type chatMessage struct {
	Role             string         `json:"role"`
	Content          string         `json:"content"`
	ReasoningContent string         `json:"reasoning_content,omitempty"`
	ToolCalls        []chatToolCall `json:"tool_calls,omitempty"`
	ToolCallID       string         `json:"tool_call_id,omitempty"`
}

type chatToolCall struct {
	ID       string `json:"id"`
	Type     string `json:"type"`
	Function struct {
		Name      string `json:"name"`
		Arguments string `json:"arguments"`
	} `json:"function"`
}

type chatTool struct {
	Type     string `json:"type"`
	Function struct {
		Name        string          `json:"name"`
		Description string          `json:"description"`
		Parameters  json.RawMessage `json:"parameters"`
	} `json:"function"`
}

// sseChunk is a single SSE data payload from the chat/completions stream.
type sseChunk struct {
	ID      string `json:"id"`
	Model   string `json:"model"`
	Choices []struct {
		Index int `json:"index"`
		Delta struct {
			Role             string            `json:"role"`
			Content          *string           `json:"content"`
			ReasoningContent *string           `json:"reasoning_content"`
			ToolCalls        []sseToolCallDelta `json:"tool_calls"`
		} `json:"delta"`
		FinishReason *string `json:"finish_reason"`
	} `json:"choices"`
	Usage *sseUsage `json:"usage"`
}

type sseToolCallDelta struct {
	Index    int     `json:"index"`
	ID       *string `json:"id"`
	Function *struct {
		Name      *string `json:"name"`
		Arguments *string `json:"arguments"`
	} `json:"function"`
}

type sseUsage struct {
	PromptTokens     int `json:"prompt_tokens"`
	CompletionTokens int `json:"completion_tokens"`
	TotalTokens      int `json:"total_tokens"`
	CompletionDetails *struct {
		ReasoningTokens int `json:"reasoning_tokens"`
	} `json:"completion_tokens_details"`
	PromptDetails *struct {
		CachedTokens int `json:"cached_tokens"`
	} `json:"prompt_tokens_details"`
	CacheHitTokens  int `json:"prompt_cache_hit_tokens"`
	CacheMissTokens int `json:"prompt_cache_miss_tokens"`
}

// Invoke implements modelinvocation.ProviderAdapter.
func (a *Adapter) Invoke(ctx context.Context, req modelinvocation.ProviderRequest) (<-chan modelinvocation.Fragment, error) {
	// Build the request body.
	body, err := a.buildRequestBody(req)
	if err != nil {
		return nil, fmt.Errorf("openai: build request body: %w", err)
	}

	// Create the HTTP request.
	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, a.baseURL+"/chat/completions", strings.NewReader(body))
	if err != nil {
		return nil, fmt.Errorf("openai: create request: %w", err)
	}
	httpReq.Header.Set("Content-Type", "application/json")
	httpReq.Header.Set("Authorization", "Bearer "+a.apiKey)

	// Execute the request.
	client := &http.Client{}
	resp, err := client.Do(httpReq)
	if err != nil {
		return nil, fmt.Errorf("openai: execute request: %w", err)
	}

	// Non-200: emit an error fragment and close.
	if resp.StatusCode != http.StatusOK {
		return a.errorResponse(resp)
	}

	// Start the SSE parsing goroutine.
	ch := make(chan modelinvocation.Fragment, 16)
	go a.parseSSE(ctx, resp.Body, ch)

	return ch, nil
}

// buildRequestBody encodes the ProviderRequest into the JSON body.
func (a *Adapter) buildRequestBody(req modelinvocation.ProviderRequest) (string, error) {
	msgs := make([]chatMessage, 0, len(req.Messages)+1)

	// Prepend system message if present.
	if req.System != "" {
		msgs = append(msgs, chatMessage{
			Role:    "system",
			Content: req.System,
		})
	}

	for _, m := range req.Messages {
		cm, err := encodeMessage(m)
		if err != nil {
			return "", err
		}
		msgs = append(msgs, cm)
	}

	cr := chatRequest{
		Model:    req.Model,
		Messages: msgs,
		Stream:   true,
		StreamOpts: streamOpts{
			IncludeUsage: true,
		},
	}

	if req.Catalog != nil && len(req.Catalog.Tools) > 0 {
		cr.Tools = make([]chatTool, 0, len(req.Catalog.Tools))
		for _, d := range req.Catalog.Tools {
			ct := chatTool{
				Type: "function",
			}
			ct.Function.Name = d.Name
			ct.Function.Description = d.Description
			ct.Function.Parameters = d.InputSchema
			cr.Tools = append(cr.Tools, ct)
		}
	}

	if req.MaxTokens > 0 {
		cr.MaxTokens = req.MaxTokens
	}

	b, err := json.Marshal(cr)
	if err != nil {
		return "", err
	}
	return string(b), nil
}

// encodeMessage converts a modelinvocation.ModelMessage to a chatMessage.
func encodeMessage(m modelinvocation.ModelMessage) (chatMessage, error) {
	switch m.Role {
	case modelinvocation.RoleUser:
		return chatMessage{
			Role:    "user",
			Content: m.Content,
		}, nil

	case modelinvocation.RoleAssistant:
		cm := chatMessage{
			Role:    "assistant",
			Content: m.Content,
		}
		if m.Reasoning != "" {
			cm.ReasoningContent = m.Reasoning
		}
		if len(m.ToolCalls) > 0 {
			cm.ToolCalls = make([]chatToolCall, 0, len(m.ToolCalls))
			for _, tc := range m.ToolCalls {
				argsJSON, err := json.Marshal(tc.Arguments)
				if err != nil {
					return chatMessage{}, fmt.Errorf("encode tool call arguments: %w", err)
				}
				ctc := chatToolCall{
					ID:   tc.ID,
					Type: "function",
				}
				ctc.Function.Name = tc.Name
				ctc.Function.Arguments = string(argsJSON)
				cm.ToolCalls = append(cm.ToolCalls, ctc)
			}
		}
		return cm, nil

	case modelinvocation.RoleTool:
		return chatMessage{
			Role:       "tool",
			Content:    m.Content,
			ToolCallID: m.CallID,
		}, nil

	default:
		return chatMessage{}, fmt.Errorf("unknown message role: %s", m.Role)
	}
}

// errorResponse emits a single error fragment and closes the channel.
func (a *Adapter) errorResponse(resp *http.Response) (<-chan modelinvocation.Fragment, error) {
	defer resp.Body.Close()

	body, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
	msg := fmt.Sprintf("openai: HTTP %d: %s", resp.StatusCode, strings.TrimSpace(string(body)))

	ch := make(chan modelinvocation.Fragment, 1)
	go func() {
		defer close(ch)
		ch <- modelinvocation.Fragment{
			Error: errors.New(msg),
		}
	}()
	return ch, nil
}

// parseSSE reads SSE lines from the response body and emits Fragments.
func (a *Adapter) parseSSE(ctx context.Context, body io.ReadCloser, ch chan<- modelinvocation.Fragment) {
	defer body.Close()
	defer close(ch)

	scanner := bufio.NewScanner(body)
	// Increase buffer for large tool-call chunks.
	scanner.Buffer(make([]byte, 0, 64*1024), 1024*1024)

	for scanner.Scan() {
		line := scanner.Text()

		// Skip empty lines.
		if line == "" {
			continue
		}
		// Skip SSE comments (keep-alive pings).
		if strings.HasPrefix(line, ":") {
			continue
		}
		// Parse data lines.
		if !strings.HasPrefix(line, "data: ") {
			continue
		}

		data := strings.TrimPrefix(line, "data: ")
		if data == "[DONE]" {
			return
		}

		var chunk sseChunk
		if err := json.Unmarshal([]byte(data), &chunk); err != nil {
			ch <- modelinvocation.Fragment{
				Error: fmt.Errorf("openai: parse SSE chunk: %w", err),
			}
			return
		}

		frag := a.chunkToFragment(&chunk)
		ch <- frag

		// Stop if we got an error or terminal chunk.
		if frag.Error != nil || frag.FinishReason != "" {
			return
		}
	}

	if err := scanner.Err(); err != nil && !errors.Is(err, context.Canceled) {
		ch <- modelinvocation.Fragment{
			Error: fmt.Errorf("openai: read SSE stream: %w", err),
		}
	}
}

// chunkToFragment converts an SSE chunk into a Fragment.
func (a *Adapter) chunkToFragment(chunk *sseChunk) modelinvocation.Fragment {
	frag := modelinvocation.Fragment{}

	for _, choice := range chunk.Choices {
		// Delta content (nil pointer means absent).
		if choice.Delta.Content != nil {
			frag.DeltaContent = *choice.Delta.Content
		}

		// Delta reasoning (nil pointer means absent).
		if choice.Delta.ReasoningContent != nil {
			frag.DeltaReasoning = *choice.Delta.ReasoningContent
		}

		// Finish reason (nil pointer means absent).
		if choice.FinishReason != nil {
			frag.FinishReason = *choice.FinishReason
		}

		// Tool call deltas.
		if len(choice.Delta.ToolCalls) > 0 {
			frag.ToolCallDeltas = make([]modelinvocation.ToolCallDelta, 0, len(choice.Delta.ToolCalls))
			for _, delta := range choice.Delta.ToolCalls {
				tcd := modelinvocation.ToolCallDelta{
					Index: delta.Index,
				}
				if delta.ID != nil {
					tcd.ID = *delta.ID
				}
				if delta.Function != nil {
					if delta.Function.Name != nil {
						tcd.Name = *delta.Function.Name
					}
					if delta.Function.Arguments != nil {
						tcd.Arguments = *delta.Function.Arguments
					}
				}
				frag.ToolCallDeltas = append(frag.ToolCallDeltas, tcd)
			}
		}
	}

	// Usage (nil until terminal chunk).
	if chunk.Usage != nil {
		usage := modelinvocation.CallUsage{
			InputTokens: session.TokenCount{
				Value:  int64(chunk.Usage.PromptTokens),
				Source: session.TokenSourceProvider,
			},
			OutputTokens: session.TokenCount{
				Value:  int64(chunk.Usage.CompletionTokens),
				Source: session.TokenSourceProvider,
			},
		}

		if chunk.Usage.PromptDetails != nil && chunk.Usage.PromptDetails.CachedTokens > 0 {
			usage.CacheReadTokens = &session.TokenCount{
				Value:  int64(chunk.Usage.PromptDetails.CachedTokens),
				Source: session.TokenSourceProvider,
			}
		}

		if chunk.Usage.CacheMissTokens > 0 {
			usage.CacheWriteTokens = &session.TokenCount{
				Value:  int64(chunk.Usage.CacheMissTokens),
				Source: session.TokenSourceProvider,
			}
		}

		if chunk.Usage.CompletionDetails != nil && chunk.Usage.CompletionDetails.ReasoningTokens > 0 {
			usage.ReasoningTokens = &session.TokenCount{
				Value:  int64(chunk.Usage.CompletionDetails.ReasoningTokens),
				Source: session.TokenSourceProvider,
			}
		}

		frag.Usage = &usage
	}

	return frag
}
