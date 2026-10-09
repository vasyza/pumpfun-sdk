package pumpfun

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"sync/atomic"
	"testing"
	"time"

	"github.com/coder/websocket"
)

func TestStreamSubscriptionsAndObservation(t *testing.T) {
	client := testClient(t, func(w http.ResponseWriter, r *http.Request) {
		conn, err := websocket.Accept(w, r, nil)
		if err != nil {
			t.Error(err)
			return
		}
		defer conn.CloseNow()
		for _, expected := range []string{"subscribeNewToken", "subscribeMigration", "subscribeTokenTrade"} {
			_, data, err := conn.Read(r.Context())
			if err != nil {
				t.Error(err)
				return
			}
			var sub struct {
				Method string   `json:"method"`
				Keys   []string `json:"keys"`
			}
			if err := json.Unmarshal(data, &sub); err != nil || sub.Method != expected {
				t.Errorf("subscription = %s, err = %v", data, err)
			}
			if expected == "subscribeTokenTrade" && (len(sub.Keys) != 1 || sub.Keys[0] != testMint) {
				t.Error("trade keys do not match")
			}
		}
		for _, data := range []string{
			`{"message":"Successfully subscribed."}`,
			`{"txType":"create","mint":"` + testMint + `","name":"Test"}`,
			`{"txType":"buy","mint":"` + testMint + `","solAmount":0.123456789,"tokenAmount":9007199254740993}`,
		} {
			if err := conn.Write(r.Context(), websocket.MessageText, []byte(data)); err != nil {
				t.Error(err)
				return
			}
		}
		_, _, _ = conn.Read(r.Context())
	}, nil)
	result, err := client.Observe(context.Background(), StreamOptions{NewCoins: true, Migrations: true, Trades: []string{testMint}}, ObserveOptions{Duration: time.Second, MaxEvents: 2})
	if err != nil || len(result.Events) != 2 || !result.LimitReached || result.Events[1].TokenAmount != "9007199254740993" || result.Events[0].SeenAt.IsZero() {
		t.Fatalf("result = %+v, err = %v", result, err)
	}
}

func TestObservationWindowAndParentCancellation(t *testing.T) {
	client := testClient(t, func(w http.ResponseWriter, r *http.Request) {
		conn, err := websocket.Accept(w, r, nil)
		if err != nil {
			t.Error(err)
			return
		}
		defer conn.CloseNow()
		_, _, _ = conn.Read(r.Context())
		_, _, _ = conn.Read(r.Context())
	}, nil)
	result, err := client.Observe(context.Background(), StreamOptions{NewCoins: true}, ObserveOptions{Duration: 30 * time.Millisecond})
	if err != nil || len(result.Events) != 0 || result.LimitReached {
		t.Fatalf("result = %+v, err = %v", result, err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_, err = client.Observe(ctx, StreamOptions{NewCoins: true}, ObserveOptions{})
	if !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
}

func TestOnlyOneActiveStream(t *testing.T) {
	ready := make(chan struct{})
	client := testClient(t, func(w http.ResponseWriter, r *http.Request) {
		conn, err := websocket.Accept(w, r, nil)
		if err != nil {
			t.Error(err)
			return
		}
		defer conn.CloseNow()
		_, _, _ = conn.Read(r.Context())
		close(ready)
		_, _, _ = conn.Read(r.Context())
	}, nil)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := make(chan error, 1)
	go func() { done <- client.StreamNewCoins(ctx, func(context.Context, StreamEvent) error { return nil }) }()
	select {
	case <-ready:
	case <-time.After(time.Second):
		t.Fatal("the stream did not connect")
	}
	err := client.StreamNewCoins(ctx, func(context.Context, StreamEvent) error { return nil })
	if !errors.Is(err, ErrStreamActive) {
		t.Fatal(err)
	}
	cancel()
	if err := <-done; !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
}

func TestStreamReconnectAndHandlerStop(t *testing.T) {
	var calls atomic.Int32
	client := testClient(t, func(w http.ResponseWriter, r *http.Request) {
		conn, err := websocket.Accept(w, r, nil)
		if err != nil {
			t.Error(err)
			return
		}
		defer conn.CloseNow()
		_, _, _ = conn.Read(r.Context())
		if calls.Add(1) == 1 {
			return
		}
		_ = conn.Write(r.Context(), websocket.MessageText, []byte(`{"txType":"create","mint":"`+testMint+`"}`))
		_, _, _ = conn.Read(r.Context())
	}, func(o *Options) { o.Retry.MaxRetries = 2 })
	stop := errors.New("handler stop")
	err := client.StreamNewCoins(context.Background(), func(context.Context, StreamEvent) error { return stop })
	if !errors.Is(err, stop) || calls.Load() != 2 {
		t.Fatalf("calls = %d, err = %v", calls.Load(), err)
	}
}

func TestStreamServiceError(t *testing.T) {
	client := testClient(t, func(w http.ResponseWriter, r *http.Request) {
		conn, err := websocket.Accept(w, r, nil)
		if err != nil {
			t.Error(err)
			return
		}
		defer conn.CloseNow()
		_, _, _ = conn.Read(r.Context())
		_ = conn.Write(r.Context(), websocket.MessageText, []byte(`{"errors":["API key secret"]}`))
		_, _, _ = conn.Read(r.Context())
	}, nil)
	err := client.StreamNewCoins(context.Background(), func(context.Context, StreamEvent) error { return nil })
	if !errors.Is(err, ErrUnauthorized) {
		t.Fatal(err)
	}
}
