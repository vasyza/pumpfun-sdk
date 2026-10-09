package curve

import (
	"context"
	"encoding/binary"
	"encoding/json"
	"errors"
	"net/http"
	"testing"

	"github.com/mr-tron/base58"
	"github.com/vasyza/pumpfun-sdk/internal/errs"
	"github.com/vasyza/pumpfun-sdk/internal/solana"
	"github.com/vasyza/pumpfun-sdk/internal/testutil"
	"github.com/vasyza/pumpfun-sdk/internal/transport"
)

const testMint = testutil.Mint
const testCreator = testutil.Creator

func testClient(t *testing.T, handler http.HandlerFunc, change func(*transport.Options)) *Service {
	t.Helper()
	return New(solana.New(testutil.Transport(t, handler, change)))
}

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

func TestDeriveAddress(t *testing.T) {
	// This address was checked against the Pump frontend API.
	address, err := DeriveAddress(testMint)
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
		if _, err := DecodeBondingCurve(data); !errors.Is(err, errs.ErrDecode) {
			t.Fatalf("error = %v", err)
		}
	}
	data := curveBytes(1, false)
	data[48] = 2
	if _, err := DecodeBondingCurve(data); !errors.Is(err, errs.ErrDecode) {
		t.Fatalf("boolean error = %v", err)
	}
}

func TestGetCurveAndAccountChecks(t *testing.T) {
	for _, owner := range []string{solana.PumpProgramID, solana.TokenProgram, "missing"} {
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
					testutil.RPCReply(t, w, request.ID, nil)
				} else {
					testutil.RPCReply(t, w, request.ID, testutil.EncodedAccount(curveBytes(1234, false), owner))
				}
			}, nil)
			curve, err := client.GetBondingCurve(context.Background(), testMint)
			switch owner {
			case solana.PumpProgramID:
				if err != nil || curve.Slot != 123 || curve.RealTokenReserves != "1234" {
					t.Fatalf("curve = %+v, err = %v", curve, err)
				}
			default:
				var typed *errs.Error
				if !errors.Is(err, errs.ErrNotFound) || !errors.As(err, &typed) || typed.Message != "No bonding curve exists for this mint." {
					t.Fatal(err)
				}
			}
		})
	}
}

func TestMissingCurveAndInvalidCurveData(t *testing.T) {
	for _, tc := range []struct {
		name    string
		account any
		want    error
	}{
		{name: "absent", account: nil, want: errs.ErrNotFound},
		{name: "other owner", account: testutil.EncodedAccount(curveBytes(0, false), solana.TokenProgram), want: errs.ErrNotFound},
		{name: "empty state", account: testutil.EncodedAccount([]byte{}, solana.PumpProgramID), want: errs.ErrNotFound},
		{name: "other state", account: testutil.EncodedAccount(make([]byte, 166), solana.PumpProgramID), want: errs.ErrNotFound},
		{name: "invalid encoding", account: map[string]any{"owner": solana.PumpProgramID, "data": []string{"!", "base64"}}, want: errs.ErrDecode},
		{name: "short curve", account: testutil.EncodedAccount(curveDiscriminator[:], solana.PumpProgramID), want: errs.ErrDecode},
	} {
		for _, progress := range []bool{false, true} {
			t.Run(tc.name+map[bool]string{false: "/curve", true: "/progress"}[progress], func(t *testing.T) {
				client := testClient(t, func(w http.ResponseWriter, r *http.Request) {
					var request struct {
						ID uint64 `json:"id"`
					}
					if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
						t.Error(err)
						return
					}
					if progress {
						global := make([]byte, 113)
						copy(global, globalDiscriminator[:])
						testutil.RPCReply(t, w, request.ID, []any{tc.account, testutil.EncodedAccount(global, solana.PumpProgramID)})
					} else {
						testutil.RPCReply(t, w, request.ID, tc.account)
					}
				}, nil)
				var err error
				if progress {
					_, err = client.GetGraduationProgress(t.Context(), testMint)
				} else {
					_, err = client.GetBondingCurve(t.Context(), testMint)
				}
				var typed *errs.Error
				if !errors.Is(err, tc.want) || !errors.As(err, &typed) || tc.want == errs.ErrNotFound && typed.Message != "No bonding curve exists for this mint." {
					t.Fatalf("error = %v, want kind = %v", err, tc.want)
				}
			})
		}
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
				testutil.RPCReply(t, w, request.ID, []any{testutil.EncodedAccount(curve, solana.PumpProgramID), testutil.EncodedAccount(global, solana.PumpProgramID)})
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
