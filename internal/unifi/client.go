// Package unifi talks to the UniFi Network application on a UniFi OS gateway
// such as the Dream Router 7: static DNS records, clients (DHCP reservations
// and per-client DNS names) and networks.
package unifi

import (
	"bytes"
	"context"
	"crypto/tls"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/cookiejar"
	"strings"
	"sync"
	"time"
)

// Config holds the connection settings.
type Config struct {
	Host               string // host or host:port, optionally with scheme
	Site               string
	Username, Password string
	InsecureSkipVerify bool
	// LoginRetryTimeout is how long to keep retrying a login refused with
	// HTTP 429 because the router's login limit was reached. 0 fails at once.
	LoginRetryTimeout time.Duration
	// Logf, if set, receives progress messages such as "router login limit reached".
	Logf func(format string, args ...any)
	// RetryIntervals overrides the waits between rate-limited login attempts
	// (the last one repeats); for tests.
	RetryIntervals []time.Duration
}

// APIError is an error response from the router. The newer "v2" endpoints
// (static DNS) and the classic endpoints (clients, networks) report errors
// differently; both are normalised to Code and Message.
type APIError struct {
	Method, Path string
	Status       int
	Code         string // e.g. "api.err.DuplicateFixedIP"
	Message      string
}

func (e *APIError) Error() string {
	msg := e.Message
	if e.Code != "" && e.Code != e.Message {
		msg = fmt.Sprintf("%s (%s)", e.Message, e.Code)
	}
	if e.Status == http.StatusTooManyRequests {
		msg += "; the router limits logins per minute (success.login.limit.count in " +
			"/usr/lib/ulp-go/config.props), wait a minute and try again"
	}
	return fmt.Sprintf("%s %s: HTTP %d: %s", e.Method, e.Path, e.Status, msg)
}

// HasCode reports whether err is an APIError with the given code.
func HasCode(err error, code string) bool {
	var apiErr *APIError
	return errors.As(err, &apiErr) && apiErr.Code == code
}

// IsNotFound reports whether err is an HTTP 404 from the router.
func IsNotFound(err error) bool {
	var apiErr *APIError
	return errors.As(err, &apiErr) && apiErr.Status == http.StatusNotFound
}

// Client is safe for concurrent use. It logs in on first use.
type Client struct {
	cfg     Config
	base    string
	network string // base URL of the Network application
	http    *http.Client

	mu       sync.Mutex // guards csrf and loggedIn, serialises requests
	csrf     string
	loggedIn bool

	retryIntervals []time.Duration
}

// Waits between login attempts after HTTP 429; the last one repeats.
var defaultRetryIntervals = []time.Duration{5 * time.Second, 10 * time.Second, 20 * time.Second}

// New creates a client.
func New(cfg Config) (*Client, error) {
	if cfg.Host == "" {
		return nil, errors.New("host is required")
	}
	if cfg.Username == "" || cfg.Password == "" {
		return nil, errors.New("username and password are required")
	}
	if cfg.Site == "" {
		cfg.Site = "default"
	}
	jar, _ := cookiejar.New(nil)
	base := cfg.Host
	if !strings.Contains(base, "://") {
		base = "https://" + base
	}
	base = strings.TrimSuffix(base, "/")
	intervals := defaultRetryIntervals
	if len(cfg.RetryIntervals) > 0 {
		intervals = cfg.RetryIntervals
	}
	return &Client{
		cfg:     cfg,
		base:    base,
		network: base + "/proxy/network",
		http: &http.Client{
			Jar:     jar,
			Timeout: 30 * time.Second,
			Transport: &http.Transport{
				// The gateway uses a self-signed certificate by default.
				TLSClientConfig: &tls.Config{InsecureSkipVerify: cfg.InsecureSkipVerify}, //nolint:gosec
			},
		},
		retryIntervals: intervals,
	}, nil
}

// v2 builds a URL for the Network application's v2 API (static DNS).
func (c *Client) v2(path string) string {
	return c.network + "/v2/api/site/" + c.cfg.Site + path
}

// classic builds a URL for the Network application's classic API.
func (c *Client) classic(path string) string {
	return c.network + "/api/s/" + c.cfg.Site + path
}

// do sends an authenticated request, logging in first if needed and again if
// the session has expired. out receives the decoded response body.
func (c *Client) do(ctx context.Context, method, url string, body, out any) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	for attempt := 1; ; attempt++ {
		if err := c.loginLocked(ctx); err != nil {
			return err
		}
		err := c.send(ctx, method, url, body, out)
		var apiErr *APIError
		if attempt == 1 && errors.As(err, &apiErr) && apiErr.Status == http.StatusUnauthorized {
			c.loggedIn = false
			continue
		}
		return err
	}
}

// classicDo calls a classic endpoint, whose responses wrap results as
// {"meta":{"rc":"ok"},"data":[...]}, and decodes data into out.
func (c *Client) classicDo(ctx context.Context, method, path string, body, out any) error {
	var env struct {
		Data json.RawMessage `json:"data"`
	}
	if err := c.do(ctx, method, c.classic(path), body, &env); err != nil {
		return err
	}
	if out != nil && len(env.Data) > 0 {
		if err := json.Unmarshal(env.Data, out); err != nil {
			return fmt.Errorf("%s %s: unexpected response: %w", method, path, err)
		}
	}
	return nil
}

// loginLocked logs in if there is no session, retrying while the router's
// login limit is reached, until LoginRetryTimeout. The caller holds mu.
func (c *Client) loginLocked(ctx context.Context) error {
	if c.loggedIn {
		return nil
	}
	body := map[string]any{"username": c.cfg.Username, "password": c.cfg.Password, "rememberMe": false}
	start := time.Now()
	deadline := start.Add(c.cfg.LoginRetryTimeout)
	for attempt := 0; ; attempt++ {
		err := c.send(ctx, http.MethodPost, c.base+"/api/auth/login", body, nil)
		if err == nil {
			c.loggedIn = true
			return nil
		}
		var apiErr *APIError
		if !errors.As(err, &apiErr) || apiErr.Status != http.StatusTooManyRequests {
			return fmt.Errorf("logging in to %s as %q: %w", c.base, c.cfg.Username, err)
		}
		remaining := time.Until(deadline)
		if remaining <= 0 {
			if c.cfg.LoginRetryTimeout > 0 {
				err = fmt.Errorf("still refused after retrying for %s: %w", time.Since(start).Round(time.Second), err)
			}
			return fmt.Errorf("logging in to %s as %q: %w", c.base, c.cfg.Username, err)
		}
		wait := min(c.retryIntervals[min(attempt, len(c.retryIntervals)-1)], remaining)
		if c.cfg.Logf != nil {
			c.cfg.Logf("router login limit reached, retrying in %s", wait.Round(time.Second))
		}
		select {
		case <-ctx.Done():
			return fmt.Errorf("logging in to %s: %w", c.base, ctx.Err())
		case <-time.After(wait):
		}
	}
}

// send performs one HTTP request. The caller holds mu.
func (c *Client) send(ctx context.Context, method, url string, body, out any) error {
	var reader io.Reader
	if body != nil {
		b, err := json.Marshal(body)
		if err != nil {
			return err
		}
		reader = bytes.NewReader(b)
	}
	req, err := http.NewRequestWithContext(ctx, method, url, reader)
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json")
	if c.csrf != "" {
		req.Header.Set("X-CSRF-Token", c.csrf)
	}
	resp, err := c.http.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	raw, err := io.ReadAll(resp.Body)
	if err != nil {
		return err
	}
	if t := resp.Header.Get("X-Updated-CSRF-Token"); t != "" {
		c.csrf = t
	} else if t := resp.Header.Get("X-CSRF-Token"); t != "" {
		c.csrf = t
	}
	path := strings.TrimPrefix(url, c.network)
	if resp.StatusCode >= 400 {
		return parseError(method, path, resp.StatusCode, raw)
	}
	if out != nil && len(raw) > 0 {
		if err := json.Unmarshal(raw, out); err != nil {
			return fmt.Errorf("%s %s: unexpected response: %w", method, path, err)
		}
	}
	return nil
}

// parseError understands both error formats:
//
//	v2:      {"code":"api.err.X","message":"Human text"}
//	classic: {"meta":{"rc":"error","msg":"api.err.X"},"data":[]}
//	login:   {"code":"AUTHENTICATION_FAILED_...","message":"Human text"}
func parseError(method, path string, status int, raw []byte) error {
	e := &APIError{Method: method, Path: path, Status: status}
	var payload struct {
		Code    string `json:"code"`
		Message string `json:"message"`
		Meta    struct {
			Msg string `json:"msg"`
		} `json:"meta"`
	}
	_ = json.Unmarshal(raw, &payload)
	switch {
	case payload.Message != "":
		e.Code, e.Message = payload.Code, payload.Message
	case payload.Meta.Msg != "":
		e.Code, e.Message = payload.Meta.Msg, describeCode(payload.Meta.Msg)
	default:
		e.Message = http.StatusText(status)
	}
	return e
}

// describeCode turns the classic API's bare error codes into readable text.
func describeCode(code string) string {
	switch code {
	case "api.err.DuplicateFixedIP":
		return "the IP address is already reserved for another device"
	case "api.err.InvalidFixedIP":
		return "the IP address is not valid for the network"
	case "api.err.MacUsed":
		return "a client with this MAC address already exists"
	case "api.err.LocalDnsRecordRequiresFixedIp":
		return "a device's DNS name requires a fixed IP address"
	case "api.err.InvalidPayload":
		return "the router rejected the request as invalid"
	case "api.err.NotFound":
		return "not found"
	}
	return code
}
