package models

import (
	"encoding/json"
	"errors"
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
