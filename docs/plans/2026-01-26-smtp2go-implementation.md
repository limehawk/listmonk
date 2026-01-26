# smtp2go Provider Implementation Plan

> **For Claude:** REQUIRED SUB-SKILL: Use superpowers:executing-plans to implement this plan task-by-task.

**Goal:** Add smtp2go as a first-class email provider in listmonk, enabling users to send campaigns via the smtp2go API.

**Architecture:** Create a new messenger package at `internal/messenger/smtp2go/` following the existing postback pattern. The provider sends emails via HTTP POST to `https://api.smtp2go.com/v3/email/send` with JSON payloads. Configuration is loaded from the `smtp2go` section in settings.

**Tech Stack:** Go, net/http, encoding/json, smtp2go REST API

---

## Task 1: Create smtp2go Messenger Package

**Files:**
- Create: `internal/messenger/smtp2go/smtp2go.go`

**Step 1: Create the package directory**

Run: `mkdir -p internal/messenger/smtp2go`

**Step 2: Write the smtp2go messenger implementation**

Create file `internal/messenger/smtp2go/smtp2go.go`:

```go
package smtp2go

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"time"

	"github.com/knadh/listmonk/models"
)

const apiEndpoint = "https://api.smtp2go.com/v3/email/send"

// Options represents smtp2go messenger options.
type Options struct {
	Name     string        `json:"name"`
	APIKey   string        `json:"api_key"`
	MaxConns int           `json:"max_conns"`
	Timeout  time.Duration `json:"timeout"`
}

// SMTP2Go represents the smtp2go messenger.
type SMTP2Go struct {
	name   string
	apiKey string
	c      *http.Client
}

// request is the JSON payload sent to smtp2go API.
type request struct {
	APIKey        string       `json:"api_key"`
	Sender        string       `json:"sender"`
	To            []string     `json:"to"`
	Subject       string       `json:"subject"`
	TextBody      string       `json:"text_body,omitempty"`
	HTMLBody      string       `json:"html_body,omitempty"`
	CustomHeaders []header     `json:"custom_headers,omitempty"`
	Attachments   []attachment `json:"attachments,omitempty"`
}

type header struct {
	Header string `json:"header"`
	Value  string `json:"value"`
}

type attachment struct {
	Filename string `json:"filename"`
	Fileblob string `json:"fileblob"`
	Mimetype string `json:"mimetype"`
}

// response is the JSON response from smtp2go API.
type response struct {
	RequestID string `json:"request_id"`
	Data      struct {
		Succeeded int      `json:"succeeded"`
		Failed    int      `json:"failed"`
		Failures  []string `json:"failures"`
		EmailID   string   `json:"email_id"`
	} `json:"data"`
	Error string `json:"error,omitempty"`
}

// New returns a new instance of the smtp2go messenger.
func New(o Options) (*SMTP2Go, error) {
	if o.APIKey == "" {
		return nil, fmt.Errorf("smtp2go: api_key is required")
	}

	timeout := o.Timeout
	if timeout == 0 {
		timeout = time.Second * 10
	}

	maxConns := o.MaxConns
	if maxConns == 0 {
		maxConns = 10
	}

	return &SMTP2Go{
		name:   o.Name,
		apiKey: o.APIKey,
		c: &http.Client{
			Timeout: timeout,
			Transport: &http.Transport{
				MaxIdleConnsPerHost:   maxConns,
				MaxConnsPerHost:       maxConns,
				ResponseHeaderTimeout: timeout,
				IdleConnTimeout:       timeout,
			},
		},
	}, nil
}

// Name returns the messenger's name.
func (s *SMTP2Go) Name() string {
	return s.name
}

// Push sends a message via smtp2go API.
func (s *SMTP2Go) Push(m models.Message) error {
	req := request{
		APIKey:  s.apiKey,
		Sender:  m.From,
		To:      m.To,
		Subject: m.Subject,
	}

	// Set body based on content type.
	if m.ContentType == "plain" {
		req.TextBody = string(m.Body)
	} else {
		req.HTMLBody = string(m.Body)
		// Include plain text alternative if available.
		if len(m.AltBody) > 0 {
			req.TextBody = string(m.AltBody)
		}
	}

	// Convert headers from MIMEHeader format.
	if len(m.Headers) > 0 {
		for k, vals := range m.Headers {
			for _, v := range vals {
				req.CustomHeaders = append(req.CustomHeaders, header{
					Header: k,
					Value:  v,
				})
			}
		}
	}

	// Convert attachments with base64 encoding.
	if len(m.Attachments) > 0 {
		for _, a := range m.Attachments {
			mimetype := "application/octet-stream"
			if ct := a.Header.Get("Content-Type"); ct != "" {
				mimetype = ct
			}

			req.Attachments = append(req.Attachments, attachment{
				Filename: a.Name,
				Fileblob: base64.StdEncoding.EncodeToString(a.Content),
				Mimetype: mimetype,
			})
		}
	}

	// Marshal and send.
	body, err := json.Marshal(req)
	if err != nil {
		return fmt.Errorf("smtp2go: error marshaling request: %w", err)
	}

	return s.send(body)
}

// Flush is a no-op as smtp2go doesn't batch.
func (s *SMTP2Go) Flush() error {
	return nil
}

// Close closes idle HTTP connections.
func (s *SMTP2Go) Close() error {
	s.c.CloseIdleConnections()
	return nil
}

func (s *SMTP2Go) send(body []byte) error {
	req, err := http.NewRequest(http.MethodPost, apiEndpoint, bytes.NewReader(body))
	if err != nil {
		return fmt.Errorf("smtp2go: error creating request: %w", err)
	}

	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("User-Agent", "listmonk")

	resp, err := s.c.Do(req)
	if err != nil {
		return fmt.Errorf("smtp2go: error sending request: %w", err)
	}
	defer func() {
		io.Copy(io.Discard, resp.Body)
		resp.Body.Close()
	}()

	// Read response body.
	respBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return fmt.Errorf("smtp2go: error reading response: %w", err)
	}

	// Handle HTTP errors.
	if resp.StatusCode == http.StatusUnauthorized {
		return fmt.Errorf("smtp2go: invalid or disabled API key")
	}

	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("smtp2go: API error (status %d): %s", resp.StatusCode, string(respBody))
	}

	// Parse response.
	var r response
	if err := json.Unmarshal(respBody, &r); err != nil {
		return fmt.Errorf("smtp2go: error parsing response: %w", err)
	}

	// Check for failures.
	if r.Data.Failed > 0 {
		return fmt.Errorf("smtp2go: %d message(s) failed: %v", r.Data.Failed, r.Data.Failures)
	}

	if r.Error != "" {
		return fmt.Errorf("smtp2go: %s", r.Error)
	}

	return nil
}
```

**Step 3: Verify the file compiles**

Run: `go build ./internal/messenger/smtp2go/`
Expected: No output (successful build)

**Step 4: Commit**

```bash
git add internal/messenger/smtp2go/smtp2go.go
git commit -m "feat: add smtp2go messenger package"
```

---

## Task 2: Add smtp2go Configuration to Settings Model

**Files:**
- Modify: `models/settings.go:99-111`

**Step 1: Add SMTP2Go struct to Settings**

In `models/settings.go`, add after the `Messengers` struct (around line 111):

```go
	SMTP2Go []struct {
		UUID          string `json:"uuid"`
		Enabled       bool   `json:"enabled"`
		Name          string `json:"name"`
		APIKey        string `json:"api_key,omitempty"`
		MaxConns      int    `json:"max_conns"`
		Timeout       string `json:"timeout"`
		MaxMsgRetries int    `json:"max_msg_retries"`
	} `json:"smtp2go"`
```

**Step 2: Verify the file compiles**

Run: `go build ./models/`
Expected: No output (successful build)

**Step 3: Commit**

```bash
git add models/settings.go
git commit -m "feat: add smtp2go configuration to settings model"
```

---

## Task 3: Add smtp2go Initialization Function

**Files:**
- Modify: `cmd/init.go:46-48` (imports)
- Modify: `cmd/init.go:683-717` (add init function after initPostbackMessengers)

**Step 1: Add import for smtp2go package**

In `cmd/init.go`, add to imports (around line 47):

```go
	"github.com/knadh/listmonk/internal/messenger/smtp2go"
```

**Step 2: Add initSMTP2GoMessengers function**

Add after `initPostbackMessengers` function (around line 717):

```go
// initSMTP2GoMessengers initializes and returns all the enabled
// smtp2go messenger backends.
func initSMTP2GoMessengers(ko *koanf.Koanf) []manager.Messenger {
	items := ko.Slices("smtp2go")
	if len(items) == 0 {
		return nil
	}

	var out []manager.Messenger
	for _, item := range items {
		if !item.Bool("enabled") {
			continue
		}

		// Read the smtp2go config.
		var (
			name = item.String("name")
			o    smtp2go.Options
		)
		if err := item.UnmarshalWithConf("", &o, koanf.UnmarshalConf{Tag: "json"}); err != nil {
			lo.Fatalf("error reading smtp2go config: %v", err)
		}

		// Initialize the Messenger.
		m, err := smtp2go.New(o)
		if err != nil {
			lo.Fatalf("error initializing smtp2go messenger %s: %v", name, err)
		}
		out = append(out, m)

		lo.Printf("loaded smtp2go messenger: %s", name)
	}

	return out
}
```

**Step 3: Verify the file compiles**

Run: `go build ./cmd/`
Expected: No output (successful build)

**Step 4: Commit**

```bash
git add cmd/init.go
git commit -m "feat: add smtp2go messenger initialization"
```

---

## Task 4: Wire smtp2go into Main Initialization

**Files:**
- Modify: `cmd/main.go:27-28` (imports)
- Modify: `cmd/main.go:213` (messenger initialization)

**Step 1: Add import for smtp2go package**

In `cmd/main.go`, add to imports (if not auto-imported):

```go
	// No additional import needed - smtp2go init is in cmd/init.go
```

**Step 2: Update messenger initialization**

In `cmd/main.go`, modify line 213 to include smtp2go:

Change from:
```go
		msgrs = append(initSMTPMessengers(), initPostbackMessengers(ko)...)
```

To:
```go
		msgrs = append(initSMTPMessengers(), initPostbackMessengers(ko)...)
		msgrs = append(msgrs, initSMTP2GoMessengers(ko)...)
```

**Step 3: Verify the full build compiles**

Run: `go build ./...`
Expected: No output (successful build)

**Step 4: Commit**

```bash
git add cmd/main.go
git commit -m "feat: wire smtp2go messenger into main initialization"
```

---

## Task 5: Add Configuration Sample

**Files:**
- Modify: `config.toml.sample`

**Step 1: Add smtp2go configuration example**

Add after the `[[messengers]]` section:

```toml
# smtp2go messenger (https://www.smtp2go.com)
# Sends emails via the smtp2go HTTP API.
# Multiple [[smtp2go]] blocks can be defined for multiple providers.
# [[smtp2go]]
# enabled = false
# name = "smtp2go"
# api_key = "api-XXXXXXXXXXXXXXXXXXXXXXXXXXXXXXXX"
# max_conns = 10
# timeout = "5s"
# max_msg_retries = 2
```

**Step 2: Commit**

```bash
git add config.toml.sample
git commit -m "docs: add smtp2go configuration sample"
```

---

## Task 6: Final Verification

**Step 1: Run full build**

Run: `go build -o listmonk ./cmd/`
Expected: Binary created successfully

**Step 2: Run any existing tests**

Run: `go test ./...`
Expected: All tests pass (or skip if no test files)

**Step 3: Verify with sample config**

Create a test config with smtp2go enabled and verify the binary starts (then Ctrl+C):

```bash
# Skip this if no database is available - just verify build works
```

**Step 4: Final commit with all changes squashed or as feature branch**

The feature branch `feature/smtp2go-provider` is ready for PR.

---

## Summary of Files Changed

| File | Action | Description |
|------|--------|-------------|
| `internal/messenger/smtp2go/smtp2go.go` | Create | New messenger implementation |
| `models/settings.go` | Modify | Add SMTP2Go config struct |
| `cmd/init.go` | Modify | Add import + init function |
| `cmd/main.go` | Modify | Wire smtp2go into messenger list |
| `config.toml.sample` | Modify | Add configuration example |
