package pumpfun

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"slices"
	"sync"
	"time"

	"github.com/coder/websocket"
)

// DecimalAmount retains the decimal text that a stream service sends.
// These values use the service's display units. They are not raw token amounts.
type DecimalAmount string

// UnmarshalJSON accepts a decimal JSON number or its string form.
func (a *DecimalAmount) UnmarshalJSON(data []byte) error {
	if string(data) == "null" {
		*a = ""
		return nil
	}
	s := string(data)
	if len(data) > 0 && data[0] == '"' {
		if err := json.Unmarshal(data, &s); err != nil {
			return err
		}
	}
	var n json.Number
	if err := json.Unmarshal([]byte(s), &n); err != nil || n == "" {
		return errors.New("the decimal amount is not valid")
	}
	*a = DecimalAmount(s)
	return nil
}

// StreamEvent contains PumpPortal event fields. SeenAt is the local receive time.
// A create event can omit trade fields. A trade event can omit coin name fields.
type StreamEvent struct {
	Type            string        `json:"txType" yaml:"type"`
	Mint            string        `json:"mint" yaml:"mint"`
	Signature       string        `json:"signature,omitempty" yaml:"signature,omitempty"`
	TraderPublicKey string        `json:"traderPublicKey,omitempty" yaml:"trader_public_key,omitempty"`
	Name            string        `json:"name,omitempty" yaml:"name,omitempty"`
	Symbol          string        `json:"symbol,omitempty" yaml:"symbol,omitempty"`
	URI             string        `json:"uri,omitempty" yaml:"uri,omitempty"`
	BondingCurveKey string        `json:"bondingCurveKey,omitempty" yaml:"bonding_curve_key,omitempty"`
	TokenAmount     DecimalAmount `json:"tokenAmount,omitempty" yaml:"token_amount,omitempty"`
	SOLAmount       DecimalAmount `json:"solAmount,omitempty" yaml:"sol_amount,omitempty"`
	MarketCapSOL    DecimalAmount `json:"marketCapSol,omitempty" yaml:"market_cap_sol,omitempty"`
	Pool            string        `json:"pool,omitempty" yaml:"pool,omitempty"`
	SeenAt          time.Time     `json:"seen_at" yaml:"seen_at"`
}

// StreamOptions selects subscriptions on one WebSocket connection.
type StreamOptions struct {
	NewCoins   bool
	Trades     []string
	Migrations bool
}

// EventHandler processes events in receive order. Return an error to stop.
// Keep the handler fast. The client does not queue or replay events.
type EventHandler func(context.Context, StreamEvent) error

// Stream reads selected events until the context stops or the handler fails.
// It uses one connection per client. Reconnects have a bounded retry budget.
// Events can be lost during reconnects. PumpPortal meters trade subscriptions.
func (c *Client) Stream(ctx context.Context, opts StreamOptions, handler EventHandler) error {
	if handler == nil || !opts.NewCoins && !opts.Migrations && len(opts.Trades) == 0 || len(opts.Trades) > 100 {
		return invalid("stream", "Select a stream type, at most 100 trade mints, and a handler.")
	}
	opts.Trades = slices.Clone(opts.Trades)
	for _, mint := range opts.Trades {
		if err := ValidateAddress(mint); err != nil {
			return err
		}
	}
	if !c.streaming.CompareAndSwap(false, true) {
		return &Error{Kind: KindStreamActive, Operation: "stream", Message: ErrStreamActive.Message}
	}
	defer c.streaming.Store(false)
	for attempt := 0; ; attempt++ {
		if err := c.limiter.Wait(ctx); err != nil {
			if ctx.Err() != nil {
				return contextError("stream", ctx.Err())
			}
			return contextError("stream", context.DeadlineExceeded)
		}
		delivered, retryable, err := c.streamConnection(ctx, opts, handler)
		if ctx.Err() != nil {
			return contextError("stream", ctx.Err())
		}
		if !retryable {
			return err
		}
		if delivered {
			attempt = 0
		}
		if attempt >= c.retry.MaxRetries {
			return err
		}
		c.logger.Debug().Int("attempt", attempt+1).Msg("Connect the event stream again.")
		if err := waitContext(ctx, c.backoff(attempt)); err != nil {
			return contextError("stream", err)
		}
	}
}

func (c *Client) streamConnection(ctx context.Context, opts StreamOptions, handler EventHandler) (bool, bool, error) {
	dialCtx, cancelDial := context.WithTimeout(ctx, c.timeout)
	conn, response, err := websocket.Dial(dialCtx, c.wsURL, &websocket.DialOptions{HTTPClient: c.httpClient, HTTPHeader: http.Header{"User-Agent": {"pumpfun-sdk/" + Version}}})
	cancelDial()
	if err != nil {
		if response != nil && response.StatusCode != http.StatusSwitchingProtocols {
			status := response.StatusCode
			return false, retryStatus(status), statusError("stream", status, parseRetryAfter(response.Header.Get("Retry-After"), time.Now()))
		}
		return false, true, transportError("stream", err)
	}
	conn.SetReadLimit(c.maxBody)
	streamCtx, cancel := context.WithCancel(ctx)
	var heartbeat sync.WaitGroup
	defer func() {
		cancel()
		_ = conn.CloseNow()
		heartbeat.Wait()
	}()
	write := func(method string, keys []string) error {
		payload, err := json.Marshal(struct {
			Method string   `json:"method"`
			Keys   []string `json:"keys,omitempty"`
		}{method, keys})
		if err != nil {
			return err
		}
		writeCtx, writeCancel := context.WithTimeout(streamCtx, c.timeout)
		defer writeCancel()
		return conn.Write(writeCtx, websocket.MessageText, payload)
	}
	for _, sub := range []struct {
		enabled bool
		method  string
		keys    []string
	}{
		{opts.NewCoins, "subscribeNewToken", nil},
		{opts.Migrations, "subscribeMigration", nil},
		{len(opts.Trades) > 0, "subscribeTokenTrade", opts.Trades},
	} {
		if sub.enabled {
			if err := write(sub.method, sub.keys); err != nil {
				return false, true, transportError("stream", err)
			}
		}
	}
	// Read processes control frames while Ping waits for a response.
	heartbeat.Add(1)
	go func() {
		defer heartbeat.Done()
		ticker := time.NewTicker(30 * time.Second)
		defer ticker.Stop()
		for {
			select {
			case <-streamCtx.Done():
				return
			case <-ticker.C:
				pingCtx, pingCancel := context.WithTimeout(streamCtx, c.timeout)
				err := conn.Ping(pingCtx)
				pingCancel()
				if err != nil {
					_ = conn.CloseNow()
					return
				}
			}
		}
	}()
	var delivered bool
	for {
		kind, data, err := conn.Read(streamCtx)
		if err != nil {
			return delivered, true, transportError("stream", err)
		}
		if kind != websocket.MessageText {
			return delivered, false, decodeError("stream", errors.New("the event must be a text frame"))
		}
		var envelope struct {
			StreamEvent
			Message string          `json:"message"`
			Errors  json.RawMessage `json:"errors"`
			Error   json.RawMessage `json:"error"`
		}
		if err := json.Unmarshal(data, &envelope); err != nil {
			return delivered, false, decodeError("stream", err)
		}
		if len(envelope.Errors) > 0 || len(envelope.Error) > 0 {
			return delivered, false, &Error{Kind: KindUnauthorized, Operation: "stream", Message: "The stream service did not accept the subscription. Check its API key and account balance."}
		}
		if envelope.Type == "" && envelope.Message != "" {
			continue
		}
		event := envelope.StreamEvent
		if err := ValidateAddress(event.Mint); err != nil || event.Type == "" {
			return delivered, false, decodeError("stream", errors.New("the event mint or type is not valid"))
		}
		selected := opts.NewCoins && event.Type == "create" ||
			opts.Migrations && (event.Type == "migrate" || event.Type == "migration") ||
			(event.Type == "buy" || event.Type == "sell") && slices.Contains(opts.Trades, event.Mint)
		if !selected {
			continue
		}
		event.SeenAt = time.Now().UTC()
		if err := handler(streamCtx, event); err != nil {
			return delivered, false, err
		}
		delivered = true
	}
}

// StreamNewCoins reads token creation events.
func (c *Client) StreamNewCoins(ctx context.Context, handler EventHandler) error {
	return c.Stream(ctx, StreamOptions{NewCoins: true}, handler)
}

// StreamTrades reads trades for the selected mints. PumpPortal meters this data.
func (c *Client) StreamTrades(ctx context.Context, mints []string, handler EventHandler) error {
	return c.Stream(ctx, StreamOptions{Trades: mints}, handler)
}

// StreamMigrations reads migration events.
func (c *Client) StreamMigrations(ctx context.Context, handler EventHandler) error {
	return c.Stream(ctx, StreamOptions{Migrations: true}, handler)
}

// ObserveOptions limits an event read. Zero values select 10 seconds and 20 events.
type ObserveOptions struct {
	Duration  time.Duration
	MaxEvents int
}

// Observation contains a bounded event sample. It is not a replay of past events.
type Observation struct {
	Events       []StreamEvent `json:"events" yaml:"events"`
	StartedAt    time.Time     `json:"started_at" yaml:"started_at"`
	EndedAt      time.Time     `json:"ended_at" yaml:"ended_at"`
	LimitReached bool          `json:"limit_reached" yaml:"limit_reached"`
}

var errObservationComplete = errors.New("the event limit was reached")

// Observe reads events for at most one minute and returns at most 100 events.
// A time limit returns the events received so far. Parent cancellation is an error.
func (c *Client) Observe(ctx context.Context, streams StreamOptions, opts ObserveOptions) (*Observation, error) {
	if opts.Duration == 0 {
		opts.Duration = 10 * time.Second
	}
	if opts.MaxEvents == 0 {
		opts.MaxEvents = 20
	}
	if opts.Duration < 0 || opts.Duration > time.Minute || opts.MaxEvents < 1 || opts.MaxEvents > 100 {
		return nil, invalid("observe", "Use a duration from 0 to 60 seconds and an event limit from 1 to 100.")
	}
	window, cancel := context.WithTimeout(ctx, opts.Duration)
	defer cancel()
	result := &Observation{Events: []StreamEvent{}, StartedAt: time.Now().UTC()}
	err := c.Stream(window, streams, func(_ context.Context, event StreamEvent) error {
		result.Events = append(result.Events, event)
		if len(result.Events) >= opts.MaxEvents {
			result.LimitReached = true
			return errObservationComplete
		}
		return nil
	})
	result.EndedAt = time.Now().UTC()
	if ctx.Err() != nil {
		return nil, contextError("observe", ctx.Err())
	}
	if err != nil && !errors.Is(err, errObservationComplete) && !(errors.Is(err, context.DeadlineExceeded) && window.Err() != nil) {
		return nil, err
	}
	return result, nil
}
