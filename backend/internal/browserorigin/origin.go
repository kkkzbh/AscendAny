package browserorigin

import (
	"errors"
	"fmt"
	"net/netip"
	"net/url"
	"sort"
	"strconv"
	"strings"
)

const MaxAllowed = 16

func ParseList(raw string) ([]string, error) {
	parts := strings.Split(raw, ",")
	for index, origin := range parts {
		if origin == "" || strings.TrimSpace(origin) != origin {
			return nil, fmt.Errorf("entry %d must not be empty or padded", index+1)
		}
	}
	return Canonicalize(parts)
}

func Canonicalize(origins []string) ([]string, error) {
	if len(origins) == 0 {
		return nil, errors.New("at least one origin is required")
	}
	if len(origins) > MaxAllowed {
		return nil, fmt.Errorf("at most %d origins are allowed", MaxAllowed)
	}
	seen := make(map[string]struct{}, len(origins))
	result := make([]string, len(origins))
	for index, origin := range origins {
		if err := Validate(origin); err != nil {
			return nil, fmt.Errorf("entry %d: %w", index+1, err)
		}
		if _, duplicate := seen[origin]; duplicate {
			return nil, fmt.Errorf("duplicate origin %q", origin)
		}
		seen[origin] = struct{}{}
		result[index] = origin
	}
	sort.Strings(result)
	return result, nil
}

func Validate(rawOrigin string) error {
	parsed, err := url.Parse(rawOrigin)
	if err != nil || parsed.Scheme == "" || parsed.Host == "" || parsed.Hostname() == "" {
		return errors.New("must be an absolute browser origin")
	}
	if parsed.User != nil || parsed.Path != "" || parsed.RawPath != "" || parsed.RawQuery != "" ||
		parsed.ForceQuery || parsed.Fragment != "" || parsed.Opaque != "" {
		return errors.New("must contain only scheme and authority")
	}
	if strings.IndexFunc(rawOrigin, func(value rune) bool { return value > 127 }) >= 0 ||
		parsed.Scheme != strings.ToLower(parsed.Scheme) || parsed.Host != strings.ToLower(parsed.Host) {
		return errors.New("must use lowercase canonical ASCII form")
	}
	canonical := (&url.URL{Scheme: parsed.Scheme, Host: parsed.Host}).String()
	if canonical != rawOrigin {
		return errors.New("must use canonical ASCII origin form without a trailing slash")
	}
	if port := parsed.Port(); port != "" {
		value, err := strconv.ParseUint(port, 10, 16)
		if err != nil || value == 0 || strconv.FormatUint(value, 10) != port {
			return errors.New("port must be canonical and between 1 and 65535")
		}
		if (parsed.Scheme == "https" && port == "443") || (parsed.Scheme == "http" && port == "80") {
			return errors.New("default port must be omitted")
		}
	}
	switch parsed.Scheme {
	case "https":
		return nil
	case "http":
		hostname := parsed.Hostname()
		if hostname == "localhost" {
			return nil
		}
		address, err := netip.ParseAddr(hostname)
		if err == nil && address.IsLoopback() && !address.Is4In6() && address.String() == hostname {
			return nil
		}
		return errors.New("HTTP is allowed only for a canonical loopback host")
	case "ascendany-app":
		if parsed.Host == "bundle" {
			return nil
		}
		return errors.New("AscendAny desktop origin must be ascendany-app://bundle")
	case "capacitor":
		if parsed.Host == "localhost" {
			return nil
		}
		return errors.New("Capacitor origin must be capacitor://localhost")
	default:
		return errors.New("scheme must be HTTPS, loopback HTTP, ascendany-app, or capacitor")
	}
}
