package solana

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"testing"

	"github.com/mr-tron/base58"
	"github.com/vasyza/pumpfun-sdk/internal/errs"
	"github.com/vasyza/pumpfun-sdk/internal/testutil"
	"github.com/vasyza/pumpfun-sdk/internal/transport"
)

const testMint = testutil.Mint
const testCreator = testutil.Creator

type Options = transport.Options

func testClient(t *testing.T, handler http.HandlerFunc, change func(*Options)) *Client {
	t.Helper()
	return New(testutil.Transport(t, handler, change))
}

func TestHoldersResolveToken2022Owner(t *testing.T) {
	client := testClient(t, func(w http.ResponseWriter, r *http.Request) {
		var request struct {
			Method string `json:"method"`
			ID     uint64 `json:"id"`
		}
		if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
			t.Error(err)
			return
		}
		switch request.Method {
		case "getTokenLargestAccounts":
			testutil.RPCReply(t, w, request.ID, []any{map[string]any{"address": testCreator, "amount": "9007199254740993", "decimals": 6}})
		case "getTokenSupply":
			testutil.RPCReply(t, w, request.ID, map[string]any{"amount": "18014398509481986"})
		case "getMultipleAccounts":
			data := make([]byte, 165)
			mint, _ := base58.Decode(testMint)
			owner, _ := base58.Decode(testCreator)
			copy(data, mint)
			copy(data[32:64], owner)
			testutil.RPCReply(t, w, request.ID, []any{testutil.EncodedAccount(data, Token2022Program)})
		default:
			t.Error(request.Method)
		}
	}, nil)
	result, err := client.GetHolders(context.Background(), testMint)
	if err != nil || len(result.Accounts) != 1 || result.Accounts[0].Owner != testCreator || result.Accounts[0].SharePercent != 50 {
		t.Fatalf("result = %+v, err = %v", result, err)
	}
}

func TestRPCErrorAndWrongID(t *testing.T) {
	for _, badID := range []bool{false, true} {
		client := testClient(t, func(w http.ResponseWriter, r *http.Request) {
			var request struct {
				ID uint64 `json:"id"`
			}
			if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
				t.Error(err)
				return
			}
			id := request.ID
			if badID {
				id++
			}
			testutil.WriteJSON(t, w, map[string]any{"jsonrpc": "2.0", "id": id, "error": map[string]any{"code": -32005, "message": "secret"}})
		}, nil)
		_, err := client.GetHolders(context.Background(), testMint)
		if badID && !errors.Is(err, errs.ErrDecode) || !badID && !errors.Is(err, errs.ErrRPC) {
			t.Fatalf("wrong ID = %v, err = %v", badID, err)
		}
	}
}
