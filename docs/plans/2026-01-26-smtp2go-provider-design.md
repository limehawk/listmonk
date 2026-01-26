# smtp2go Email Provider Design

## Overview

Add smtp2go as a first-class email provider in listmonk, enabling users to send campaigns via the smtp2go API.

## Package Structure

**Location:** `internal/messenger/smtp2go/`

**Files:**
- `smtp2go.go` - Main implementation

**Core struct implementing `manager.Messenger`:**

```go
type SMTP2Go struct {
    name   string
    apiKey string
    c      *http.Client
}

func (s *SMTP2Go) Name() string
func (s *SMTP2Go) Push(m models.Message) error
func (s *SMTP2Go) Flush() error
func (s *SMTP2Go) Close() error
```

**Configuration:**

```go
type Options struct {
    Name     string        `json:"name"`
    APIKey   string        `json:"api_key"`
    MaxConns int           `json:"max_conns"`
    Timeout  time.Duration `json:"timeout"`
}
```

## API Integration

**Endpoint:** `https://api.smtp2go.com/v3/email/send`

**Request payload:**

```go
type payload struct {
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
    Fileblob string `json:"fileblob"`  // base64 encoded
    Mimetype string `json:"mimetype"`
}
```

**Field mapping from `models.Message`:**

| listmonk field | smtp2go field | Notes |
|----------------|---------------|-------|
| `m.From` | `sender` | Direct map |
| `m.To` | `to` | Already a `[]string` |
| `m.Subject` | `subject` | Direct map |
| `m.Body` | `html_body` or `text_body` | Based on `m.ContentType` |
| `m.AltBody` | `text_body` | Plain text alternative |
| `m.Headers` | `custom_headers` | Convert from `MIMEHeader` |
| `m.Attachments` | `attachments` | Base64 encode content |

**Response:**

```go
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
```

## Error Handling

| Scenario | Action |
|----------|--------|
| HTTP 401 | Return error: "invalid or disabled API key" |
| HTTP 400 | Parse response body, return `error` field |
| HTTP 2xx but `failed > 0` | Return error with `failures` details |
| HTTP 2xx and `succeeded > 0` | Success, return nil |
| Network timeout | Return error (listmonk handles retry) |

## Configuration

**Settings struct addition (`models/settings.go`):**

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

**TOML config example:**

```toml
[[smtp2go]]
enabled = true
name = "smtp2go"
api_key = "api-XXXXXXXXXXXXXXXX"
max_conns = 10
timeout = "5s"
max_msg_retries = 2
```

## Files to Modify

**New:**
- `internal/messenger/smtp2go/smtp2go.go`

**Modified:**
- `cmd/init.go` - Add `initSMTP2GoMessengers()` and wire into main init
- `models/settings.go` - Add `SMTP2Go` config struct

## References

- smtp2go API docs: https://developers.smtp2go.com/docs/send-an-email
- Existing postback messenger: `internal/messenger/postback/postback.go`
