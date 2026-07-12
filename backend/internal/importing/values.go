package importing

import (
	"fmt"
	"math"
	"time"

	"github.com/kkkzbh/AscendAny/backend/internal/pintia"
)

func postgresInteger(value *pintia.NonNegativeInteger, path string) (any, error) {
	if value == nil {
		return nil, nil
	}
	converted, err := value.Int64()
	if err != nil {
		return nil, importError(ErrorValidation, true, "convert "+path, err)
	}
	return converted, nil
}

func requiredPostgresInteger(value pintia.NonNegativeInteger, path string) (int64, error) {
	converted, err := value.Int64()
	if err != nil {
		return 0, importError(ErrorValidation, true, "convert "+path, err)
	}
	return converted, nil
}

func postgresDecimal(value *pintia.Decimal, path string) (any, error) {
	if value == nil {
		return nil, nil
	}
	converted, err := value.PostgreSQLNumeric()
	if err != nil {
		return nil, importError(ErrorValidation, true, "convert "+path, err)
	}
	return converted, nil
}

func postgresInstant(value *pintia.Instant) any {
	if value == nil {
		return nil
	}
	return normalizeInstant(value.Time)
}

func normalizeInstant(value time.Time) time.Time {
	return time.UnixMilli(value.UTC().UnixMilli()).UTC()
}

func nextRevision(current int64, operation string) (int64, error) {
	if current < 0 || current == math.MaxInt64 {
		return 0, importError(ErrorHeadConflict, false, operation, fmt.Errorf("head revision %d cannot advance", current))
	}
	return current + 1, nil
}
