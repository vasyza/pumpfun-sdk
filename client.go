package pumpfun

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
	"sync/atomic"
	"time"

	"github.com/rs/zerolog"
	"golang.org/x/time/rate"
)

const (
	DefaultAPIBaseURL = "https://frontend-api-v3.pump.fun"
	DefaultRPCURL     = "https://api.mainnet-beta.solana.com"
	DefaultWSURL      = "wss://pumpportal.fun/api/data"
	DefaultTimeout    = 20 * time.Second
	SolanaMainnet     = "solana:5eykt4UsFv8P8NJdTREpY1vzqKqZKvdp"
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

// Client reads data through the configured services. Its options are immutable.
type Client struct {
	apiBase    *url.URL
	rpcURL     string
	wsURL      string
	timeout    time.Duration
	httpClient *http.Client
	retry      RetryPolicy
	limiter    *rate.Limiter
	maxBody    int64
	logger     zerolog.Logger
	rpcID      atomic.Uint64
	streaming  atomic.Bool
}

// NewClient checks the options and creates a read-only client.
func NewClient(opts Options) (*Client, error) {
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
		return nil, invalid("client", "The API URL is not valid.")
	}
	if _, err := parseEndpoint(opts.RPCURL, false); err != nil {
		return nil, invalid("client", "The RPC URL is not valid.")
	}
	if _, err := parseEndpoint(opts.WSURL, true); err != nil {
		return nil, invalid("client", "The WebSocket URL is not valid.")
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
		return nil, invalid("client", "Timeout, rate, burst, and response size must be positive and finite.")
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
		return nil, invalid("client", "The retry policy is not valid.")
	}
	hc := &http.Client{}
	if opts.HTTPClient != nil {
		*hc = *opts.HTTPClient
	}
	// Redirects can send an RPC body or a URL key to a different service.
	if hc.CheckRedirect == nil {
		hc.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }
	}
	return &Client{
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

func (c *Client) apiURL(path string, query url.Values) string {
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

func (c *Client) do(ctx context.Context, op, method, endpoint string, body []byte, out any) error {
	ctx, cancel := context.WithTimeout(ctx, c.timeout)
	defer cancel()
	for attempt := 0; ; attempt++ {
		if err := c.limiter.Wait(ctx); err != nil {
			if ctx.Err() != nil {
				return contextError(op, ctx.Err())
			}
			return contextError(op, context.DeadlineExceeded)
		}
		status, retryAfter, err := c.attempt(ctx, op, method, endpoint, body, out)
		if err == nil {
			return nil
		}
		if ctx.Err() != nil {
			return contextError(op, ctx.Err())
		}
		retryable := status == 0 && errors.Is(err, ErrTransport) || retryStatus(status)
		if !retryable || attempt >= c.retry.MaxRetries || retryAfter > c.retry.MaxBackoff {
			return err
		}
		delay := max(c.backoff(attempt), retryAfter)
		c.logger.Debug().Str("operation", op).Int("attempt", attempt+1).Dur("delay", delay).Msg("Wait before the next request.")
		if err := waitContext(ctx, delay); err != nil {
			return contextError(op, err)
		}
	}
}

func (c *Client) attempt(ctx context.Context, op, method, endpoint string, body []byte, out any) (int, time.Duration, error) {
	req, err := http.NewRequestWithContext(ctx, method, endpoint, bytes.NewReader(body))
	if err != nil {
		return 0, 0, invalid(op, "The request URL is not valid.")
	}
	req.Header.Set("Accept", "application/json")
	req.Header.Set("User-Agent", "pumpfun-sdk/"+Version)
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	start := time.Now()
	resp, err := c.httpClient.Do(req)
	if err != nil {
		return 0, 0, transportError(op, err)
	}
	defer resp.Body.Close()
	c.logger.Debug().Str("operation", op).Int("status", resp.StatusCode).Dur("elapsed", time.Since(start)).Msg("HTTP request complete.")
	retryAfter := parseRetryAfter(resp.Header.Get("Retry-After"), time.Now())
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		// Read a small part to permit connection reuse. Do not retain the body.
		_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, 4096))
		return resp.StatusCode, retryAfter, statusError(op, resp.StatusCode, retryAfter)
	}
	data, err := io.ReadAll(io.LimitReader(resp.Body, c.maxBody+1))
	if err != nil {
		return resp.StatusCode, 0, transportError(op, err)
	}
	if int64(len(data)) > c.maxBody {
		return resp.StatusCode, 0, decodeError(op, fmt.Errorf("the response exceeds the size limit"))
	}
	if err := json.Unmarshal(data, out); err != nil {
		return resp.StatusCode, 0, decodeError(op, err)
	}
	return resp.StatusCode, 0, nil
}

func statusError(op string, status int, after time.Duration) error {
	kind, message := KindTransport, "The HTTP request failed."
	switch {
	case status == http.StatusNotFound:
		kind, message = KindNotFound, ErrNotFound.Message
	case status == http.StatusUnauthorized || status == http.StatusForbidden:
		kind, message = KindUnauthorized, ErrUnauthorized.Message
	case status == http.StatusTooManyRequests:
		kind, message = KindRateLimited, ErrRateLimited.Message
	case status >= 500 || status == http.StatusRequestTimeout:
		kind, message = KindUnavailable, ErrUnavailable.Message
	case status == http.StatusBadRequest || status == http.StatusUnprocessableEntity:
		kind, message = KindInvalidArgument, "The server did not accept the request."
	}
	return &Error{Kind: kind, Operation: op, Message: message, StatusCode: status, RetryAfter: after}
}

func transportError(op string, err error) error {
	var uerr *url.Error
	if errors.As(err, &uerr) {
		err = uerr.Err
	}
	if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
		return contextError(op, err)
	}
	return &Error{Kind: KindTransport, Operation: op, Message: ErrTransport.Message, Cause: err}
}

func contextError(op string, cause error) error {
	return &Error{Kind: KindCanceled, Operation: op, Message: "The operation stopped or its time limit expired.", Cause: cause}
}

func retryStatus(status int) bool {
	return status == 408 || status == 429 || status == 500 || status == 502 || status == 503 || status == 504
}

func parseRetryAfter(value string, now time.Time) time.Duration {
	if n, err := strconv.ParseInt(value, 10, 32); err == nil && n > 0 {
		return time.Duration(n) * time.Second
	}
	if t, err := http.ParseTime(value); err == nil && t.After(now) {
		return t.Sub(now)
	}
	return 0
}

func (c *Client) backoff(attempt int) time.Duration {
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

func waitContext(ctx context.Context, delay time.Duration) error {
	timer := time.NewTimer(delay)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-timer.C:
		return nil
	}
}
