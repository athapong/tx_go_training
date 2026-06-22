// Package main สาธิต LLM API Integration สำหรับ Go Applications
// รันด้วย: go run main.go
//
// โครงสร้างของ demo นี้:
//   1. LLMClient interface — generic, รองรับทั้ง OpenAI และ Anthropic
//   2. OpenAIClient — ใช้ github.com/sashabaranov/go-openai
//   3. AnthropicClient — direct HTTP calls (ไม่มี official Go SDK)
//   4. Middleware chain: RateLimiter → RetryMiddleware → CostTracker
//   5. StreamingHandler — parse SSE responses
//   6. Structured output — ส่ง JSON schema ใน system prompt, parse เป็น Go struct
package main

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"math"
	"math/rand"
	"net/http"
	"strings"
	"sync"
	"time"
)

// ============================================================
// SECTION 1: Core Interface และ Types
// ============================================================

// Message — โครงสร้าง message สำหรับส่งหา LLM
// เหมือนกันทั้ง OpenAI และ Anthropic (เราทำ mapping ให้)
type Message struct {
	Role    string // "system", "user", "assistant"
	Content string
}

// CompletionRequest — request ที่ส่งหา LLM
type CompletionRequest struct {
	Messages    []Message
	Model       string
	MaxTokens   int
	Temperature float64
	Stream      bool
}

// CompletionResponse — response จาก LLM
type CompletionResponse struct {
	Content      string
	InputTokens  int
	OutputTokens int
	Model        string
	StopReason   string
}

// StreamChunk — ข้อมูลแต่ละ chunk ใน streaming response
type StreamChunk struct {
	Delta    string
	Done     bool
	Err      error
}

// LLMClient — interface หลักที่ทั้ง OpenAI และ Anthropic implement
// ทำไมต้องเป็น interface?
//   - เปลี่ยน provider ได้โดยไม่กระทบ business logic
//   - test ด้วย mock ได้ง่าย
//   - ทำ middleware pattern ได้ (wrapper ที่ implement interface เดียวกัน)
type LLMClient interface {
	Complete(ctx context.Context, req CompletionRequest) (*CompletionResponse, error)
	Stream(ctx context.Context, req CompletionRequest, handler StreamHandler) error
	Name() string
}

// StreamHandler — callback function สำหรับรับ streaming chunks
type StreamHandler func(chunk StreamChunk)

// ============================================================
// SECTION 2: OpenAI Implementation
// ============================================================
// NOTE: ใน production จริง ควรใช้ github.com/sashabaranov/go-openai
// แต่ demo นี้ implement HTTP โดยตรงเพื่อให้เข้าใจ internals
// การใช้ go-openai SDK:
//   client := openai.NewClient(apiKey)
//   resp, err := client.CreateChatCompletion(ctx, openai.ChatCompletionRequest{...})

// OpenAIClient — implements LLMClient สำหรับ OpenAI API
type OpenAIClient struct {
	apiKey     string
	baseURL    string
	httpClient *http.Client
}

// NewOpenAIClient — สร้าง OpenAI client
// ใส่ apiKey = "" เพื่อใช้ใน demo (จะ return mock response)
func NewOpenAIClient(apiKey string) *OpenAIClient {
	return &OpenAIClient{
		apiKey:  apiKey,
		baseURL: "https://api.openai.com/v1",
		httpClient: &http.Client{
			Timeout: 60 * time.Second,
		},
	}
}

func (c *OpenAIClient) Name() string { return "openai" }

// Complete — ส่ง request ไป OpenAI และรับ response เต็ม
// OpenAI API format:
//
//	POST /v1/chat/completions
//	{
//	  "model": "gpt-4o",
//	  "messages": [{"role": "user", "content": "Hello"}],
//	  "max_tokens": 1000
//	}
func (c *OpenAIClient) Complete(ctx context.Context, req CompletionRequest) (*CompletionResponse, error) {
	// ถ้าไม่มี API key → ใช้ mock response
	if c.apiKey == "" {
		return mockOpenAIResponse(req), nil
	}

	// สร้าง OpenAI request body
	messages := make([]map[string]string, len(req.Messages))
	for i, m := range req.Messages {
		messages[i] = map[string]string{
			"role":    m.Role,
			"content": m.Content,
		}
	}

	body := map[string]interface{}{
		"model":       req.Model,
		"messages":    messages,
		"max_tokens":  req.MaxTokens,
		"temperature": req.Temperature,
	}

	jsonBody, err := json.Marshal(body)
	if err != nil {
		return nil, fmt.Errorf("marshal request: %w", err)
	}

	httpReq, err := http.NewRequestWithContext(ctx, "POST",
		c.baseURL+"/chat/completions", bytes.NewReader(jsonBody))
	if err != nil {
		return nil, fmt.Errorf("create request: %w", err)
	}

	httpReq.Header.Set("Content-Type", "application/json")
	httpReq.Header.Set("Authorization", "Bearer "+c.apiKey)

	resp, err := c.httpClient.Do(httpReq)
	if err != nil {
		return nil, fmt.Errorf("http request: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("openai error %d: %s", resp.StatusCode, body)
	}

	// Parse OpenAI response format
	var openAIResp struct {
		Choices []struct {
			Message struct {
				Content string `json:"content"`
			} `json:"message"`
			FinishReason string `json:"finish_reason"`
		} `json:"choices"`
		Usage struct {
			PromptTokens     int `json:"prompt_tokens"`
			CompletionTokens int `json:"completion_tokens"`
		} `json:"usage"`
		Model string `json:"model"`
	}

	if err := json.NewDecoder(resp.Body).Decode(&openAIResp); err != nil {
		return nil, fmt.Errorf("decode response: %w", err)
	}

	if len(openAIResp.Choices) == 0 {
		return nil, errors.New("no choices in response")
	}

	return &CompletionResponse{
		Content:      openAIResp.Choices[0].Message.Content,
		InputTokens:  openAIResp.Usage.PromptTokens,
		OutputTokens: openAIResp.Usage.CompletionTokens,
		Model:        openAIResp.Model,
		StopReason:   openAIResp.Choices[0].FinishReason,
	}, nil
}

// Stream — OpenAI streaming โดยใช้ SSE (Server-Sent Events)
// OpenAI ส่ง response เป็น chunks แบบนี้:
//
//	data: {"choices":[{"delta":{"content":"Hello"}}]}
//	data: {"choices":[{"delta":{"content":" world"}}]}
//	data: [DONE]
func (c *OpenAIClient) Stream(ctx context.Context, req CompletionRequest, handler StreamHandler) error {
	if c.apiKey == "" {
		return mockOpenAIStream(req, handler)
	}

	messages := make([]map[string]string, len(req.Messages))
	for i, m := range req.Messages {
		messages[i] = map[string]string{"role": m.Role, "content": m.Content}
	}

	body := map[string]interface{}{
		"model":       req.Model,
		"messages":    messages,
		"max_tokens":  req.MaxTokens,
		"temperature": req.Temperature,
		"stream":      true,
	}

	jsonBody, _ := json.Marshal(body)
	httpReq, err := http.NewRequestWithContext(ctx, "POST",
		c.baseURL+"/chat/completions", bytes.NewReader(jsonBody))
	if err != nil {
		return err
	}

	httpReq.Header.Set("Content-Type", "application/json")
	httpReq.Header.Set("Authorization", "Bearer "+c.apiKey)
	httpReq.Header.Set("Accept", "text/event-stream")

	resp, err := c.httpClient.Do(httpReq)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	// Parse SSE stream
	return parseOpenAISSE(resp.Body, handler)
}

// parseOpenAISSE — แกะ Server-Sent Events จาก OpenAI
func parseOpenAISSE(body io.Reader, handler StreamHandler) error {
	scanner := bufio.NewScanner(body)
	for scanner.Scan() {
		line := scanner.Text()

		// SSE format: บรรทัดที่ไม่ใช่ข้อมูลจะเป็น empty หรือ comment
		if line == "" || strings.HasPrefix(line, ":") {
			continue
		}

		// ตัด prefix "data: "
		if !strings.HasPrefix(line, "data: ") {
			continue
		}
		data := strings.TrimPrefix(line, "data: ")

		// [DONE] หมายความว่า stream จบแล้ว
		if data == "[DONE]" {
			handler(StreamChunk{Done: true})
			return nil
		}

		// Parse JSON chunk
		var chunk struct {
			Choices []struct {
				Delta struct {
					Content string `json:"content"`
				} `json:"delta"`
				FinishReason *string `json:"finish_reason"`
			} `json:"choices"`
		}

		if err := json.Unmarshal([]byte(data), &chunk); err != nil {
			continue // skip malformed chunks
		}

		if len(chunk.Choices) > 0 {
			content := chunk.Choices[0].Delta.Content
			if content != "" {
				handler(StreamChunk{Delta: content})
			}
		}
	}
	return scanner.Err()
}

// ============================================================
// SECTION 3: Anthropic Implementation
// ============================================================
// ทำไม Anthropic ใช้ direct HTTP?
//   - ไม่มี official Go SDK จาก Anthropic (ณ ปี 2024)
//   - Community SDK มีอยู่ แต่ไม่ได้ maintained โดย Anthropic
//   - API ของ Anthropic เข้าใจง่าย สามารถ implement เองได้ไม่ยาก
//
// ความแตกต่างสำคัญระหว่าง OpenAI และ Anthropic API:
//   OpenAI: messages array รวม system message ไว้ด้วย (role: "system")
//   Anthropic: แยก system prompt ออกมาเป็น field ต่างหาก
//   Anthropic: ใช้ header "x-api-key" แทน "Authorization: Bearer"
//   Anthropic: ต้องส่ง "anthropic-version" header ด้วย

// AnthropicClient — implements LLMClient สำหรับ Anthropic API
type AnthropicClient struct {
	apiKey     string
	baseURL    string
	httpClient *http.Client
}

func NewAnthropicClient(apiKey string) *AnthropicClient {
	return &AnthropicClient{
		apiKey:  apiKey,
		baseURL: "https://api.anthropic.com/v1",
		httpClient: &http.Client{
			Timeout: 60 * time.Second,
		},
	}
}

func (c *AnthropicClient) Name() string { return "anthropic" }

// Complete — ส่ง request ไป Anthropic Messages API
// Anthropic API format:
//
//	POST /v1/messages
//	{
//	  "model": "claude-opus-4-5",
//	  "max_tokens": 1000,
//	  "system": "You are helpful",      ← system prompt แยกออกมา
//	  "messages": [{"role": "user", "content": "Hello"}]
//	}
func (c *AnthropicClient) Complete(ctx context.Context, req CompletionRequest) (*CompletionResponse, error) {
	if c.apiKey == "" {
		return mockAnthropicResponse(req), nil
	}

	// แยก system message ออกจาก messages อื่น
	// Anthropic ต้องการ system เป็น field แยก ไม่ใช่ใน messages array
	var systemPrompt string
	var userMessages []map[string]string

	for _, m := range req.Messages {
		if m.Role == "system" {
			systemPrompt = m.Content
		} else {
			userMessages = append(userMessages, map[string]string{
				"role":    m.Role,
				"content": m.Content,
			})
		}
	}

	body := map[string]interface{}{
		"model":      req.Model,
		"max_tokens": req.MaxTokens,
		"messages":   userMessages,
	}
	if systemPrompt != "" {
		body["system"] = systemPrompt
	}

	jsonBody, _ := json.Marshal(body)
	httpReq, err := http.NewRequestWithContext(ctx, "POST",
		c.baseURL+"/messages", bytes.NewReader(jsonBody))
	if err != nil {
		return nil, err
	}

	// Anthropic ใช้ header คนละชื่อกับ OpenAI
	httpReq.Header.Set("Content-Type", "application/json")
	httpReq.Header.Set("x-api-key", c.apiKey)
	httpReq.Header.Set("anthropic-version", "2023-06-01")

	resp, err := c.httpClient.Do(httpReq)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("anthropic error %d: %s", resp.StatusCode, body)
	}

	// Parse Anthropic response format
	// ต่างจาก OpenAI: content เป็น array ของ blocks
	var anthropicResp struct {
		Content []struct {
			Type string `json:"type"`
			Text string `json:"text"`
		} `json:"content"`
		Usage struct {
			InputTokens  int `json:"input_tokens"`
			OutputTokens int `json:"output_tokens"`
		} `json:"usage"`
		Model      string `json:"model"`
		StopReason string `json:"stop_reason"`
	}

	if err := json.NewDecoder(resp.Body).Decode(&anthropicResp); err != nil {
		return nil, err
	}

	var content string
	for _, block := range anthropicResp.Content {
		if block.Type == "text" {
			content += block.Text
		}
	}

	return &CompletionResponse{
		Content:      content,
		InputTokens:  anthropicResp.Usage.InputTokens,
		OutputTokens: anthropicResp.Usage.OutputTokens,
		Model:        anthropicResp.Model,
		StopReason:   anthropicResp.StopReason,
	}, nil
}

// Stream — Anthropic streaming ใช้ SSE เช่นกัน แต่ format event ต่างกัน
// Anthropic SSE format:
//
//	event: content_block_delta
//	data: {"type":"content_block_delta","delta":{"type":"text_delta","text":"Hello"}}
//
//	event: message_stop
//	data: {"type":"message_stop"}
func (c *AnthropicClient) Stream(ctx context.Context, req CompletionRequest, handler StreamHandler) error {
	if c.apiKey == "" {
		return mockAnthropicStream(req, handler)
	}

	var systemPrompt string
	var userMessages []map[string]string
	for _, m := range req.Messages {
		if m.Role == "system" {
			systemPrompt = m.Content
		} else {
			userMessages = append(userMessages, map[string]string{
				"role": m.Role, "content": m.Content,
			})
		}
	}

	body := map[string]interface{}{
		"model":      req.Model,
		"max_tokens": req.MaxTokens,
		"messages":   userMessages,
		"stream":     true,
	}
	if systemPrompt != "" {
		body["system"] = systemPrompt
	}

	jsonBody, _ := json.Marshal(body)
	httpReq, err := http.NewRequestWithContext(ctx, "POST",
		c.baseURL+"/messages", bytes.NewReader(jsonBody))
	if err != nil {
		return err
	}

	httpReq.Header.Set("Content-Type", "application/json")
	httpReq.Header.Set("x-api-key", c.apiKey)
	httpReq.Header.Set("anthropic-version", "2023-06-01")
	httpReq.Header.Set("Accept", "text/event-stream")

	resp, err := c.httpClient.Do(httpReq)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	return parseAnthropicSSE(resp.Body, handler)
}

// parseAnthropicSSE — แกะ SSE จาก Anthropic
// Anthropic มี event types หลายชนิด เราสนใจแค่ content_block_delta
func parseAnthropicSSE(body io.Reader, handler StreamHandler) error {
	scanner := bufio.NewScanner(body)
	var eventType string

	for scanner.Scan() {
		line := scanner.Text()

		if line == "" {
			eventType = "" // reset event type หลัง empty line
			continue
		}

		if strings.HasPrefix(line, "event: ") {
			eventType = strings.TrimPrefix(line, "event: ")
			continue
		}

		if !strings.HasPrefix(line, "data: ") {
			continue
		}
		data := strings.TrimPrefix(line, "data: ")

		switch eventType {
		case "content_block_delta":
			var delta struct {
				Delta struct {
					Type string `json:"type"`
					Text string `json:"text"`
				} `json:"delta"`
			}
			if err := json.Unmarshal([]byte(data), &delta); err == nil {
				if delta.Delta.Type == "text_delta" && delta.Delta.Text != "" {
					handler(StreamChunk{Delta: delta.Delta.Text})
				}
			}
		case "message_stop":
			handler(StreamChunk{Done: true})
			return nil
		}
	}
	return scanner.Err()
}

// ============================================================
// SECTION 4: Middleware Chain
// ============================================================
// Middleware pattern: wrapper ที่ implement LLMClient interface
// เรียงต่อกันได้: RateLimiter(RetryMiddleware(CostTracker(actualClient)))
//
// ลำดับการ wrap ที่แนะนำ (จากนอกสุดเข้าไป):
//   RateLimiter → RetryMiddleware → CostTracker → actual client
// เหตุผล: rate limit ก่อน retry เพื่อไม่ให้ retry เร็วเกินไป

// RateLimiterMiddleware — จำกัด request rate ด้วย token bucket algorithm
// Token bucket: มี bucket ขนาด N tokens
//   - แต่ละ request ใช้ 1 token
//   - เพิ่ม token ทุกๆ interval จนถึง N
type RateLimiterMiddleware struct {
	wrapped  LLMClient
	tokens   chan struct{}
	ticker   *time.Ticker
	stopChan chan struct{}
}

// NewRateLimiterMiddleware — สร้าง rate limiter
// rps = requests per second สูงสุด
func NewRateLimiterMiddleware(client LLMClient, rps int) *RateLimiterMiddleware {
	tokens := make(chan struct{}, rps)

	// เติม token เริ่มต้น
	for i := 0; i < rps; i++ {
		tokens <- struct{}{}
	}

	rl := &RateLimiterMiddleware{
		wrapped:  client,
		tokens:   tokens,
		ticker:   time.NewTicker(time.Second / time.Duration(rps)),
		stopChan: make(chan struct{}),
	}

	// goroutine เติม token ตาม rate
	go func() {
		for {
			select {
			case <-rl.ticker.C:
				select {
				case rl.tokens <- struct{}{}:
				default: // bucket เต็มแล้ว
				}
			case <-rl.stopChan:
				rl.ticker.Stop()
				return
			}
		}
	}()

	return rl
}

func (rl *RateLimiterMiddleware) Name() string { return "ratelimiter+" + rl.wrapped.Name() }

func (rl *RateLimiterMiddleware) Complete(ctx context.Context, req CompletionRequest) (*CompletionResponse, error) {
	// รอ token
	select {
	case <-rl.tokens:
	case <-ctx.Done():
		return nil, ctx.Err()
	}
	return rl.wrapped.Complete(ctx, req)
}

func (rl *RateLimiterMiddleware) Stream(ctx context.Context, req CompletionRequest, handler StreamHandler) error {
	select {
	case <-rl.tokens:
	case <-ctx.Done():
		return ctx.Err()
	}
	return rl.wrapped.Stream(ctx, req, handler)
}

// RetryMiddleware — retry อัตโนมัติด้วย exponential backoff + jitter
// Exponential backoff: รอนานขึ้นแบบ exponential หลังแต่ละ retry
// Jitter: สุ่ม delay เพื่อไม่ให้หลาย client retry พร้อมกัน (thundering herd)
//
// สูตร: delay = min(baseDelay * 2^attempt, maxDelay) + random(0, baseDelay)
type RetryMiddleware struct {
	wrapped    LLMClient
	maxRetries int
	baseDelay  time.Duration
	maxDelay   time.Duration
}

func NewRetryMiddleware(client LLMClient, maxRetries int) *RetryMiddleware {
	return &RetryMiddleware{
		wrapped:    client,
		maxRetries: maxRetries,
		baseDelay:  500 * time.Millisecond,
		maxDelay:   30 * time.Second,
	}
}

func (r *RetryMiddleware) Name() string { return "retry+" + r.wrapped.Name() }

// isRetryable — ตรวจว่า error นี้ควร retry หรือไม่
// 429 (rate limit) และ 5xx (server error) ควร retry
// 4xx อื่นๆ (400 bad request, 401 unauthorized) ไม่ควร retry
func isRetryable(err error) bool {
	if err == nil {
		return false
	}
	msg := err.Error()
	// Rate limit หรือ server errors ควร retry
	return strings.Contains(msg, "429") ||
		strings.Contains(msg, "500") ||
		strings.Contains(msg, "502") ||
		strings.Contains(msg, "503") ||
		strings.Contains(msg, "504")
}

func (r *RetryMiddleware) calculateDelay(attempt int) time.Duration {
	// Exponential backoff
	delay := r.baseDelay * time.Duration(math.Pow(2, float64(attempt)))
	if delay > r.maxDelay {
		delay = r.maxDelay
	}
	// Full jitter: random ระหว่าง 0 ถึง delay
	jitter := time.Duration(rand.Int63n(int64(r.baseDelay)))
	return delay + jitter
}

func (r *RetryMiddleware) Complete(ctx context.Context, req CompletionRequest) (*CompletionResponse, error) {
	var lastErr error
	for attempt := 0; attempt <= r.maxRetries; attempt++ {
		if attempt > 0 {
			delay := r.calculateDelay(attempt - 1)
			log.Printf("[retry] attempt %d/%d after %v (error: %v)",
				attempt, r.maxRetries, delay, lastErr)

			select {
			case <-time.After(delay):
			case <-ctx.Done():
				return nil, ctx.Err()
			}
		}

		resp, err := r.wrapped.Complete(ctx, req)
		if err == nil {
			return resp, nil
		}

		lastErr = err
		if !isRetryable(err) {
			return nil, err // ไม่ retry
		}
	}
	return nil, fmt.Errorf("max retries exceeded: %w", lastErr)
}

func (r *RetryMiddleware) Stream(ctx context.Context, req CompletionRequest, handler StreamHandler) error {
	var lastErr error
	for attempt := 0; attempt <= r.maxRetries; attempt++ {
		if attempt > 0 {
			delay := r.calculateDelay(attempt - 1)
			select {
			case <-time.After(delay):
			case <-ctx.Done():
				return ctx.Err()
			}
		}

		err := r.wrapped.Stream(ctx, req, handler)
		if err == nil {
			return nil
		}

		lastErr = err
		if !isRetryable(err) {
			return err
		}
	}
	return fmt.Errorf("max retries exceeded: %w", lastErr)
}

// CostTracker — middleware ที่ track tokens และ estimated cost ทุก request
// ราคาตัวอย่าง (USD per 1M tokens, อาจเปลี่ยนแปลงได้):
//   GPT-4o: input $2.50, output $10.00
//   Claude claude-opus-4-5: input $3.00, output $15.00
//   Claude claude-haiku-3-5: input $0.25, output $1.25
type CostTracker struct {
	wrapped      LLMClient
	mu           sync.Mutex
	totalInput   int
	totalOutput  int
	totalCostUSD float64
	requestCount int
}

// costPerMillionTokens — ราคาต่อ 1M tokens (USD)
var costPerMillionTokens = map[string][2]float64{
	// [input, output]
	"gpt-4o":              {2.50, 10.00},
	"gpt-4o-mini":         {0.15, 0.60},
	"gpt-3.5-turbo":       {0.50, 1.50},
	"claude-opus-4-5":  {3.00, 15.00},
	"claude-sonnet-4-5": {3.00, 15.00},
	"claude-haiku-3-5": {0.25, 1.25},
}

func NewCostTracker(client LLMClient) *CostTracker {
	return &CostTracker{wrapped: client}
}

func (ct *CostTracker) Name() string { return "costtracker+" + ct.wrapped.Name() }

func (ct *CostTracker) estimateCost(model string, inputTokens, outputTokens int) float64 {
	prices, ok := costPerMillionTokens[model]
	if !ok {
		// ใช้ GPT-4o เป็น default ถ้าไม่รู้จัก model
		prices = costPerMillionTokens["gpt-4o"]
	}
	inputCost := float64(inputTokens) * prices[0] / 1_000_000
	outputCost := float64(outputTokens) * prices[1] / 1_000_000
	return inputCost + outputCost
}

func (ct *CostTracker) Complete(ctx context.Context, req CompletionRequest) (*CompletionResponse, error) {
	start := time.Now()
	resp, err := ct.wrapped.Complete(ctx, req)
	elapsed := time.Since(start)

	if err != nil || resp == nil {
		return resp, err
	}

	cost := ct.estimateCost(resp.Model, resp.InputTokens, resp.OutputTokens)

	ct.mu.Lock()
	ct.totalInput += resp.InputTokens
	ct.totalOutput += resp.OutputTokens
	ct.totalCostUSD += cost
	ct.requestCount++
	ct.mu.Unlock()

	log.Printf("[cost] model=%s in=%d out=%d cost=$%.6f latency=%v",
		resp.Model, resp.InputTokens, resp.OutputTokens, cost, elapsed)

	return resp, nil
}

func (ct *CostTracker) Stream(ctx context.Context, req CompletionRequest, handler StreamHandler) error {
	return ct.wrapped.Stream(ctx, req, handler)
}

// Summary — สรุปค่าใช้จ่ายทั้งหมด
func (ct *CostTracker) Summary() {
	ct.mu.Lock()
	defer ct.mu.Unlock()
	fmt.Printf("\n=== Cost Summary ===\n")
	fmt.Printf("Total requests:       %d\n", ct.requestCount)
	fmt.Printf("Total input tokens:   %d\n", ct.totalInput)
	fmt.Printf("Total output tokens:  %d\n", ct.totalOutput)
	fmt.Printf("Estimated cost (USD): $%.6f\n", ct.totalCostUSD)
	// แปลงเป็น THB (สมมติ 1 USD = 35 THB)
	fmt.Printf("Estimated cost (THB): ฿%.4f\n", ct.totalCostUSD*35)
}

// ============================================================
// SECTION 5: Structured Output
// ============================================================
// Structured output = บังคับให้ LLM ตอบใน JSON format ที่เรากำหนด
//
// วิธีที่ได้ผลดีที่สุด:
//   1. อธิบาย JSON schema ใน system prompt
//   2. บอกให้ตอบ JSON อย่างเดียว ห้ามมีข้อความอื่น
//   3. Parse JSON จาก response
//   4. Validate ด้วย schema หรือ struct

// ProductInfo — struct ที่ต้องการ extract จากข้อความ
type ProductInfo struct {
	Name        string  `json:"name"`
	Price       float64 `json:"price"`
	Currency    string  `json:"currency"`
	Description string  `json:"description"`
	Category    string  `json:"category"`
}

// ExtractStructured — extract ข้อมูล structured จาก text ด้วย LLM
// เทคนิค: ส่ง JSON schema ใน system prompt
func ExtractStructured(ctx context.Context, client LLMClient, text string) (*ProductInfo, error) {
	// System prompt อธิบาย JSON schema ที่ต้องการ
	// วิธีนี้ได้ผลดีกว่าแค่บอกให้ตอบ JSON
	systemPrompt := `คุณคือ data extraction assistant.
ให้ extract ข้อมูลสินค้าจาก text และตอบกลับเป็น JSON เท่านั้น ห้ามมีข้อความอื่น

JSON Schema ที่ต้องการ:
{
  "name": "string — ชื่อสินค้า",
  "price": "number — ราคาตัวเลข (ไม่มีหน่วย)",
  "currency": "string — สกุลเงิน เช่น THB, USD",
  "description": "string — รายละเอียดสินค้า",
  "category": "string — หมวดหมู่สินค้า"
}

ตัวอย่าง output:
{"name":"สมาร์ทโฟน XR","price":9990,"currency":"THB","description":"หน้าจอ 6.1 นิ้ว","category":"อิเล็กทรอนิกส์"}`

	req := CompletionRequest{
		Messages: []Message{
			{Role: "system", Content: systemPrompt},
			{Role: "user", Content: "Extract product info from: " + text},
		},
		Model:       "gpt-4o",
		MaxTokens:   500,
		Temperature: 0, // temperature=0 สำหรับ structured output เพื่อให้ deterministic
	}

	resp, err := client.Complete(ctx, req)
	if err != nil {
		return nil, fmt.Errorf("llm request: %w", err)
	}

	// LLM อาจมี markdown fence รอบ JSON
	// ต้องทำความสะอาดก่อน parse
	cleaned := cleanJSONResponse(resp.Content)

	var info ProductInfo
	if err := json.Unmarshal([]byte(cleaned), &info); err != nil {
		return nil, fmt.Errorf("parse response json: %w", err)
	}

	return &info, nil
}

// cleanJSONResponse — ลบ markdown code fence ออกจาก LLM response
// LLM บางครั้งตอบ ```json ... ``` แม้เราบอกให้ตอบ JSON ล้วน
func cleanJSONResponse(s string) string {
	s = strings.TrimSpace(s)
	// ลบ ```json ... ``` หรือ ``` ... ```
	if strings.HasPrefix(s, "```") {
		lines := strings.Split(s, "\n")
		// ลบบรรทัดแรก (```json) และบรรทัดสุดท้าย (```)
		if len(lines) > 2 {
			lines = lines[1 : len(lines)-1]
		}
		s = strings.Join(lines, "\n")
	}
	return strings.TrimSpace(s)
}

// ============================================================
// SECTION 6: Mock implementations (ใช้เมื่อไม่มี API key)
// ============================================================

func mockOpenAIResponse(req CompletionRequest) *CompletionResponse {
	// ดึง user message ล่าสุดมาทำ mock response
	var userMsg string
	for _, m := range req.Messages {
		if m.Role == "user" {
			userMsg = m.Content
		}
	}

	var content string
	if strings.Contains(userMsg, "Extract product info") {
		// Mock structured output สำหรับ product extraction
		content = `{"name":"โน้ตบุ๊ค ASUS VivoBook","price":25900,"currency":"THB","description":"หน้าจอ 15.6 นิ้ว FHD, Intel Core i5, RAM 8GB, SSD 512GB","category":"คอมพิวเตอร์"}`
	} else {
		content = fmt.Sprintf("[OpenAI Mock] ตอบกลับ request: %.50s...", userMsg)
	}

	return &CompletionResponse{
		Content:      content,
		InputTokens:  len(userMsg)/4 + 50,  // ประมาณ token count
		OutputTokens: len(content) / 4,
		Model:        "gpt-4o",
		StopReason:   "stop",
	}
}

func mockOpenAIStream(req CompletionRequest, handler StreamHandler) error {
	words := strings.Fields("[OpenAI Stream Mock] สวัสดีครับ นี่คือ streaming response จาก mock OpenAI client ครับ")
	for _, w := range words {
		handler(StreamChunk{Delta: w + " "})
		time.Sleep(50 * time.Millisecond)
	}
	handler(StreamChunk{Done: true})
	return nil
}

func mockAnthropicResponse(req CompletionRequest) *CompletionResponse {
	var userMsg string
	for _, m := range req.Messages {
		if m.Role == "user" {
			userMsg = m.Content
		}
	}

	content := fmt.Sprintf("[Anthropic Mock] ตอบกลับ: %.50s...", userMsg)
	return &CompletionResponse{
		Content:      content,
		InputTokens:  len(userMsg)/4 + 50,
		OutputTokens: len(content) / 4,
		Model:        "claude-opus-4-5",
		StopReason:   "end_turn",
	}
}

func mockAnthropicStream(req CompletionRequest, handler StreamHandler) error {
	words := strings.Fields("[Anthropic Stream Mock] สวัสดีครับ นี่คือ streaming response จาก mock Anthropic client ครับ")
	for _, w := range words {
		handler(StreamChunk{Delta: w + " "})
		time.Sleep(50 * time.Millisecond)
	}
	handler(StreamChunk{Done: true})
	return nil
}

// ============================================================
// SECTION 7: Demo Main
// ============================================================

func main() {
	fmt.Println("=== Module 7: LLM API Integration Demo ===")
	fmt.Println("(รันด้วย mock mode — ไม่ต้องใช้ API key จริง)")
	fmt.Println()

	ctx := context.Background()

	// สร้าง clients (ไม่มี API key → ใช้ mock)
	openAIBase := NewOpenAIClient("")
	anthropicBase := NewAnthropicClient("")

	// สร้าง middleware chain สำหรับ OpenAI
	// ลำดับ: request → RateLimiter → Retry → CostTracker → OpenAI
	openAICostTracker := NewCostTracker(openAIBase)
	openAIRetry := NewRetryMiddleware(openAICostTracker, 3)
	openAIClient := NewRateLimiterMiddleware(openAIRetry, 10) // 10 req/sec

	// สร้าง middleware chain สำหรับ Anthropic
	anthropicCostTracker := NewCostTracker(anthropicBase)
	anthropicClient := NewRetryMiddleware(anthropicCostTracker, 3)

	// ─── Demo 1: Basic completion ───────────────────────────
	fmt.Println("--- Demo 1: Basic Completion ---")
	demoBasicCompletion(ctx, openAIClient, "OpenAI")
	demoBasicCompletion(ctx, anthropicClient, "Anthropic")

	// ─── Demo 2: Streaming response ─────────────────────────
	fmt.Println("\n--- Demo 2: Streaming Response ---")
	demoStreaming(ctx, openAIClient, "OpenAI")
	demoStreaming(ctx, anthropicBase, "Anthropic") // test base client stream

	// ─── Demo 3: Structured output ──────────────────────────
	fmt.Println("\n--- Demo 3: Structured Output ---")
	demoStructuredOutput(ctx, openAIClient)

	// ─── แสดง cost summary ──────────────────────────────────
	fmt.Println()
	openAICostTracker.Summary()
	anthropicCostTracker.Summary()
}

func demoBasicCompletion(ctx context.Context, client LLMClient, providerName string) {
	req := CompletionRequest{
		Messages: []Message{
			{Role: "system", Content: "คุณคือ assistant ที่ช่วยตอบคำถามเกี่ยวกับ Go programming"},
			{Role: "user", Content: "อธิบาย goroutine ให้เข้าใจง่ายๆ ใน 2 ประโยค"},
		},
		Model:       "gpt-4o",
		MaxTokens:   200,
		Temperature: 0.7,
	}

	resp, err := client.Complete(ctx, req)
	if err != nil {
		log.Printf("[%s] error: %v", providerName, err)
		return
	}

	fmt.Printf("[%s] Response: %s\n", providerName, resp.Content)
	fmt.Printf("[%s] Tokens: in=%d out=%d\n", providerName, resp.InputTokens, resp.OutputTokens)
}

func demoStreaming(ctx context.Context, client LLMClient, providerName string) {
	req := CompletionRequest{
		Messages: []Message{
			{Role: "user", Content: "นับ 1 ถึง 5 เป็นภาษาไทย"},
		},
		Model:     "gpt-4o",
		MaxTokens: 100,
		Stream:    true,
	}

	fmt.Printf("[%s] Streaming: ", providerName)
	err := client.Stream(ctx, req, func(chunk StreamChunk) {
		if chunk.Done {
			fmt.Println() // new line เมื่อ done
			return
		}
		if chunk.Err != nil {
			fmt.Printf("\n[error] %v\n", chunk.Err)
			return
		}
		fmt.Print(chunk.Delta) // print โดยไม่ขึ้นบรรทัดใหม่
	})

	if err != nil {
		log.Printf("[%s] stream error: %v", providerName, err)
	}
}

func demoStructuredOutput(ctx context.Context, client LLMClient) {
	// ตัวอย่างข้อความสินค้าจาก Thai e-commerce
	productTexts := []string{
		"ASUS VivoBook 15 โน้ตบุ๊ค หน้าจอ 15.6 นิ้ว FHD, Intel Core i5-1235U, RAM 8GB DDR4, SSD 512GB ราคา 25,900 บาท",
		"iPhone 15 Pro Max 256GB สีไทเทเนียมธรรมชาติ ราคา 49,900 บาท ประกันศูนย์ไทย 1 ปี",
	}

	for _, text := range productTexts {
		info, err := ExtractStructured(ctx, client, text)
		if err != nil {
			log.Printf("extract error: %v", err)
			continue
		}
		fmt.Printf("Product: %s | ราคา: %.0f %s | หมวด: %s\n",
			info.Name, info.Price, info.Currency, info.Category)
	}
}
