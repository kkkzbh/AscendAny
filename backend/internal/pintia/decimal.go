package pintia

import (
	"encoding/json"
	"fmt"
	"math"
	"regexp"
	"strconv"
	"strings"
)

const maxCanonicalDecimalBytes = 1 << 20

const (
	maxPostgresNumericIntegerDigits    = 131_072
	maxPostgresNumericFractionalDigits = 16_383

	// AnalyticsDecimalMaximum and AnalyticsDecimalMinimumPositive define the
	// exact, non-zero score interval accepted by the import contract. The
	// symmetric exponents keep division, squaring, and bounded aggregation far
	// inside finite float64 arithmetic.
	AnalyticsDecimalMaximum         = "1e100"
	AnalyticsDecimalMinimumPositive = "1e-100"
)

var jsonNumberPattern = regexp.MustCompile(`^-?(?:0|[1-9][0-9]*)(?:\.[0-9]+)?(?:[eE][+-]?[0-9]+)?$`)

var (
	analyticsDecimalMaximumCanonical         = "1" + strings.Repeat("0", 100)
	analyticsDecimalMinimumPositiveCanonical = "0." + strings.Repeat("0", 99) + "1"
)

// Decimal retains a canonical, exact base-10 representation. It avoids float
// rounding in validation and domain hashing.
type Decimal struct {
	canonical string
}

func ParseDecimal(value string) (Decimal, error) {
	return parseDecimalBytes([]byte(value))
}

func (d Decimal) String() string {
	return d.canonical
}

// AnalyticsFloat64 converts a validated score to the representation consumed
// by the analytics engine. Bounds are compared against the exact canonical
// decimal before conversion, so a value which rounds onto a boundary cannot
// cross it silently. Zero is valid; every non-zero value must remain non-zero.
func (d Decimal) AnalyticsFloat64() (float64, error) {
	if d.canonical == "" {
		return 0, fmt.Errorf("decimal is uninitialized")
	}
	if strings.HasPrefix(d.canonical, "-") {
		return 0, fmt.Errorf("analytics decimal must be non-negative")
	}
	if d.canonical == "0" {
		return 0, nil
	}
	if compareNonNegativeDecimalText(d.canonical, analyticsDecimalMinimumPositiveCanonical) < 0 {
		return 0, fmt.Errorf(
			"non-zero analytics decimal is below the exact minimum %s",
			AnalyticsDecimalMinimumPositive,
		)
	}
	if compareNonNegativeDecimalText(d.canonical, analyticsDecimalMaximumCanonical) > 0 {
		return 0, fmt.Errorf(
			"analytics decimal exceeds the exact maximum %s",
			AnalyticsDecimalMaximum,
		)
	}
	converted, err := strconv.ParseFloat(d.canonical, 64)
	if err != nil || math.IsNaN(converted) || math.IsInf(converted, 0) || converted == 0 {
		if err != nil {
			return 0, fmt.Errorf("convert analytics decimal to finite float64: %w", err)
		}
		return 0, fmt.Errorf("analytics decimal does not have a finite non-zero float64 representation")
	}
	return converted, nil
}

func compareNonNegativeDecimalText(left, right string) int {
	leftInteger, leftFraction, _ := strings.Cut(left, ".")
	rightInteger, rightFraction, _ := strings.Cut(right, ".")
	if len(leftInteger) < len(rightInteger) {
		return -1
	}
	if len(leftInteger) > len(rightInteger) {
		return 1
	}
	if leftInteger < rightInteger {
		return -1
	}
	if leftInteger > rightInteger {
		return 1
	}
	fractionLength := max(len(leftFraction), len(rightFraction))
	for index := 0; index < fractionLength; index++ {
		leftDigit := byte('0')
		if index < len(leftFraction) {
			leftDigit = leftFraction[index]
		}
		rightDigit := byte('0')
		if index < len(rightFraction) {
			rightDigit = rightFraction[index]
		}
		if leftDigit < rightDigit {
			return -1
		}
		if leftDigit > rightDigit {
			return 1
		}
	}
	return 0
}

// PostgreSQLNumeric returns the exact text accepted by an unconstrained
// PostgreSQL numeric column without exceeding PostgreSQL's documented digit
// limits.
func (d Decimal) PostgreSQLNumeric() (string, error) {
	if d.canonical == "" {
		return "", fmt.Errorf("decimal is uninitialized")
	}
	value := strings.TrimPrefix(d.canonical, "-")
	integerDigits := len(value)
	fractionalDigits := 0
	if decimalPoint := strings.IndexByte(value, '.'); decimalPoint >= 0 {
		integerDigits = decimalPoint
		fractionalDigits = len(value) - decimalPoint - 1
	}
	if integerDigits > maxPostgresNumericIntegerDigits {
		return "", fmt.Errorf(
			"decimal has %d integer digits; PostgreSQL numeric supports at most %d",
			integerDigits,
			maxPostgresNumericIntegerDigits,
		)
	}
	if fractionalDigits > maxPostgresNumericFractionalDigits {
		return "", fmt.Errorf(
			"decimal has %d fractional digits; PostgreSQL numeric supports at most %d",
			fractionalDigits,
			maxPostgresNumericFractionalDigits,
		)
	}
	return d.canonical, nil
}

func (d *Decimal) UnmarshalJSON(data []byte) error {
	parsed, err := parseDecimalBytes(data)
	if err != nil {
		return err
	}
	*d = parsed
	return nil
}

func (d Decimal) MarshalJSON() ([]byte, error) {
	if d.canonical == "" {
		return nil, fmt.Errorf("decimal is uninitialized")
	}
	return []byte(d.canonical), nil
}

func parseDecimalBytes(data []byte) (Decimal, error) {
	value := string(data)
	if !jsonNumberPattern.MatchString(value) {
		return Decimal{}, fmt.Errorf("invalid JSON number %q", value)
	}

	negative := false
	if value[0] == '-' {
		negative = true
		value = value[1:]
	}

	exponent := int64(0)
	if index := strings.IndexAny(value, "eE"); index >= 0 {
		exponentText := value[index+1:]
		value = value[:index]
		parsed, err := strconv.ParseInt(exponentText, 10, 64)
		if err != nil {
			return Decimal{}, fmt.Errorf("decimal exponent is out of range: %w", err)
		}
		exponent = parsed
	}

	integerPart := value
	fractionPart := ""
	if index := strings.IndexByte(value, '.'); index >= 0 {
		integerPart = value[:index]
		fractionPart = value[index+1:]
	}
	digits := integerPart + fractionPart
	exponentBound := int64(maxCanonicalDecimalBytes + len(data) + 1)
	if exponent > exponentBound || exponent < -exponentBound {
		return Decimal{}, fmt.Errorf("canonical decimal exceeds %d bytes", maxCanonicalDecimalBytes)
	}

	leading := 0
	for leading < len(digits) && digits[leading] == '0' {
		leading++
	}
	if leading == len(digits) {
		return Decimal{canonical: "0"}, nil
	}
	decimalIndex := int64(len(integerPart)) + exponent
	digits = digits[leading:]
	decimalIndex -= int64(leading)

	for len(digits) > 1 && digits[len(digits)-1] == '0' {
		digits = digits[:len(digits)-1]
	}

	outputLength := int64(len(digits))
	switch {
	case decimalIndex <= 0:
		outputLength = 2 - decimalIndex + int64(len(digits))
	case decimalIndex < int64(len(digits)):
		outputLength++
	case decimalIndex > int64(len(digits)):
		outputLength = decimalIndex
	}
	if negative {
		outputLength++
	}
	if outputLength > maxCanonicalDecimalBytes {
		return Decimal{}, fmt.Errorf("canonical decimal exceeds %d bytes", maxCanonicalDecimalBytes)
	}

	var builder strings.Builder
	builder.Grow(int(outputLength))
	if negative {
		builder.WriteByte('-')
	}
	switch {
	case decimalIndex <= 0:
		builder.WriteString("0.")
		builder.WriteString(strings.Repeat("0", int(-decimalIndex)))
		builder.WriteString(digits)
	case decimalIndex >= int64(len(digits)):
		builder.WriteString(digits)
		builder.WriteString(strings.Repeat("0", int(decimalIndex)-len(digits)))
	default:
		builder.WriteString(digits[:decimalIndex])
		builder.WriteByte('.')
		builder.WriteString(digits[decimalIndex:])
	}

	canonical := builder.String()
	if !json.Valid([]byte(canonical)) {
		return Decimal{}, fmt.Errorf("canonical decimal is invalid")
	}
	return Decimal{canonical: canonical}, nil
}
