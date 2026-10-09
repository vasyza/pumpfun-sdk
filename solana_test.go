package pumpfun

import (
	"context"
	"encoding/base64"
	"encoding/binary"
	"encoding/json"
	"errors"
	"net/http"
	"testing"

	"github.com/mr-tron/base58"
)

func curveBytes(remaining uint64, complete bool) []byte {
	data := make([]byte, 166)
	copy(data, curveDiscriminator[:])
	for i, n := range []uint64{1_073_000_000_000_000, 30_000_000_000, remaining, 10_000_000_000, 1_000_000_000_000_000} {
		binary.LittleEndian.PutUint64(data[8+i*8:16+i*8], n)
	}
	if complete {
		data[48] = 1
	}
	creator, _ := base58.Decode(testCreator)
	copy(data[49:81], creator)
	return data
}

func encodedAccount(data []byte, owner string) map[string]any {
	return map[string]any{"owner": owner, "executable": false, "data": []string{base64.StdEncoding.EncodeToString(data), "base64"}}
}

func rpcReply(t *testing.T, w http.ResponseWriter, id uint64, value any) {
	t.Helper()
	writeJSON(t, w, map[string]any{"jsonrpc": "2.0", "id": id, "result": map[string]any{"context": map[string]any{"slot": 123}, "value": value}})
}

func TestDeriveBondingCurveAddress(t *testing.T) {
	// This address was checked against the Pump frontend API.
	address, err := DeriveBondingCurveAddress(testMint)
	if err != nil || address != "7XMeiu8AZvgTzEdA5vq3Q8dMCLMpTTgLdB3n5FAv1CT1" {
		t.Fatalf("address = %s, err = %v", address, err)
	}
}

func TestDecodeCurveLayouts(t *testing.T) {
	for _, size := range []int{49, 81, 82, 83, 150, 166, 200} {
		data := make([]byte, size)
		copy(data, curveBytes(793_100_000_000_000, false))
		curve, err := DecodeBondingCurve(data)
		if err != nil || curve.RealTokenReserves != "793100000000000" || curve.Complete {
			t.Fatalf("size = %d, curve = %+v, err = %v", size, curve, err)
		}
		if size >= 81 && curve.Creator != testCreator {
			t.Fatalf("creator = %s", curve.Creator)
		}
	}
	for _, data := range [][]byte{make([]byte, 49), curveBytes(0, true)[:48]} {
		if _, err := DecodeBondingCurve(data); !errors.Is(err, ErrDecode) {
			t.Fatalf("error = %v", err)
		}
	}
	data := curveBytes(1, false)
	data[48] = 2
	if _, err := DecodeBondingCurve(data); !errors.Is(err, ErrDecode) {
		t.Fatalf("boolean error = %v", err)
	}
}

func TestGetCurveAndAccountChecks(t *testing.T) {
	for _, owner := range []string{PumpProgramID, TokenProgram, "missing"} {
		t.Run(owner, func(t *testing.T) {
			client := testClient(t, func(w http.ResponseWriter, r *http.Request) {
				var request struct {
					Method string            `json:"method"`
					ID     uint64            `json:"id"`
					Params []json.RawMessage `json:"params"`
				}
				if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
					t.Error(err)
					return
				}
				if request.Method != "getAccountInfo" || string(request.Params[0]) != `"7XMeiu8AZvgTzEdA5vq3Q8dMCLMpTTgLdB3n5FAv1CT1"` {
					t.Errorf("request = %+v", request)
				}
				if owner == "missing" {
					rpcReply(t, w, request.ID, nil)
				} else {
					rpcReply(t, w, request.ID, encodedAccount(curveBytes(1234, false), owner))
				}
			}, nil)
			curve, err := client.GetBondingCurve(context.Background(), testMint)
			switch owner {
			case PumpProgramID:
				if err != nil || curve.Slot != 123 || curve.RealTokenReserves != "1234" {
					t.Fatalf("curve = %+v, err = %v", curve, err)
				}
			case "missing":
				if !errors.Is(err, ErrNotFound) {
					t.Fatal(err)
				}
			default:
				if !errors.Is(err, ErrDecode) {
					t.Fatal(err)
				}
			}
		})
	}
}

func TestGraduationProgress(t *testing.T) {
	for _, tc := range []struct {
		name      string
		remaining uint64
		initial   uint64
		complete  bool
		mayhem    bool
		percent   *float64
	}{
		{"half", 50, 100, false, false, ptrFloat(50)},
		{"complete", 0, 100, true, false, ptrFloat(100)},
		{"zero baseline", 0, 0, false, false, nil},
		{"changed baseline", 200, 100, false, false, nil},
		{"mayhem", 50, 100, false, true, nil},
	} {
		t.Run(tc.name, func(t *testing.T) {
			client := testClient(t, func(w http.ResponseWriter, r *http.Request) {
				var request struct {
					Method string `json:"method"`
					ID     uint64 `json:"id"`
				}
				if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
					t.Error(err)
					return
				}
				if request.Method != "getMultipleAccounts" {
					t.Error(request.Method)
				}
				curve := curveBytes(tc.remaining, tc.complete)
				if tc.mayhem {
					curve[81] = 1
				}
				global := make([]byte, 113)
				copy(global, globalDiscriminator[:])
				binary.LittleEndian.PutUint64(global[89:97], tc.initial)
				rpcReply(t, w, request.ID, []any{encodedAccount(curve, PumpProgramID), encodedAccount(global, PumpProgramID)})
			}, nil)
			result, err := client.GetGraduationProgress(context.Background(), testMint)
			if err != nil {
				t.Fatal(err)
			}
			if tc.percent == nil {
				if result.Percent != nil || result.Reason == "" {
					t.Fatalf("result = %+v", result)
				}
			} else if result.Percent == nil || *result.Percent != *tc.percent {
				t.Fatalf("result = %+v", result)
			}
		})
	}
}

func ptrFloat(v float64) *float64 { return &v }

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
			rpcReply(t, w, request.ID, []any{map[string]any{"address": testCreator, "amount": "9007199254740993", "decimals": 6}})
		case "getTokenSupply":
			rpcReply(t, w, request.ID, map[string]any{"amount": "18014398509481986"})
		case "getMultipleAccounts":
			data := make([]byte, 165)
			mint, _ := base58.Decode(testMint)
			owner, _ := base58.Decode(testCreator)
			copy(data, mint)
			copy(data[32:64], owner)
			rpcReply(t, w, request.ID, []any{encodedAccount(data, Token2022Program)})
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
			writeJSON(t, w, map[string]any{"jsonrpc": "2.0", "id": id, "error": map[string]any{"code": -32005, "message": "secret"}})
		}, nil)
		_, err := client.GetBondingCurve(context.Background(), testMint)
		if badID && !errors.Is(err, ErrDecode) || !badID && !errors.Is(err, ErrRPC) {
			t.Fatalf("wrong ID = %v, err = %v", badID, err)
		}
	}
}
