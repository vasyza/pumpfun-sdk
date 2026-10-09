package stream

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"slices"
	"sync"
	"sync/atomic"
	"time"

	"github.com/coder/websocket"
	"github.com/vasyza/pumpfun-sdk/internal/errs"
	"github.com/vasyza/pumpfun-sdk/internal/models"
	"github.com/vasyza/pumpfun-sdk/internal/solana"
	"github.com/vasyza/pumpfun-sdk/internal/transport"
)

// Service owns one active WebSocket stream.
type Service struct {
	transport *transport.Transport
	streaming atomic.Bool
}

// New creates a stream service with the supplied transport.
func New(t *transport.Transport) *Service { return &Service{transport: t} }

type DecimalAmount = models.DecimalAmount

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
func (c *Service) Stream(ctx context.Context, opts StreamOptions, handler EventHandler) error {
	if handler == nil || !opts.NewCoins && !opts.Migrations && len(opts.Trades) == 0 || len(opts.Trades) > 100 {
		return errs.Invalid("stream", "Select a stream type, at most 100 trade mints, and a handler.")
	}
	opts.Trades = slices.Clone(opts.Trades)
	for _, mint := range opts.Trades {
		if err := solana.ValidateAddress(mint); err != nil {
			return err
		}
	}
	if !c.streaming.CompareAndSwap(false, true) {
		return &errs.Error{Kind: errs.KindStreamActive, Operation: "stream", Message: errs.ErrStreamActive.Message}
	}
	defer c.streaming.Store(false)
	for attempt := 0; ; attempt++ {
		if err := c.transport.Wait(ctx); err != nil {
			if ctx.Err() != nil {
				return errs.Context("stream", ctx.Err())
			}
			return errs.Context("stream", context.DeadlineExceeded)
		}
		delivered, retryable, err := c.streamConnection(ctx, opts, handler)
		if ctx.Err() != nil {
			return errs.Context("stream", ctx.Err())
		}
		if !retryable {
			return err
		}
		var serviceErr *errs.Error
		var retryAfter time.Duration
		if errors.As(err, &serviceErr) {
			retryAfter = serviceErr.RetryAfter
		}
		if retryAfter > c.transport.RetryPolicy().MaxBackoff {
			return err
		}
		if delivered {
			attempt = 0
		}
		if attempt >= c.transport.RetryPolicy().MaxRetries {
			return err
		}
		c.transport.Logger().Debug().Int("attempt", attempt+1).Msg("Connect the event stream again.")
		if err := transport.WaitContext(ctx, max(c.transport.Backoff(attempt), retryAfter)); err != nil {
			return errs.Context("stream", err)
		}
	}
}

func (c *Service) streamConnection(ctx context.Context, opts StreamOptions, handler EventHandler) (bool, bool, error) {
	dialCtx, cancelDial := context.WithTimeout(ctx, c.transport.Timeout())
	conn, response, err := websocket.Dial(dialCtx, c.transport.WSURL(), &websocket.DialOptions{HTTPClient: c.transport.HTTPClient(), HTTPHeader: http.Header{"User-Agent": {"pumpfun-sdk/" + transport.Version}}})
	// Dial owns the network body. Close any retained error body.
	if response != nil && response.Body != nil {
		defer func() { _ = response.Body.Close() }()
	}
	cancelDial()
	if err != nil {
		if response != nil && response.StatusCode != http.StatusSwitchingProtocols {
			status := response.StatusCode
			return false, transport.RetryStatus(status), transport.StatusError("stream", status, transport.ParseRetryAfter(response.Header.Get("Retry-After"), time.Now()))
		}
		return false, true, errs.Transport("stream", err)
	}
	conn.SetReadLimit(c.transport.MaxResponseBytes())
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
		writeCtx, writeCancel := context.WithTimeout(streamCtx, c.transport.Timeout())
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
				return false, true, errs.Transport("stream", err)
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
				pingCtx, pingCancel := context.WithTimeout(streamCtx, c.transport.Timeout())
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
			return delivered, true, errs.Transport("stream", err)
		}
		if kind != websocket.MessageText {
			return delivered, false, errs.Decode("stream", errors.New("the event must be a text frame"))
		}
		var envelope struct {
			StreamEvent
			Message string          `json:"message"`
			Errors  json.RawMessage `json:"errors"`
			Error   json.RawMessage `json:"error"`
		}
		if err := json.Unmarshal(data, &envelope); err != nil {
			return delivered, false, errs.Decode("stream", err)
		}
		if hasServiceError(envelope.Errors) || hasServiceError(envelope.Error) {
			return delivered, false, &errs.Error{Kind: errs.KindUnauthorized, Operation: "stream", Message: "The stream service did not accept the subscription. Check its API key and account balance."}
		}
		if envelope.Type == "" && envelope.Message != "" {
			continue
		}
		event := envelope.StreamEvent
		if err := solana.ValidateAddress(event.Mint); err != nil || event.Type == "" {
			return delivered, false, errs.Decode("stream", errors.New("the event mint or type is not valid"))
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

func hasServiceError(data json.RawMessage) bool {
	if len(data) == 0 {
		return false
	}
	var value any
	if err := json.Unmarshal(data, &value); err != nil {
		return true
	}
	switch value := value.(type) {
	case nil:
		return false
	case []any:
		return len(value) > 0
	case map[string]any:
		return len(value) > 0
	case string:
		return value != ""
	case bool:
		return value
	default:
		return true
	}
}

// StreamNewCoins reads token creation events.
func (c *Service) StreamNewCoins(ctx context.Context, handler EventHandler) error {
	return c.Stream(ctx, StreamOptions{NewCoins: true}, handler)
}

// StreamTrades reads trades for the selected mints. PumpPortal meters this data.
func (c *Service) StreamTrades(ctx context.Context, mints []string, handler EventHandler) error {
	return c.Stream(ctx, StreamOptions{Trades: mints}, handler)
}

// StreamMigrations reads migration events.
func (c *Service) StreamMigrations(ctx context.Context, handler EventHandler) error {
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
func (c *Service) Observe(ctx context.Context, streams StreamOptions, opts ObserveOptions) (*Observation, error) {
	if opts.Duration == 0 {
		opts.Duration = 10 * time.Second
	}
	if opts.MaxEvents == 0 {
		opts.MaxEvents = 20
	}
	if opts.Duration < 0 || opts.Duration > time.Minute || opts.MaxEvents < 1 || opts.MaxEvents > 100 {
		return nil, errs.Invalid("observe", "Use a duration from 0 to 60 seconds and an event limit from 1 to 100.")
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
		return nil, errs.Context("observe", ctx.Err())
	}
	if err == nil || errors.Is(err, errObservationComplete) {
		return result, nil
	}
	if errors.Is(err, context.DeadlineExceeded) && window.Err() != nil {
		return result, nil
	}
	return nil, err
}
