package gemini

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"time"

	"apiforge/internal/util/httpx"
)

const codeAssistBase = "https://cloudcode-pa.googleapis.com/v1internal"

func userAgent() string {
	if v := os.Getenv("GEMINI_USER_AGENT"); v != "" {
		return v
	}
	return "GeminiCLI/0.1.0 (darwin; arm64)"
}

func authHeaders(req *http.Request, token string) {
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("User-Agent", userAgent())
}

// discoverProject resolves the Code Assist cloudaicompanionProject id. Uses
// GEMINI_PROJECT if set; otherwise runs the loadCodeAssist handshake. For the
// free tier the project may legitimately be empty.
func discoverProject(ctx context.Context, c *creds, log *slog.Logger) string {
	if v := os.Getenv("GEMINI_PROJECT"); v != "" {
		return v
	}
	tok, err := c.AccessToken(ctx)
	if err != nil {
		return ""
	}
	body, _ := json.Marshal(map[string]any{"metadata": map[string]any{"pluginType": "GEMINI"}})
	lctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(lctx, http.MethodPost, codeAssistBase+":loadCodeAssist", bytes.NewReader(body))
	if err != nil {
		return ""
	}
	authHeaders(req, tok)
	res, err := httpx.Client.Do(req)
	if err != nil {
		return ""
	}
	defer res.Body.Close()
	if res.StatusCode != http.StatusOK {
		if log != nil {
			log.Warn("loadCodeAssist non-OK; continuing without a project id", "status", res.StatusCode)
		}
		return ""
	}
	var data struct {
		CloudaicompanionProject json.RawMessage `json:"cloudaicompanionProject"`
	}
	if json.NewDecoder(res.Body).Decode(&data) != nil {
		return ""
	}
	// The field is either a string id or an object {id}.
	var asString string
	if json.Unmarshal(data.CloudaicompanionProject, &asString) == nil && asString != "" {
		return asString
	}
	var asObj struct {
		ID string `json:"id"`
	}
	if json.Unmarshal(data.CloudaicompanionProject, &asObj) == nil {
		return asObj.ID
	}
	return ""
}

// geminiGenerate calls generateContent / streamGenerateContent with the Code
// Assist envelope.
func geminiGenerate(ctx context.Context, c *creds, model, project string, request any, stream bool) (*http.Response, error) {
	tok, err := c.AccessToken(ctx)
	if err != nil {
		return nil, fmt.Errorf("gemini token refresh: %w", err)
	}
	verb := "generateContent"
	if stream {
		verb = "streamGenerateContent?alt=sse"
	}
	envelope := map[string]any{"model": model, "request": request}
	if project != "" {
		envelope["project"] = project
	}
	body, err := json.Marshal(envelope)
	if err != nil {
		return nil, err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, codeAssistBase+":"+verb, bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	authHeaders(req, tok)
	return httpx.Client.Do(req)
}
