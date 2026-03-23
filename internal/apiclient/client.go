package apiclient

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"strings"
)

// APIError represents an HTTP error response from the API.
type APIError struct {
	StatusCode int
	Message    string
}

func (e *APIError) Error() string {
	return fmt.Sprintf("API error %d: %s", e.StatusCode, e.Message)
}

// Response holds the raw HTTP response data.
type Response struct {
	StatusCode int
	Body       []byte
}

// Client is an HTTP client wrapper for the Dibbla API.
type Client struct {
	BaseURL string
	Token   string
	Verbose bool
	http    *http.Client
}

// NewClient creates a new Client.
func NewClient(baseURL, token string, verbose bool) *Client {
	return &Client{
		BaseURL: baseURL,
		Token:   token,
		Verbose: verbose,
		http:    &http.Client{},
	}
}

func (c *Client) Get(path string) (*Response, error) {
	return c.do("GET", path, nil)
}

func (c *Client) Post(path string, body interface{}) (*Response, error) {
	return c.do("POST", path, body)
}

func (c *Client) Put(path string, body interface{}) (*Response, error) {
	return c.do("PUT", path, body)
}

func (c *Client) Patch(path string, body interface{}) (*Response, error) {
	return c.do("PATCH", path, body)
}

func (c *Client) Delete(path string) (*Response, error) {
	return c.do("DELETE", path, nil)
}

func (c *Client) do(method, path string, body interface{}) (*Response, error) {
	if c.http == nil {
		c.http = &http.Client{}
	}
	url := strings.TrimSuffix(c.BaseURL, "/") + path

	var reqBody io.Reader
	if body != nil {
		data, err := json.Marshal(body)
		if err != nil {
			return nil, fmt.Errorf("failed to marshal request body: %w", err)
		}
		reqBody = bytes.NewReader(data)
	}

	req, err := http.NewRequest(method, url, reqBody)
	if err != nil {
		return nil, fmt.Errorf("failed to create request: %w", err)
	}

	req.Header.Set("Authorization", "Bearer "+c.Token)
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json")

	if c.Verbose {
		fmt.Fprintf(os.Stderr, "%s %s\n", method, url)
	}

	resp, err := c.http.Do(req)
	if err != nil {
		return nil, fmt.Errorf("request failed: %w", err)
	}
	defer resp.Body.Close()

	respBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("failed to read response body: %w", err)
	}

	if c.Verbose {
		fmt.Fprintf(os.Stderr, "Status: %d\n", resp.StatusCode)
		fmt.Fprintf(os.Stderr, "Response: %s\n", string(respBody))
	}

	if resp.StatusCode >= 400 {
		return nil, &APIError{
			StatusCode: resp.StatusCode,
			Message:    string(respBody),
		}
	}

	return &Response{
		StatusCode: resp.StatusCode,
		Body:       respBody,
	}, nil
}

// ExitCodeForStatus maps an HTTP status code to a CLI exit code.
func ExitCodeForStatus(status int) int {
	switch status {
	case 401, 403:
		return 3
	case 404:
		return 4
	case 422:
		return 5
	case 409:
		return 6
	case 408:
		return 7
	default:
		return 1
	}
}

const validateTokenPath = "/api/auth/v1/tokens/validate"

// ValidateToken calls the token validation endpoint and returns an error if the token is invalid.
// baseURL should be the API base (e.g. https://api.dibbla.com) without trailing slash.
func ValidateToken(baseURL, token string) error {
	baseURL = strings.TrimSuffix(baseURL, "/")
	url := baseURL + validateTokenPath

	req, err := http.NewRequest("POST", url, nil)
	if err != nil {
		return fmt.Errorf("failed to create request: %w", err)
	}
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("Accept", "application/json")

	client := &http.Client{}
	resp, err := client.Do(req)
	if err != nil {
		return fmt.Errorf("request failed: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode >= 200 && resp.StatusCode < 300 {
		return nil
	}
	body, _ := io.ReadAll(resp.Body)
	msg := strings.TrimSpace(string(body))
	if msg == "" {
		switch resp.StatusCode {
		case 401:
			msg = "invalid or expired token"
		case 403:
			msg = "access denied"
		default:
			msg = fmt.Sprintf("validation failed (HTTP %d)", resp.StatusCode)
		}
	}
	return &APIError{StatusCode: resp.StatusCode, Message: msg}
}
