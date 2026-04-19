---
layout: default
title: "Go"
parent: "Client SDK Examples"
grand_parent: "Client Guide"
nav_order: 3
---

# Go Client Examples

Go examples for interacting with the Lenny REST API using `net/http`.

## Prerequisites

```
// go.mod
module lenny-go-client

go 1.21

require (
    // No external dependencies; uses only the standard library.
)
```

---

## Full Session Lifecycle

```go
// main.go: Lenny session lifecycle in Go.
//
// Run with: go run main.go
//
// Set environment variables:
//   LENNY_URL          - Lenny gateway URL (default: https://lenny.example.com)
//   OIDC_TOKEN_URL     - OIDC token endpoint
//   OIDC_CLIENT_ID     - OAuth client ID
//   OIDC_CLIENT_SECRET - OAuth client secret

package main

import (
	"bufio"
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"math"
	"math/rand"
	"mime/multipart"
	"net/http"
	"net/url"
	"os"
	"strings"
	"time"
)

// ---------------------------------------------------------------------------
// Configuration
// ---------------------------------------------------------------------------

var (
	lennyURL        = envOr("LENNY_URL", "https://lenny.example.com")
	oidcTokenURL    = envOr("OIDC_TOKEN_URL", "https://auth.example.com/oauth/token")
	oidcClientID    = envOr("OIDC_CLIENT_ID", "your-client-id")
	oidcClientSecret = envOr("OIDC_CLIENT_SECRET", "your-client-secret")
)

func envOr(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}

// ---------------------------------------------------------------------------
// API Response Types
// ---------------------------------------------------------------------------

type Session struct {
	SessionID             string                 `json:"sessionId"`
	State                 string                 `json:"state"`
	Runtime               string                 `json:"runtime,omitempty"`
	Pool                  string                 `json:"pool,omitempty"`
	CreatedAt             string                 `json:"createdAt,omitempty"`
	StartedAt             string                 `json:"startedAt,omitempty"`
	UploadToken           string                 `json:"uploadToken,omitempty"`
	SessionIsolationLevel *SessionIsolationLevel `json:"sessionIsolationLevel,omitempty"`
	Labels                map[string]string      `json:"labels,omitempty"`
}

type SessionIsolationLevel struct {
	ExecutionMode    string `json:"executionMode"`
	IsolationProfile string `json:"isolationProfile"`
	PodReuse         bool   `json:"podReuse"`
}

type PaginatedResponse[T any] struct {
	Items   []T    `json:"items"`
	Cursor  string `json:"cursor"`
	HasMore bool   `json:"hasMore"`
	Total   *int   `json:"total,omitempty"`
}

type Runtime struct {
	Name         string            `json:"name"`
	Type         string            `json:"type"`
	Labels       map[string]string `json:"labels,omitempty"`
	Capabilities map[string]bool   `json:"capabilities,omitempty"`
}

type Artifact struct {
	Path     string `json:"path"`
	Size     int64  `json:"size"`
	MimeType string `json:"mimeType"`
}

type Usage struct {
	InputTokens            int64      `json:"inputTokens"`
	OutputTokens           int64      `json:"outputTokens"`
	WallClockSeconds       float64    `json:"wallClockSeconds"`
	PodMinutes             float64    `json:"podMinutes"`
	CredentialLeaseMinutes float64    `json:"credentialLeaseMinutes"`
	TreeUsage              *TreeUsage `json:"treeUsage,omitempty"`
}

type TreeUsage struct {
	InputTokens  int64   `json:"inputTokens"`
	OutputTokens int64   `json:"outputTokens"`
	TotalTasks   int     `json:"totalTasks"`
	PodMinutes   float64 `json:"podMinutes"`
}

type LennyError struct {
	Code      string                 `json:"code"`
	Category  string                 `json:"category"`
	Message   string                 `json:"message"`
	Retryable bool                   `json:"retryable"`
	Details   map[string]interface{} `json:"details,omitempty"`
}

func (e *LennyError) Error() string {
	return fmt.Sprintf("[%s] %s", e.Code, e.Message)
}

type DeliveryReceipt struct {
	Status    string `json:"status"`
	Timestamp string `json:"timestamp"`
}

type MessageResponse struct {
	MessageID       string          `json:"messageId"`
	DeliveryReceipt DeliveryReceipt `json:"deliveryReceipt"`
}

type UploadResponse struct {
	Uploaded []struct {
		Path string `json:"path"`
		Size int64  `json:"size"`
	} `json:"uploaded"`
}

// ---------------------------------------------------------------------------
// Lenny Client
// ---------------------------------------------------------------------------

type LennyClient struct {
	httpClient *http.Client
	token      string
}

func NewLennyClient(token string) *LennyClient {
	return &LennyClient{
		httpClient: &http.Client{Timeout: 30 * time.Second},
		token:      token,
	}
}

// Request makes an API call with automatic retry for TRANSIENT errors.
func (c *LennyClient) Request(method, path string, body interface{}, result interface{}) error {
	return c.RequestWithHeaders(method, path, body, nil, result)
}

func (c *LennyClient) RequestWithHeaders(method, path string, body interface{}, extraHeaders map[string]string, result interface{}) error {
	const maxRetries = 5
	baseDelay := 1.0
	maxDelay := 60.0

	for attempt := 0; attempt <= maxRetries; attempt++ {
		var bodyReader io.Reader
		if body != nil {
			data, err := json.Marshal(body)
			if err != nil {
				return fmt.Errorf("marshal body: %w", err)
			}
			bodyReader = bytes.NewReader(data)
		}

		req, err := http.NewRequest(method, lennyURL+path, bodyReader)
		if err != nil {
			return fmt.Errorf("create request: %w", err)
		}

		req.Header.Set("Authorization", "Bearer "+c.token)
		if body != nil {
			req.Header.Set("Content-Type", "application/json")
		}
		for k, v := range extraHeaders {
			req.Header.Set(k, v)
		}

		resp, err := c.httpClient.Do(req)
		if err != nil {
			if attempt == maxRetries {
				return fmt.Errorf("request failed: %w", err)
			}
			wait := math.Min(baseDelay*math.Pow(2, float64(attempt))+rand.Float64(), maxDelay)
			fmt.Printf("  Retrying in %.1fs (network error, attempt %d/%d)\n", wait, attempt+1, maxRetries)
			time.Sleep(time.Duration(wait * float64(time.Second)))
			continue
		}
		defer resp.Body.Close()

		respBody, err := io.ReadAll(resp.Body)
		if err != nil {
			return fmt.Errorf("read response: %w", err)
		}

		if resp.StatusCode >= 200 && resp.StatusCode < 300 {
			if result != nil && len(respBody) > 0 {
				return json.Unmarshal(respBody, result)
			}
			return nil
		}

		// Parse error
		var errResp struct {
			Error LennyError `json:"error"`
		}
		if err := json.Unmarshal(respBody, &errResp); err != nil {
			return fmt.Errorf("HTTP %d: %s", resp.StatusCode, string(respBody))
		}

		if !errResp.Error.Retryable || attempt == maxRetries {
			return &errResp.Error
		}

		// Calculate wait
		wait := baseDelay*math.Pow(2, float64(attempt)) + rand.Float64()
		if ra := resp.Header.Get("Retry-After"); ra != "" {
			fmt.Sscanf(ra, "%f", &wait)
		}
		wait = math.Min(wait, maxDelay)

		fmt.Printf("  Retrying in %.1fs (%s, attempt %d/%d)\n",
			wait, errResp.Error.Code, attempt+1, maxRetries)
		time.Sleep(time.Duration(wait * float64(time.Second)))
	}

	return fmt.Errorf("max retries exceeded")
}

// UploadFiles uploads files using multipart/form-data.
func (c *LennyClient) UploadFiles(sessionID, uploadToken string, files map[string][]byte) (*UploadResponse, error) {
	var buf bytes.Buffer
	writer := multipart.NewWriter(&buf)

	for name, content := range files {
		part, err := writer.CreateFormFile("files", name)
		if err != nil {
			return nil, fmt.Errorf("create form file: %w", err)
		}
		if _, err := part.Write(content); err != nil {
			return nil, fmt.Errorf("write file content: %w", err)
		}
	}

	if err := writer.Close(); err != nil {
		return nil, fmt.Errorf("close multipart writer: %w", err)
	}

	req, err := http.NewRequest("POST", lennyURL+"/v1/sessions/"+sessionID+"/upload", &buf)
	if err != nil {
		return nil, err
	}

	req.Header.Set("Authorization", "Bearer "+c.token)
	req.Header.Set("Content-Type", writer.FormDataContentType())
	req.Header.Set("X-Upload-Token", uploadToken)

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	if resp.StatusCode != 200 {
		body, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("upload failed: %s", string(body))
	}

	var result UploadResponse
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return nil, err
	}
	return &result, nil
}

// StreamSession connects to the SSE stream and prints agent output.
func (c *LennyClient) StreamSession(sessionID string) error {
	var lastCursor string

	for {
		u := lennyURL + "/v1/sessions/" + sessionID + "/logs"
		if lastCursor != "" {
			u += "?cursor=" + url.QueryEscape(lastCursor)
		}

		req, err := http.NewRequest("GET", u, nil)
		if err != nil {
			return err
		}
		req.Header.Set("Authorization", "Bearer "+c.token)
		req.Header.Set("Accept", "text/event-stream")

		// Use a client without timeout for SSE
		sseClient := &http.Client{}
		resp, err := sseClient.Do(req)
		if err != nil {
			fmt.Println("\n[Connection lost, reconnecting...]")
			time.Sleep(1 * time.Second)
			continue
		}

		scanner := bufio.NewScanner(resp.Body)
		// SSE payloads can exceed the default 64 KB bufio scanner buffer
		// (e.g., agent_output with embedded base64 screenshots). Raise the
		// token ceiling to 10 MB to prevent silent truncation.
		scanner.Buffer(make([]byte, 0, 1024*1024), 10*1024*1024)
		var eventType string
		var dataLines []string

		for scanner.Scan() {
			line := scanner.Text()

			if strings.HasPrefix(line, "event: ") {
				eventType = line[7:]
			} else if strings.HasPrefix(line, "data: ") {
				dataLines = append(dataLines, line[6:])
			} else if strings.HasPrefix(line, "id: ") {
				lastCursor = line[4:]
			} else if line == "" {
				if eventType != "" && len(dataLines) > 0 {
					data := strings.Join(dataLines, "\n")

					switch eventType {
					case "agent_output":
						var event struct {
							Parts []struct {
								Type   string `json:"type"`
								Inline string `json:"inline"`
							} `json:"parts"`
						}
						if err := json.Unmarshal([]byte(data), &event); err == nil {
							for _, part := range event.Parts {
								if part.Type == "text" {
									fmt.Print(part.Inline)
								}
							}
						}
					case "status_change":
						var sc struct{ State string `json:"state"` }
						json.Unmarshal([]byte(data), &sc)
						fmt.Printf("\n[Status: %s]\n", sc.State)
					case "error":
						var e struct {
							Code    string `json:"code"`
							Message string `json:"message"`
						}
						json.Unmarshal([]byte(data), &e)
						fmt.Printf("\n[Error: %s - %s]\n", e.Code, e.Message)
					case "session_complete":
						fmt.Println("\n[Session complete]")
						resp.Body.Close()
						return nil
					case "checkpoint_boundary":
						var cb struct{ EventsLost int `json:"events_lost"` }
						json.Unmarshal([]byte(data), &cb)
						if cb.EventsLost > 0 {
							fmt.Printf("\n[WARNING: %d events lost]\n", cb.EventsLost)
						}
					}
				}
				eventType = ""
				dataLines = nil
			}
		}

		resp.Body.Close()

		if err := scanner.Err(); err != nil {
			fmt.Println("\n[Stream error, reconnecting...]")
			time.Sleep(1 * time.Second)
			continue
		}

		break
	}

	return nil
}

// Paginate iterates through all pages of a paginated endpoint.
func Paginate[T any](c *LennyClient, path string, limit int) ([]T, error) {
	var all []T
	cursor := ""

	for {
		queryPath := fmt.Sprintf("%s?limit=%d", path, limit)
		if cursor != "" {
			queryPath += "&cursor=" + url.QueryEscape(cursor)
		}

		var page PaginatedResponse[T]
		if err := c.Request("GET", queryPath, nil, &page); err != nil {
			return nil, err
		}

		all = append(all, page.Items...)

		if !page.HasMore || page.Cursor == "" {
			break
		}
		cursor = page.Cursor
	}

	return all, nil
}

// ---------------------------------------------------------------------------
// Authentication
// ---------------------------------------------------------------------------

func getAccessToken() (string, error) {
	data := url.Values{
		"grant_type":    {"client_credentials"},
		"client_id":     {oidcClientID},
		"client_secret": {oidcClientSecret},
		"scope":         {"openid profile"},
	}

	resp, err := http.PostForm(oidcTokenURL, data)
	if err != nil {
		return "", fmt.Errorf("token request failed: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != 200 {
		body, _ := io.ReadAll(resp.Body)
		return "", fmt.Errorf("token request failed (%d): %s", resp.StatusCode, string(body))
	}

	var tokenResp struct {
		AccessToken string `json:"access_token"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&tokenResp); err != nil {
		return "", err
	}
	return tokenResp.AccessToken, nil
}

// rotateLennyToken rotates the current Lenny access token via RFC 8693 token
// exchange. Call shortly before `exp` to avoid a gap in authorization. For
// delegation child-token minting, additionally set `actor_token` to the parent
// session token and narrow `scope`.
func rotateLennyToken(currentToken string) (string, error) {
	data := url.Values{
		"grant_type":           {"urn:ietf:params:oauth:grant-type:token-exchange"},
		"subject_token":        {currentToken},
		"subject_token_type":   {"urn:ietf:params:oauth:token-type:access_token"},
		"requested_token_type": {"urn:ietf:params:oauth:token-type:access_token"},
	}

	resp, err := http.PostForm(lennyURL+"/v1/oauth/token", data)
	if err != nil {
		return "", fmt.Errorf("token rotation failed: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != 200 {
		body, _ := io.ReadAll(resp.Body)
		return "", fmt.Errorf("token rotation failed (%d): %s", resp.StatusCode, string(body))
	}

	var tokenResp struct {
		AccessToken string `json:"access_token"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&tokenResp); err != nil {
		return "", err
	}
	return tokenResp.AccessToken, nil
}

// ---------------------------------------------------------------------------
// Main
// ---------------------------------------------------------------------------

func main() {
	fmt.Println("=== Lenny Go Client Example ===\n")

	// 1. Authenticate
	fmt.Println("1. Authenticating...")
	token, err := getAccessToken()
	if err != nil {
		fmt.Fprintf(os.Stderr, "Auth failed: %v\n", err)
		os.Exit(1)
	}
	fmt.Printf("   Token: %s...\n", token[:20])

	client := NewLennyClient(token)

	// 2. Discover runtimes
	fmt.Println("\n2. Discovering runtimes...")
	var runtimes PaginatedResponse[Runtime]
	if err := client.Request("GET", "/v1/runtimes", nil, &runtimes); err != nil {
		fmt.Fprintf(os.Stderr, "List runtimes: %v\n", err)
		os.Exit(1)
	}
	for _, rt := range runtimes.Items {
		fmt.Printf("   - %s (%s)\n", rt.Name, rt.Type)
	}

	runtimeName := "claude-worker"
	if len(runtimes.Items) > 0 {
		runtimeName = runtimes.Items[0].Name
	}

	// 3. Create session
	fmt.Printf("\n3. Creating session with '%s'...\n", runtimeName)
	var session Session
	if err := client.Request("POST", "/v1/sessions", map[string]interface{}{
		"runtime": runtimeName,
		"labels":  map[string]string{"example": "go-client"},
	}, &session); err != nil {
		fmt.Fprintf(os.Stderr, "Create session: %v\n", err)
		os.Exit(1)
	}
	fmt.Printf("   Session: %s\n", session.SessionID)

	// 4. Upload files
	fmt.Println("\n4. Uploading files...")
	uploaded, err := client.UploadFiles(session.SessionID, session.UploadToken, map[string][]byte{
		"main.go":   []byte("package main\n\nimport \"fmt\"\n\nfunc main() {\n\tfmt.Println(\"Hello\")\n}\n"),
		"README.md": []byte("# Example\n\nA simple Go program.\n"),
	})
	if err != nil {
		fmt.Fprintf(os.Stderr, "Upload: %v\n", err)
		os.Exit(1)
	}
	for _, f := range uploaded.Uploaded {
		fmt.Printf("   - %s (%d bytes)\n", f.Path, f.Size)
	}

	// 5. Finalize
	fmt.Println("\n5. Finalizing workspace...")
	var finalized Session
	if err := client.RequestWithHeaders("POST",
		"/v1/sessions/"+session.SessionID+"/finalize",
		nil, map[string]string{"X-Upload-Token": session.UploadToken},
		&finalized); err != nil {
		fmt.Fprintf(os.Stderr, "Finalize: %v\n", err)
		os.Exit(1)
	}
	fmt.Printf("   State: %s\n", finalized.State)

	// 6. Start
	fmt.Println("\n6. Starting session...")
	var started Session
	if err := client.Request("POST", "/v1/sessions/"+session.SessionID+"/start", nil, &started); err != nil {
		fmt.Fprintf(os.Stderr, "Start: %v\n", err)
		os.Exit(1)
	}
	fmt.Printf("   State: %s\n", started.State)

	// 7. Send message
	fmt.Println("\n7. Sending message...")
	var msg MessageResponse
	if err := client.Request("POST", "/v1/sessions/"+session.SessionID+"/messages", map[string]interface{}{
		"input": []map[string]string{
			{"type": "text", "inline": "Review the Go code in main.go. Suggest idiomatic improvements."},
		},
	}, &msg); err != nil {
		fmt.Fprintf(os.Stderr, "Send message: %v\n", err)
		os.Exit(1)
	}
	fmt.Printf("   Delivery: %s\n", msg.DeliveryReceipt.Status)

	// 8. Stream output
	fmt.Println("\n8. Streaming output:")
	fmt.Println(strings.Repeat("-", 40))
	if err := client.StreamSession(session.SessionID); err != nil {
		fmt.Fprintf(os.Stderr, "Stream: %v\n", err)
	}
	fmt.Println(strings.Repeat("-", 40))

	// 9. Artifacts
	fmt.Println("\n9. Artifacts:")
	artifacts, err := Paginate[Artifact](client, "/v1/sessions/"+session.SessionID+"/artifacts", 50)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Artifacts: %v\n", err)
	}
	for _, a := range artifacts {
		fmt.Printf("   - %s (%d bytes)\n", a.Path, a.Size)
	}

	// 10. Usage
	fmt.Println("\n10. Usage:")
	var usage Usage
	if err := client.Request("GET", "/v1/sessions/"+session.SessionID+"/usage", nil, &usage); err != nil {
		fmt.Fprintf(os.Stderr, "Usage: %v\n", err)
	} else {
		fmt.Printf("    Input tokens:  %d\n", usage.InputTokens)
		fmt.Printf("    Output tokens: %d\n", usage.OutputTokens)
		fmt.Printf("    Wall clock:    %.0fs\n", usage.WallClockSeconds)
	}

	// 11. Final state
	fmt.Println("\n11. Final state:")
	var final Session
	if err := client.Request("GET", "/v1/sessions/"+session.SessionID, nil, &final); err != nil {
		fmt.Fprintf(os.Stderr, "Get session: %v\n", err)
	} else {
		fmt.Printf("    State: %s\n", final.State)
		switch final.State {
		case "completed", "failed", "cancelled", "expired":
			// Already terminal
		default:
			fmt.Println("    Terminating...")
			client.Request("POST", "/v1/sessions/"+session.SessionID+"/terminate", nil, nil)
		}
	}

	fmt.Println("\n=== Done ===")
}
```
