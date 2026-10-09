package models

import (
	"encoding/json"
	"testing"
)

func TestRawAmountRetainsPrecision(t *testing.T) {
	for _, input := range []string{`9007199254740993`, `"9007199254740993"`} {
		var amount RawAmount
		if err := json.Unmarshal([]byte(input), &amount); err != nil {
			t.Fatal(err)
		}
		data, err := json.Marshal(amount)
		if err != nil || string(data) != `"9007199254740993"` {
			t.Fatalf("amount = %s, err = %v", data, err)
		}
	}
	for _, input := range []string{`-1`, `1.5`, `"no"`} {
		var amount RawAmount
		if err := json.Unmarshal([]byte(input), &amount); err == nil {
			t.Errorf("accepted %s", input)
		}
	}
}
