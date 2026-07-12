package pintia

import (
	"encoding/json"
	"math"
	"strconv"
	"testing"
)

func TestDecimalCanonicalization(t *testing.T) {
	tests := map[string]string{
		"0":           "0",
		"-0":          "0",
		"100":         "100",
		"100.000":     "100",
		"1e2":         "100",
		"10e1":        "100",
		"0.00100":     "0.001",
		"1000e-5":     "0.01",
		"-12.3400e-1": "-1.234",
	}
	for input, want := range tests {
		t.Run(input, func(t *testing.T) {
			value, err := ParseDecimal(input)
			if err != nil {
				t.Fatal(err)
			}
			if got := value.String(); got != want {
				t.Fatalf("ParseDecimal(%q).String() = %q, want %q", input, got, want)
			}
		})
	}
}

func TestDecimalRejectsInvalidAndExpansionBombs(t *testing.T) {
	for _, input := range []string{"", "+1", "01", ".1", "1.", "NaN", "1e999999999999999999999999", "1e2000000"} {
		t.Run(input, func(t *testing.T) {
			if _, err := ParseDecimal(input); err == nil {
				t.Fatalf("ParseDecimal(%q) error = nil", input)
			}
		})
	}
}

func TestNonNegativeIntegerUsesJSONSchemaNumericSemantics(t *testing.T) {
	for input, want := range map[string]uint64{
		"0":     0,
		"-0":    0,
		"100.0": 100,
		"1e2":   100,
	} {
		t.Run(input, func(t *testing.T) {
			var value NonNegativeInteger
			if err := json.Unmarshal([]byte(input), &value); err != nil {
				t.Fatal(err)
			}
			if got := value.Uint64(); got != want {
				t.Fatalf("Uint64() = %d, want %d", got, want)
			}
		})
	}

	for _, input := range []string{"-1", "1.5", "1e-1", strconv.FormatUint(math.MaxUint64, 10) + "0"} {
		t.Run("invalid/"+input, func(t *testing.T) {
			var value NonNegativeInteger
			if err := json.Unmarshal([]byte(input), &value); err == nil {
				t.Fatalf("json.Unmarshal(%q) error = nil", input)
			}
		})
	}
}

func TestPostgreSQLNumericAndBigintBoundaries(t *testing.T) {
	validDecimal, err := ParseDecimal("1e131071")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := validDecimal.PostgreSQLNumeric(); err != nil {
		t.Fatalf("PostgreSQLNumeric(valid) error = %v", err)
	}
	invalidIntegerDigits, err := ParseDecimal("1e131072")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := invalidIntegerDigits.PostgreSQLNumeric(); err == nil {
		t.Fatal("PostgreSQLNumeric(integer overflow) error = nil")
	}
	invalidFractionalDigits, err := ParseDecimal("1e-16384")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := invalidFractionalDigits.PostgreSQLNumeric(); err == nil {
		t.Fatal("PostgreSQLNumeric(fraction overflow) error = nil")
	}

	if got, err := NewNonNegativeInteger(math.MaxInt64).Int64(); err != nil || got != math.MaxInt64 {
		t.Fatalf("Int64(MaxInt64) = %d, %v", got, err)
	}
	if _, err := NewNonNegativeInteger(math.MaxInt64 + 1).Int64(); err == nil {
		t.Fatal("Int64(MaxInt64+1) error = nil")
	}
}

func TestAnalyticsFloat64UsesExactDecimalBoundaries(t *testing.T) {
	tests := []struct {
		name    string
		input   string
		wantErr bool
		zero    bool
	}{
		{name: "zero", input: "0", zero: true},
		{name: "minimum positive", input: AnalyticsDecimalMinimumPositive},
		{name: "maximum", input: AnalyticsDecimalMaximum},
		{name: "exactly below positive minimum", input: "9.999999999999999999999e-101", wantErr: true},
		{name: "underflow exponent", input: "1e-1000", wantErr: true},
		{name: "exactly over maximum", input: "1.0000000000000000000000000001e100", wantErr: true},
		{name: "overflow exponent", input: "1e1000", wantErr: true},
		{name: "negative", input: "-1", wantErr: true},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			decimal, err := ParseDecimal(test.input)
			if err != nil {
				t.Fatal(err)
			}
			value, err := decimal.AnalyticsFloat64()
			if test.wantErr {
				if err == nil {
					t.Fatalf("AnalyticsFloat64(%q) = %g, nil", test.input, value)
				}
				return
			}
			if err != nil {
				t.Fatalf("AnalyticsFloat64(%q) error = %v", test.input, err)
			}
			if !test.zero && (value == 0 || math.IsInf(value, 0) || math.IsNaN(value)) {
				t.Fatalf("AnalyticsFloat64(%q) = %g", test.input, value)
			}
		})
	}
}
