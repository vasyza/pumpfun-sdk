//go:build live

package client_test

import (
	"context"
	"os"
	"testing"

	"github.com/vasyza/pumpfun-sdk/internal/client"
	"github.com/vasyza/pumpfun-sdk/internal/models"
)

// TestLiveRead requires both the live tag and a mint from the environment.
func TestLiveRead(t *testing.T) {
	mint := os.Getenv("PUMPFUN_LIVE_MINT")
	if mint == "" {
		t.Skip("Set PUMPFUN_LIVE_MINT to run the live read.")
	}
	client, err := client.NewClient(client.Options{})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := client.GetCoin(context.Background(), mint); err != nil {
		t.Fatal(err)
	}
	if _, err := client.GetTrades(context.Background(), mint, models.TradeOptions{Limit: 1}); err != nil {
		t.Fatal(err)
	}
}
