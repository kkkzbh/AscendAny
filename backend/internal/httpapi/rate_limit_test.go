package httpapi

import (
	"net/http/httptest"
	"net/netip"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

type mutableRateClock struct {
	mu  sync.Mutex
	now time.Time
}

func (clock *mutableRateClock) Now() time.Time {
	clock.mu.Lock()
	defer clock.mu.Unlock()
	return clock.now
}

func (clock *mutableRateClock) advance(duration time.Duration) {
	clock.mu.Lock()
	defer clock.mu.Unlock()
	clock.now = clock.now.Add(duration)
}

func TestFixedWindowRateLimiterResetsAtExactBoundary(t *testing.T) {
	clock := &mutableRateClock{now: time.Date(2026, 7, 10, 0, 0, 0, 0, time.UTC)}
	limiter, err := newFixedWindowRateLimiter(
		map[string]RateLimit{"auth.login": {Requests: 2, Window: time.Minute}},
		4,
		clock,
	)
	if err != nil {
		t.Fatal(err)
	}
	if !limiter.Allow("auth.login", "192.0.2.1").Allowed ||
		!limiter.Allow("auth.login", "192.0.2.1").Allowed {
		t.Fatal("requests within capacity were rejected")
	}
	denied := limiter.Allow("auth.login", "192.0.2.1")
	if denied.Allowed || denied.RetryAfter != time.Minute {
		t.Fatalf("denied decision = %#v", denied)
	}
	clock.advance(time.Minute)
	if !limiter.Allow("auth.login", "192.0.2.1").Allowed {
		t.Fatal("request at the next exact window was rejected")
	}
}

func TestFixedWindowRateLimiterIsBounded(t *testing.T) {
	clock := &mutableRateClock{now: time.Date(2026, 7, 10, 0, 0, 0, 0, time.UTC)}
	limiter, err := newFixedWindowRateLimiter(
		map[string]RateLimit{"auth.me": {Requests: 1, Window: time.Minute}},
		2,
		clock,
	)
	if err != nil {
		t.Fatal(err)
	}
	for _, client := range []string{"192.0.2.1", "192.0.2.2", "192.0.2.3"} {
		if !limiter.Allow("auth.me", client).Allowed {
			t.Fatalf("first request for %s was rejected", client)
		}
		clock.advance(time.Second)
	}
	if len(limiter.buckets) != 2 {
		t.Fatalf("bucket count = %d, want 2", len(limiter.buckets))
	}
}

func TestFixedWindowRateLimiterConcurrentCapacity(t *testing.T) {
	clock := &mutableRateClock{now: time.Date(2026, 7, 10, 0, 0, 0, 0, time.UTC)}
	limiter, err := newFixedWindowRateLimiter(
		map[string]RateLimit{"auth.refresh": {Requests: 10, Window: time.Minute}},
		2,
		clock,
	)
	if err != nil {
		t.Fatal(err)
	}
	var allowed atomic.Int64
	var wait sync.WaitGroup
	for range 100 {
		wait.Add(1)
		go func() {
			defer wait.Done()
			if limiter.Allow("auth.refresh", "192.0.2.1").Allowed {
				allowed.Add(1)
			}
		}()
	}
	wait.Wait()
	if allowed.Load() != 10 {
		t.Fatalf("allowed = %d, want 10", allowed.Load())
	}
}

func TestDefaultRateLimiterCoversEveryHTTPPolicyScope(t *testing.T) {
	limiter, err := NewDefaultRateLimiter()
	if err != nil {
		t.Fatal(err)
	}
	registry, err := newRouteRegistry(apiRouteContracts(&Handler{}, time.Second, time.Second))
	if err != nil {
		t.Fatal(err)
	}
	for _, contract := range registry.contracts {
		if contract.policy == nil {
			continue
		}
		if !limiter.Allow(contract.policy.rateScope, "test-client:"+contract.examplePath).Allowed {
			t.Fatalf("route %s %s has uncovered rate scope %q", contract.method, contract.pattern, contract.policy.rateScope)
		}
	}
	for _, secondary := range []string{
		"auth.login.username", "auth.register.username", "auth.enrollment.claim.token",
	} {
		if !limiter.Allow(secondary, "test-secondary").Allowed {
			t.Fatalf("secondary rate scope %q is not configured", secondary)
		}
	}
}

func TestDirectClientAddressRejectsForwardingAmbiguity(t *testing.T) {
	for _, input := range []string{"192.0.2.1", "host.example:80", "192.0.2.1:0", "[fe80::1%eth0]:80"} {
		if _, err := directClientAddress(input); err == nil {
			t.Fatalf("directClientAddress(%q) unexpectedly succeeded", input)
		}
	}
	address, err := directClientAddress("[2001:db8::1]:443")
	if err != nil || address != "2001:db8::1" {
		t.Fatalf("IPv6 address = %q, err = %v", address, err)
	}
}

func TestTrustedProxyRequiresExactCanonicalClientHeader(t *testing.T) {
	resolver, err := newClientAddressResolver(
		[]netip.Prefix{netip.MustParsePrefix("127.0.0.1/32"), netip.MustParsePrefix("::1/128")},
		"CF-Connecting-IP",
		[]string{"http://127.0.0.1:5173", "http://localhost:5173", "https://ascendany.example"},
	)
	if err != nil {
		t.Fatal(err)
	}
	valid := httptest.NewRequest("GET", "/api/v2/auth/me", nil)
	valid.RemoteAddr = "127.0.0.1:43100"
	valid.Header.Set("Origin", "https://ascendany.example")
	valid.Header.Set("CF-Connecting-IP", "203.0.113.7")
	address, err := resolver.Resolve(valid)
	if err != nil || address != "203.0.113.7" {
		t.Fatalf("trusted client = %q, err = %v", address, err)
	}

	tests := map[string][]string{
		"invalid":      {"client.example"},
		"noncanonical": {"2001:0db8::1"},
		"multiple":     {"203.0.113.7", "203.0.113.8"},
	}
	for name, values := range tests {
		t.Run(name, func(t *testing.T) {
			request := httptest.NewRequest("GET", "/api/v2/auth/me", nil)
			request.RemoteAddr = "127.0.0.1:43100"
			request.Header.Set("Origin", "http://127.0.0.1:5173")
			for _, value := range values {
				request.Header.Add("CF-Connecting-IP", value)
			}
			if _, err := resolver.Resolve(request); err == nil {
				t.Fatal("trusted proxy request unexpectedly succeeded")
			}
		})
	}

	untrusted := httptest.NewRequest("GET", "/api/v2/auth/me", nil)
	untrusted.RemoteAddr = "192.0.2.25:43100"
	untrusted.Header.Set("Origin", "http://127.0.0.1:5173")
	untrusted.Header.Add("CF-Connecting-IP", "203.0.113.7")
	untrusted.Header.Add("CF-Connecting-IP", "invalid-spoof")
	address, err = resolver.Resolve(untrusted)
	if err != nil || address != "192.0.2.25" {
		t.Fatalf("untrusted peer did not ignore spoofed header: address=%q err=%v", address, err)
	}
}

func TestTrustedLoopbackPeerAllowsOnlyConfiguredLoopbackHTTPOriginWithoutForwardingHeader(t *testing.T) {
	resolver, err := newClientAddressResolver(
		[]netip.Prefix{netip.MustParsePrefix("127.0.0.1/32"), netip.MustParsePrefix("::1/128")},
		"CF-Connecting-IP",
		[]string{"http://127.0.0.1:5173", "http://localhost:5173", "https://ascendany.example"},
	)
	if err != nil {
		t.Fatal(err)
	}

	for _, origin := range []string{"http://127.0.0.1:5173", "http://localhost:5173"} {
		t.Run(origin, func(t *testing.T) {
			request := httptest.NewRequest("GET", "/api/v2/auth/me", nil)
			request.RemoteAddr = "127.0.0.1:43100"
			request.Header.Set("Origin", origin)
			address, err := resolver.Resolve(request)
			if err != nil || address != "127.0.0.1" {
				t.Fatalf("loopback client = %q, err = %v", address, err)
			}
		})
	}

	for _, origin := range []string{"", "https://ascendany.example", "http://[::1]:5173"} {
		t.Run("reject_"+origin, func(t *testing.T) {
			request := httptest.NewRequest("GET", "/api/v2/auth/me", nil)
			request.RemoteAddr = "127.0.0.1:43100"
			if origin != "" {
				request.Header.Set("Origin", origin)
			}
			if _, err := resolver.Resolve(request); err == nil {
				t.Fatal("trusted proxy request without a forwarding header unexpectedly succeeded")
			}
		})
	}

	nonLoopbackResolver, err := newClientAddressResolver(
		[]netip.Prefix{netip.MustParsePrefix("192.0.2.0/24")},
		"CF-Connecting-IP",
		[]string{"http://127.0.0.1:5173"},
	)
	if err != nil {
		t.Fatal(err)
	}
	nonLoopbackRequest := httptest.NewRequest("GET", "/api/v2/auth/me", nil)
	nonLoopbackRequest.RemoteAddr = "192.0.2.25:43100"
	nonLoopbackRequest.Header.Set("Origin", "http://127.0.0.1:5173")
	if _, err := nonLoopbackResolver.Resolve(nonLoopbackRequest); err == nil {
		t.Fatal("trusted non-loopback peer unexpectedly used the loopback-origin capability")
	}
}

func TestTrustedProxyConfigurationIsExplicit(t *testing.T) {
	if _, err := newClientAddressResolver(nil, "CF-Connecting-IP", nil); err == nil {
		t.Fatal("client header without trusted proxy was accepted")
	}
	if _, err := newClientAddressResolver(
		[]netip.Prefix{netip.MustParsePrefix("127.0.0.1/32")},
		"X-Forwarded-For",
		nil,
	); err == nil {
		t.Fatal("unapproved proxy header was accepted")
	}
	if _, err := newClientAddressResolver(
		[]netip.Prefix{netip.MustParsePrefix("192.0.2.1/24")},
		"CF-Connecting-IP",
		nil,
	); err == nil {
		t.Fatal("unmasked trusted proxy prefix was accepted")
	}
}
