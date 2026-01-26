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
