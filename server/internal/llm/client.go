package llm

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/chy/chat2db/server/internal/model"
	"github.com/chy/chat2db/server/internal/service"
)

// ChatRequest is what the UI asks for.
type ChatRequest struct {
	Prompt    string    `json:"prompt"`
	Dialect   string    `json:"dialect"` // postgres, mysql, ...
	TableDDL  string    `json:"table_ddl,omitempty"`
	Selection string    `json:"selection,omitempty"`
	Messages  []ChatMsg `json:"messages,omitempty"`
}

// ChatMsg is a single message in OpenAI Chat Completion format.
type ChatMsg struct {
	Role    string `json:"role"`
	Content string `json:"content"`
}

// ChatResponse returns the generated SQL (and optional explanation).
type ChatResponse struct {
	SQL         string `json:"sql"`
	Explanation string `json:"explanation,omitempty"`
	Raw         string `json:"raw"`
}

// ErrNotConfigured is returned when a user has no LLM configuration.
var ErrNotConfigured = errors.New("LLM is not configured for this user")

// resolveCredential 返回可用的 (endpoint, model, apiKey)。
// 优先使用用户自己的 LLM 配置；若未配置，则尝试查找该用户所在任意组的 Owner，
// 只要该组 share_llm=true 且 Owner 配置齐全就使用 Owner 的凭据。
func resolveCredential(u *model.User) (endpoint, modelName, apiKey string, err error) {
	endpoint = strings.TrimRight(u.LLMEndpoint, "/")
	modelName = u.LLMModel
	if endpoint != "" && modelName != "" && u.LLMAPIKeyEnc != "" {
		apiKey, err = service.GetLLMAPIKey(u)
		return
	}

	// 查找共享给该用户的 Owner 配置
	owner, err2 := service.FindSharedLLMOwner(u.ID)
	if err2 != nil {
		err = err2
		return
	}
	if owner == nil {
		err = ErrNotConfigured
		return
	}
	endpoint = strings.TrimRight(owner.LLMEndpoint, "/")
	modelName = owner.LLMModel
	apiKey, err = service.GetLLMAPIKey(owner)
	return
}

// Chat forwards the user's prompt to the user-configured OpenAI-compatible endpoint
// and returns a distilled SQL answer.
func Chat(ctx context.Context, u *model.User, req ChatRequest) (*ChatResponse, error) {
	endpoint, modelName, apiKey, err := resolveCredential(u)
	if err != nil {
		return nil, err
	}
	if !strings.Contains(endpoint, "/chat/completions") {
		endpoint = endpoint + "/v1/chat/completions"
	}

	dialect := req.Dialect
	if dialect == "" {
		dialect = "postgres"
	}
	system := fmt.Sprintf(`You are a senior SQL engineer. Write %s SQL only.
Rules:
- Always output a single ready-to-execute SQL statement inside a fenced code block tagged sql.
- After the code block, optionally add a one-line explanation in the user's language.
- Do NOT include DROP/TRUNCATE/ALTER unless the user explicitly asked.
- Assume the default schema is public unless specified.
- For PostgreSQL sequence reset questions, prefer SELECT setval('table_column_seq', value); using the explicit sequence name. Do not use pg_get_serial_sequence unless the user asks for dynamic sequence lookup.`, dialect)

	var messages []ChatMsg
	messages = append(messages, ChatMsg{Role: "system", Content: system})
	if req.TableDDL != "" {
		messages = append(messages, ChatMsg{
			Role:    "system",
			Content: "Relevant schema/DDL for reference:\n" + req.TableDDL,
		})
	}
	if req.Selection != "" {
		messages = append(messages, ChatMsg{
			Role:    "system",
			Content: "User has selected this SQL snippet to modify or extend:\n" + req.Selection,
		})
	}
	for _, m := range req.Messages {
		messages = append(messages, m)
	}
	messages = append(messages, ChatMsg{Role: "user", Content: req.Prompt})

	body, _ := json.Marshal(map[string]any{
		"model":       modelName,
		"messages":    messages,
		"temperature": 0.2,
		"stream":      false,
	})

	hr, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	hr.Header.Set("Content-Type", "application/json")
	hr.Header.Set("Authorization", "Bearer "+apiKey)

	cli := &http.Client{Timeout: 60 * time.Second}
	resp, err := cli.Do(hr)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(resp.Body)
	if resp.StatusCode >= 400 {
		return nil, fmt.Errorf("llm http %d: %s", resp.StatusCode, string(raw))
	}
	var parsed struct {
		Choices []struct {
			Message ChatMsg `json:"message"`
		} `json:"choices"`
	}
	if err := json.Unmarshal(raw, &parsed); err != nil || len(parsed.Choices) == 0 {
		return &ChatResponse{Raw: string(raw)}, nil
	}
	content := parsed.Choices[0].Message.Content
	sql, expl := extractSQL(content)
	return &ChatResponse{SQL: sql, Explanation: expl, Raw: content}, nil
}

// extractSQL extracts a ```sql ... ``` block; falls back to the whole content.
func extractSQL(content string) (string, string) {
	lines := strings.Split(content, "\n")
	var inBlock bool
	var sqlBuf, restBuf strings.Builder
	for _, ln := range lines {
		trim := strings.TrimSpace(ln)
		if !inBlock && (strings.HasPrefix(trim, "```sql") || strings.HasPrefix(trim, "```SQL") || strings.HasPrefix(trim, "```postgres")) {
			inBlock = true
			continue
		}
		if inBlock && strings.HasPrefix(trim, "```") {
			inBlock = false
			continue
		}
		if inBlock {
			sqlBuf.WriteString(ln)
			sqlBuf.WriteByte('\n')
		} else {
			restBuf.WriteString(ln)
			restBuf.WriteByte('\n')
		}
	}
	sql := strings.TrimSpace(sqlBuf.String())
	expl := strings.TrimSpace(restBuf.String())
	if sql == "" {
		return strings.TrimSpace(content), ""
	}
	return sql, expl
}
