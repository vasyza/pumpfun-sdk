package transport

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math"
	"math/rand/v2"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/rs/zerolog"
	"github.com/vasyza/pumpfun-sdk/internal/errs"
	"golang.org/x/time/rate"
)

const (
	DefaultAPIBaseURL = "https://frontend-api-v3.pump.fun"
	DefaultRPCURL     = "https://api.mainnet-beta.solana.com"
	DefaultWSURL      = "wss://pumpportal.fun/api/data"
	DefaultTimeout    = 20 * time.Second
	Version           = "0.1.0"
)

// RetryPolicy limits retries for temporary HTTP and WebSocket failures.
// MaxRetries is the number of extra attempts. Zero permits one attempt.
type RetryPolicy struct {
	MaxRetries     int
	InitialBackoff time.Duration
	MaxBackoff     time.Duration
}

// Options configures a client. Zero values select defaults, except in Retry.
// A nil Retry selects three retries. An explicit policy can select no retries.
type Options struct {
	APIBaseURL        string
	RPCURL            string
	WSURL             string
	Timeout           time.Duration
	HTTPClient        *http.Client
	Retry             *RetryPolicy
	RequestsPerSecond float64
	Burst             int
	MaxResponseBytes  int64
	Logger            zerolog.Logger
}

// Transport sends HTTP requests. Its settings are immutable.
type Transport struct {
	apiBase    *url.URL
	rpcURL     string
	wsURL      string
	timeout    time.Duration
	httpClient *http.Client
	retry      RetryPolicy
	limiter    *rate.Limiter
	maxBody    int64
	logger     zerolog.Logger
}

// New checks the settings and creates a shared transport.
func New(opts Options) (*Transport, error) {
	if opts.APIBaseURL == "" {
		opts.APIBaseURL = DefaultAPIBaseURL
	}
	if opts.RPCURL == "" {
		opts.RPCURL = DefaultRPCURL
	}
	if opts.WSURL == "" {
		opts.WSURL = DefaultWSURL
	}
	api, err := parseEndpoint(opts.APIBaseURL, false)
	if err != nil {
		return nil, errs.Invalid("client", "The API URL is not valid.")
	}
	if _, err := parseEndpoint(opts.RPCURL, false); err != nil {
		return nil, errs.Invalid("client", "The RPC URL is not valid.")
	}
	if _, err := parseEndpoint(opts.WSURL, true); err != nil {
		return nil, errs.Invalid("client", "The WebSocket URL is not valid.")
	}
	if opts.Timeout == 0 {
		opts.Timeout = DefaultTimeout
	}
	if opts.RequestsPerSecond == 0 {
		opts.RequestsPerSecond = 2
	}
	if opts.Burst == 0 {
		opts.Burst = 1
	}
	if opts.MaxResponseBytes == 0 {
		opts.MaxResponseBytes = 8 << 20
	}
	if opts.Timeout < 0 || opts.RequestsPerSecond < 0 || math.IsNaN(opts.RequestsPerSecond) || math.IsInf(opts.RequestsPerSecond, 0) || opts.Burst < 1 || opts.MaxResponseBytes < 1 || opts.MaxResponseBytes > 64<<20 {
		return nil, errs.Invalid("client", "Timeout, rate, burst, and response size must be positive and finite.")
	}
	policy := RetryPolicy{MaxRetries: 3, InitialBackoff: 200 * time.Millisecond, MaxBackoff: 5 * time.Second}
	if opts.Retry != nil {
		policy = *opts.Retry
		if policy.InitialBackoff == 0 {
			policy.InitialBackoff = 200 * time.Millisecond
		}
		if policy.MaxBackoff == 0 {
			policy.MaxBackoff = 5 * time.Second
		}
	}
	if policy.MaxRetries < 0 || policy.MaxRetries > 10 || policy.InitialBackoff < 0 || policy.MaxBackoff < policy.InitialBackoff || policy.MaxBackoff > time.Minute {
		return nil, errs.Invalid("client", "The retry policy is not valid.")
	}
	hc := &http.Client{}
	if opts.HTTPClient != nil {
		*hc = *opts.HTTPClient
	}
	// Redirects can send an RPC body or a URL key to a different service.
	if hc.CheckRedirect == nil {
		hc.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }
	}
	return &Transport{
		apiBase: api, rpcURL: opts.RPCURL, wsURL: opts.WSURL, timeout: opts.Timeout,
		httpClient: hc, retry: policy, limiter: rate.NewLimiter(rate.Limit(opts.RequestsPerSecond), opts.Burst),
		maxBody: opts.MaxResponseBytes, logger: opts.Logger,
	}, nil
}

func parseEndpoint(raw string, ws bool) (*url.URL, error) {
	u, err := url.Parse(raw)
	if err != nil || u.Hostname() == "" || u.Fragment != "" || u.User != nil {
		return nil, fmt.Errorf("the URL must contain a host and no user data or fragment")
	}
	if (ws && u.Scheme != "ws" && u.Scheme != "wss") || (!ws && u.Scheme != "http" && u.Scheme != "https") {
		return nil, fmt.Errorf("the URL scheme is not supported")
	}
	return u, nil
}

func (c *Transport) APIURL(path string, query url.Values) string {
	u := *c.apiBase
	u.Path = strings.TrimRight(u.Path, "/") + path
	u.RawPath = ""
	q := u.Query()
	for k, values := range query {
		q[k] = values
	}
	u.RawQuery = q.Encode()
	return u.String()
}

func (c *Transport) Do(ctx context.Context, op, method, endpoint string, body []byte, out any) error {
	return c.DoDecode(ctx, op, method, endpoint, body, func(data []byte) error {
		if err := json.Unmarshal(data, out); err != nil {
			return errs.Decode(op, err)
		}
		return nil
	})
}

// DoDecode applies one retry budget to HTTP and response rate limit errors.
// The decoder must return ErrRateLimited for a response that permits a retry.
func (c *Transport) DoDecode(ctx context.Context, op, method, endpoint string, body []byte, decode func([]byte) error) error {
	ctx, cancel := context.WithTimeout(ctx, c.timeout)
	defer cancel()
	for attempt := 0; ; attempt++ {
		if err := c.limiter.Wait(ctx); err != nil {
			if ctx.Err() != nil {
				return errs.Context(op, ctx.Err())
			}
			return errs.Context(op, context.DeadlineExceeded)
		}
		status, retryAfter, err := c.attempt(ctx, op, method, endpoint, body, decode)
		if err == nil {
			return nil
		}
		if ctx.Err() != nil {
			return errs.Context(op, ctx.Err())
		}
		retryable := status < 400 && errors.Is(err, errs.ErrTransport) || RetryStatus(status) || errors.Is(err, errs.ErrRateLimited)
		if !retryable || attempt >= c.retry.MaxRetries || retryAfter > c.retry.MaxBackoff {
			return err
		}
		delay := max(c.Backoff(attempt), retryAfter)
		c.logger.Debug().Str("operation", op).Int("attempt", attempt+1).Dur("delay", delay).Msg("Wait before the next request.")
		if err := WaitContext(ctx, delay); err != nil {
			return errs.Context(op, err)
		}
	}
}

func (c *Transport) attempt(ctx context.Context, op, method, endpoint string, body []byte, decode func([]byte) error) (int, time.Duration, error) {
	req, err := http.NewRequestWithContext(ctx, method, endpoint, bytes.NewReader(body))
	if err != nil {
		return 0, 0, errs.Invalid(op, "The request URL is not valid.")
	}
	req.Header.Set("Accept", "application/json")
	req.Header.Set("User-Agent", "pumpfun-sdk/"+Version)
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	start := time.Now()
	resp, err := c.httpClient.Do(req)
	if err != nil {
		return 0, 0, errs.Transport(op, err)
	}
	defer func() { _ = resp.Body.Close() }()
	c.logger.Debug().Str("operation", op).Int("status", resp.StatusCode).Dur("elapsed", time.Since(start)).Msg("HTTP request complete.")
	retryAfter := ParseRetryAfter(resp.Header.Get("Retry-After"), time.Now())
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		// Read a small part to permit connection reuse. Do not retain the body.
		_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, 4096))
		return resp.StatusCode, retryAfter, StatusError(op, resp.StatusCode, retryAfter)
	}
	data, err := io.ReadAll(io.LimitReader(resp.Body, c.maxBody+1))
	if err != nil {
		return resp.StatusCode, 0, errs.Transport(op, err)
	}
	if int64(len(data)) > c.maxBody {
		return resp.StatusCode, 0, errs.Decode(op, fmt.Errorf("the response exceeds the size limit"))
	}
	if err := decode(data); err != nil {
		var serviceErr *errs.Error
		if errors.Is(err, errs.ErrRateLimited) && errors.As(err, &serviceErr) {
			withResponse := *serviceErr
			withResponse.StatusCode, withResponse.RetryAfter = resp.StatusCode, retryAfter
			err = &withResponse
		}
		return resp.StatusCode, retryAfter, err
	}
	return resp.StatusCode, 0, nil
}

func StatusError(op string, status int, after time.Duration) error {
	kind, message := errs.KindTransport, "The HTTP request failed."
	switch {
	case status == http.StatusNotFound:
		kind, message = errs.KindNotFound, errs.ErrNotFound.Message
	case status == http.StatusUnauthorized || status == http.StatusForbidden:
		kind, message = errs.KindUnauthorized, errs.ErrUnauthorized.Message
	case status == http.StatusTooManyRequests:
		kind, message = errs.KindRateLimited, errs.ErrRateLimited.Message
	case status >= 500 || status == http.StatusRequestTimeout:
		kind, message = errs.KindUnavailable, errs.ErrUnavailable.Message
	case status == http.StatusBadRequest || status == http.StatusUnprocessableEntity:
		kind, message = errs.KindInvalidArgument, "The server did not accept the request."
	}
	return &errs.Error{Kind: kind, Operation: op, Message: message, StatusCode: status, RetryAfter: after}
}

func RetryStatus(status int) bool {
	return status == 408 || status == 429 || status == 500 || status == 502 || status == 503 || status == 504
}

func ParseRetryAfter(value string, now time.Time) time.Duration {
	if n, err := strconv.ParseInt(value, 10, 32); err == nil && n > 0 {
		return time.Duration(n) * time.Second
	}
	if t, err := http.ParseTime(value); err == nil && t.After(now) {
		return t.Sub(now)
	}
	return 0
}

func (c *Transport) Backoff(attempt int) time.Duration {
	delay := c.retry.InitialBackoff
	for range attempt {
		if delay >= c.retry.MaxBackoff/2 {
			delay = c.retry.MaxBackoff
			break
		}
		delay *= 2
	}
	// Equal jitter retains a minimum delay and spreads concurrent retries.
	return delay/2 + time.Duration(rand.Int64N(int64(delay/2)+1))
}

func WaitContext(ctx context.Context, delay time.Duration) error {
	timer := time.NewTimer(delay)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-timer.C:
		return nil
	}
}

// Timeout returns the time limit for one operation.
func (c *Transport) Timeout() time.Duration { return c.timeout }

// RPCURL returns the configured RPC endpoint.
func (c *Transport) RPCURL() string { return c.rpcURL }

// WSURL returns the configured WebSocket endpoint.
func (c *Transport) WSURL() string { return c.wsURL }

// HTTPClient returns the shared HTTP client.
func (c *Transport) HTTPClient() *http.Client { return c.httpClient }

// MaxResponseBytes returns the response size limit.
func (c *Transport) MaxResponseBytes() int64 { return c.maxBody }

// RetryPolicy returns a copy of the retry settings.
func (c *Transport) RetryPolicy() RetryPolicy { return c.retry }

// Logger returns the structured log writer.
func (c *Transport) Logger() *zerolog.Logger { return &c.logger }

// Wait reserves one request from the shared rate limit.
func (c *Transport) Wait(ctx context.Context) error { return c.limiter.Wait(ctx) }
