package sklclient

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"
)

type Options struct {
	BaseUserAgent string
	Timeout       time.Duration
	MaxRPS        float64
	AuthToken     string
	SKLTicket     string
}

type Client struct {
	baseURL *url.URL
	token   string

	authToken string
	sklTicket string

	httpClient *http.Client
	ua         string

	mu          sync.Mutex
	minInterval time.Duration
	lastRequest time.Time
}

func NewFromTokenURL(raw string, opt Options) (*Client, error) {
	u, err := url.Parse(raw)
	if err != nil {
		return nil, fmt.Errorf("parse url: %w", err)
	}
	q := u.Query()
	token := strings.TrimSpace(q.Get("token"))
	if token == "" {
		return nil, fmt.Errorf("token not found in url query")
	}

	authToken := strings.TrimSpace(opt.AuthToken)
	if authToken == "" {
		authToken = strings.TrimSpace(q.Get("sessionId"))
	}
	if authToken == "" {
		authToken = strings.TrimSpace(q.Get("x-auth-token"))
	}

	sklTicket := strings.TrimSpace(opt.SKLTicket)
	if sklTicket == "" {
		sklTicket = strings.TrimSpace(q.Get("skl-ticket"))
	}
	if sklTicket == "" {
		sklTicket = strings.TrimSpace(q.Get("skl_ticket"))
	}
	if sklTicket == "" {
		sklTicket = strings.TrimSpace(q.Get("jsapi_ticket"))
	}

	base := &url.URL{
		Scheme: u.Scheme,
		Host:   u.Host,
	}

	timeout := opt.Timeout
	if timeout <= 0 {
		timeout = 15 * time.Second
	}
	ua := strings.TrimSpace(opt.BaseUserAgent)
	if ua == "" {
		ua = "hduwords/0.1"
	}

	maxRPS := opt.MaxRPS
	if maxRPS <= 0 {
		maxRPS = 2
	}

	return &Client{
		baseURL:   base,
		token:     token,
		authToken: authToken,
		sklTicket: sklTicket,
		httpClient: &http.Client{
			Timeout: timeout,
		},
		ua:          ua,
		minInterval: time.Duration(float64(time.Second) / maxRPS),
	}, nil
}

type APIError struct {
	StatusCode int
	Code       int    `json:"code"`
	Msg        string `json:"msg"`
	Endpoint   string
}

func (e *APIError) Error() string {
	if e.Code != 0 || e.Msg != "" {
		return fmt.Sprintf("%s: http=%d code=%d msg=%q", e.Endpoint, e.StatusCode, e.Code, e.Msg)
	}
	return fmt.Sprintf("%s: http=%d", e.Endpoint, e.StatusCode)
}

func (c *Client) do(ctx context.Context, method, path string, query url.Values, body any, out any) error {
	u := c.baseURL.ResolveReference(&url.URL{Path: path})
	if query == nil {
		query = url.Values{}
	}
	query.Set("token", c.token)
	u.RawQuery = query.Encode()

	var r io.Reader
	if body != nil {
		b, err := json.Marshal(body)
		if err != nil {
			return fmt.Errorf("marshal request body: %w", err)
		}
		r = bytes.NewReader(b)
	}

	req, err := http.NewRequestWithContext(ctx, method, u.String(), r)
	if err != nil {
		return fmt.Errorf("new request: %w", err)
	}
	req.Header.Set("User-Agent", c.ua)
	if c.authToken != "" {
		req.Header.Set("X-Auth-Token", c.authToken)
	}
	if c.sklTicket != "" {
		req.Header.Set("skl-ticket", c.sklTicket)
	}
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("Accept", "application/json")
	}

	if err := c.rateLimit(ctx); err != nil {
		return err
	}

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return fmt.Errorf("request: %w", err)
	}
	defer resp.Body.Close()

	data, err := io.ReadAll(resp.Body)
	if err != nil {
		return fmt.Errorf("read response: %w", err)
	}

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		var apiErr APIError
		apiErr.StatusCode = resp.StatusCode
		apiErr.Endpoint = fmt.Sprintf("%s %s", method, path)
		_ = json.Unmarshal(data, &apiErr)
		return &apiErr
	}

	if out == nil || len(data) == 0 {
		return nil
	}
	if err := json.Unmarshal(data, out); err != nil {
		return fmt.Errorf("decode response (%s %s): %w", method, path, err)
	}

	if v, ok := out.(interface{ APIErrorFields() (int, string) }); ok {
		code, msg := v.APIErrorFields()
		if code != 0 {
			return &APIError{
				StatusCode: resp.StatusCode,
				Code:       code,
				Msg:        msg,
				Endpoint:   fmt.Sprintf("%s %s", method, path),
			}
		}
	}

	return nil
}

func (c *Client) rateLimit(ctx context.Context) error {
	c.mu.Lock()
	defer c.mu.Unlock()

	now := time.Now()
	if c.lastRequest.IsZero() {
		c.lastRequest = now
		return nil
	}

	wait := c.minInterval - now.Sub(c.lastRequest)
	if wait <= 0 {
		c.lastRequest = now
		return nil
	}

	timer := time.NewTimer(wait)
	defer timer.Stop()

	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-timer.C:
		c.lastRequest = time.Now()
		return nil
	}
}

func (c *Client) mustValues(kv ...string) (url.Values, error) {
	if len(kv)%2 != 0 {
		return nil, errors.New("mustValues expects even kv length")
	}
	v := url.Values{}
	for i := 0; i < len(kv); i += 2 {
		v.Set(kv[i], kv[i+1])
	}
	return v, nil
}
