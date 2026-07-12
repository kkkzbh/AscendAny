package httpapi

import (
	"errors"
	"strconv"
	"strings"
)

func parseCursorPageQuery(
	rawQuery string,
	forceQuery bool,
	defaultLimit int,
	maximumLimit int,
	validCursor func(string) bool,
) (*string, int, error) {
	if defaultLimit < 1 || maximumLimit < defaultLimit || validCursor == nil {
		return nil, 0, errors.New("pagination parser configuration is invalid")
	}
	if rawQuery == "" && !forceQuery {
		return nil, defaultLimit, nil
	}
	if rawQuery == "" {
		return nil, 0, errors.New("empty query marker is not canonical")
	}

	limit := defaultLimit
	var cursor *string
	seen := make(map[string]struct{}, 2)
	for _, field := range strings.Split(rawQuery, "&") {
		name, value, found := strings.Cut(field, "=")
		if !found || name == "" || value == "" {
			return nil, 0, errors.New("query field must contain one non-empty value")
		}
		if _, duplicate := seen[name]; duplicate {
			return nil, 0, errors.New("query field is duplicated")
		}
		seen[name] = struct{}{}
		switch name {
		case "cursor":
			if !validCursor(value) {
				return nil, 0, errors.New("cursor is not canonical")
			}
			cursorValue := value
			cursor = &cursorValue
		case "limit":
			if value[0] == '0' || len(value) > len(strconv.Itoa(maximumLimit)) {
				return nil, 0, errors.New("limit is not canonical decimal")
			}
			for _, character := range value {
				if character < '0' || character > '9' {
					return nil, 0, errors.New("limit is not decimal")
				}
			}
			parsed, err := strconv.Atoi(value)
			if err != nil || parsed < 1 || parsed > maximumLimit {
				return nil, 0, errors.New("limit is outside the supported range")
			}
			limit = parsed
		default:
			return nil, 0, errors.New("query field is unknown")
		}
	}
	return cursor, limit, nil
}
