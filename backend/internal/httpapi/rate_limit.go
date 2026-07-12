package httpapi

import (
	"container/list"
	"fmt"
	"sync"
	"time"
)

type RateLimit struct {
	Requests int
	Window   time.Duration
}

type RateLimitDecision struct {
	Allowed    bool
	RetryAfter time.Duration
}

type RequestRateLimiter interface {
	Allow(scope, key string) RateLimitDecision
}

type rateLimitClock interface {
	Now() time.Time
}

type wallClock struct{}

func (wallClock) Now() time.Time { return time.Now() }

type rateBucket struct {
	windowStart time.Time
	requests    int
	recency     *list.Element
}

// FixedWindowRateLimiter is a bounded, mutex-protected limiter keyed by the
// exact scope and key supplied by the caller.
type FixedWindowRateLimiter struct {
	mu         sync.Mutex
	clock      rateLimitClock
	limits     map[string]RateLimit
	maxBuckets int
	buckets    map[string]*rateBucket
	recency    *list.List
}

func NewFixedWindowRateLimiter(limits map[string]RateLimit, maxBuckets int) (*FixedWindowRateLimiter, error) {
	return newFixedWindowRateLimiter(limits, maxBuckets, wallClock{})
}

func newFixedWindowRateLimiter(
	limits map[string]RateLimit,
	maxBuckets int,
	clock rateLimitClock,
) (*FixedWindowRateLimiter, error) {
	if len(limits) == 0 || maxBuckets < 1 || clock == nil {
		return nil, fmt.Errorf("rate limiter requires limits, positive capacity, and a clock")
	}
	copied := make(map[string]RateLimit, len(limits))
	for scope, limit := range limits {
		if scope == "" || limit.Requests < 1 || limit.Window <= 0 {
			return nil, fmt.Errorf("rate limit for %q is invalid", scope)
		}
		copied[scope] = limit
	}
	return &FixedWindowRateLimiter{
		clock:      clock,
		limits:     copied,
		maxBuckets: maxBuckets,
		buckets:    make(map[string]*rateBucket, maxBuckets),
		recency:    list.New(),
	}, nil
}

func NewDefaultRateLimiter() (*FixedWindowRateLimiter, error) {
	return NewFixedWindowRateLimiter(map[string]RateLimit{
		"api.capabilities":                  {Requests: 120, Window: time.Minute},
		"auth.login":                        {Requests: 20, Window: 5 * time.Minute},
		"auth.login.username":               {Requests: 10, Window: 15 * time.Minute},
		"auth.refresh":                      {Requests: 60, Window: time.Minute},
		"auth.logout":                       {Requests: 30, Window: time.Minute},
		"auth.me":                           {Requests: 120, Window: time.Minute},
		"auth.enrollment.claim":             {Requests: 20, Window: 15 * time.Minute},
		"auth.enrollment.claim.token":       {Requests: 10, Window: 15 * time.Minute},
		"admin.enrollment.issue":            {Requests: 30, Window: time.Minute},
		"admin.enrollment.revoke":           {Requests: 60, Window: time.Minute},
		"account.profile.update":            {Requests: 30, Window: time.Minute},
		"account.sessions.list":             {Requests: 120, Window: time.Minute},
		"account.sessions.revoke":           {Requests: 60, Window: time.Minute},
		"students.me.analytics":             {Requests: 120, Window: time.Minute},
		"students.me.achievements":          {Requests: 120, Window: time.Minute},
		"students.me.recommendation":        {Requests: 120, Window: time.Minute},
		"students.me.notes.list":            {Requests: 120, Window: time.Minute},
		"students.me.notes.get":             {Requests: 120, Window: time.Minute},
		"students.me.notes.create":          {Requests: 60, Window: time.Minute},
		"students.me.notes.replace":         {Requests: 60, Window: time.Minute},
		"students.me.notes.archive":         {Requests: 60, Window: time.Minute},
		"students.me.notes.restore":         {Requests: 60, Window: time.Minute},
		"students.me.chat.threads.list":     {Requests: 120, Window: time.Minute},
		"students.me.chat.threads.create":   {Requests: 30, Window: time.Minute},
		"students.me.chat.messages.list":    {Requests: 180, Window: time.Minute},
		"students.me.agent.runs.enqueue":    {Requests: 60, Window: time.Minute},
		"students.me.auto-analysis.enqueue": {Requests: 30, Window: time.Minute},
		"students.me.agent.runs.get":        {Requests: 180, Window: time.Minute},
		"students.me.agent.runs.events":     {Requests: 30, Window: time.Minute},
		"students.leaderboard":              {Requests: 120, Window: time.Minute},
		"oj.problems.list":                  {Requests: 120, Window: time.Minute},
		"oj.problems.get":                   {Requests: 120, Window: time.Minute},
		"admin.oj.problems.create-version":  {Requests: 20, Window: 15 * time.Minute},
		"oj.submissions.create":             {Requests: 60, Window: time.Minute},
		"oj.submissions.get":                {Requests: 180, Window: time.Minute},
		"oj.submissions.events":             {Requests: 30, Window: time.Minute},
		"lsp.sessions.create":               {Requests: 10, Window: time.Minute},
		"lsp.sessions.close":                {Requests: 30, Window: time.Minute},
		"lsp.sessions.attach":               {Requests: 20, Window: time.Minute},
		"exams.list":                        {Requests: 120, Window: time.Minute},
		"exams.get":                         {Requests: 120, Window: time.Minute},
		"exams.analysis-generation.get":     {Requests: 120, Window: time.Minute},
		"exams.analysis-generation.events":  {Requests: 30, Window: time.Minute},
		"admin.accounts.list":               {Requests: 120, Window: time.Minute},
		"admin.accounts.state":              {Requests: 30, Window: time.Minute},
		"admin.students.list":               {Requests: 120, Window: time.Minute},
		"admin.audit.list":                  {Requests: 120, Window: time.Minute},
		"admin.configurations.list":         {Requests: 120, Window: time.Minute},
		"admin.configurations.get":          {Requests: 120, Window: time.Minute},
		"admin.configurations.versions.list": {
			Requests: 120,
			Window:   time.Minute,
		},
		"admin.configurations.create-version":                {Requests: 30, Window: time.Minute},
		"admin.model-connections.test":                       {Requests: 10, Window: 15 * time.Minute},
		"admin.recommendation.training-runs.create":          {Requests: 10, Window: 15 * time.Minute},
		"admin.recommendation.review-context.get":            {Requests: 60, Window: time.Minute},
		"admin.recommendation.training-runs.get":             {Requests: 120, Window: time.Minute},
		"admin.recommendation.training-runs.events.list":     {Requests: 120, Window: time.Minute},
		"feedback.submit":                                    {Requests: 30, Window: time.Minute},
		"imports.create":                                     {Requests: 10, Window: 15 * time.Minute},
		"imports.list":                                       {Requests: 120, Window: time.Minute},
		"imports.get":                                        {Requests: 120, Window: time.Minute},
		"imports.events":                                     {Requests: 30, Window: time.Minute},
		"internal.recommendation.trainer-agent.claim.ip":     {Requests: 30, Window: time.Minute},
		"internal.recommendation.trainer-agent.claim.agent":  {Requests: 30, Window: time.Minute},
		"internal.recommendation.trainer-agent.heartbeat.ip": {Requests: 360, Window: time.Minute},
		"internal.recommendation.trainer-agent.output.ip":    {Requests: 30, Window: time.Minute},
		"internal.recommendation.trainer-agent.failure.ip":   {Requests: 30, Window: time.Minute},
	}, 8192)
}

func (limiter *FixedWindowRateLimiter) Allow(scope, keyValue string) RateLimitDecision {
	limiter.mu.Lock()
	defer limiter.mu.Unlock()

	limit, known := limiter.limits[scope]
	if !known || keyValue == "" {
		return RateLimitDecision{Allowed: false, RetryAfter: time.Second}
	}
	now := limiter.clock.Now()
	key := scope + "\x00" + keyValue
	bucket, exists := limiter.buckets[key]
	if exists && !now.Before(bucket.windowStart.Add(limit.Window)) {
		bucket.windowStart = now
		bucket.requests = 0
	}
	if !exists {
		if len(limiter.buckets) >= limiter.maxBuckets {
			limiter.evictOldest()
		}
		bucket = &rateBucket{windowStart: now}
		bucket.recency = limiter.recency.PushBack(key)
		limiter.buckets[key] = bucket
	} else {
		limiter.recency.MoveToBack(bucket.recency)
	}
	if bucket.requests >= limit.Requests {
		retry := bucket.windowStart.Add(limit.Window).Sub(now)
		if retry < time.Second {
			retry = time.Second
		}
		return RateLimitDecision{Allowed: false, RetryAfter: retry}
	}
	bucket.requests++
	return RateLimitDecision{Allowed: true}
}

func (limiter *FixedWindowRateLimiter) evictOldest() {
	oldest := limiter.recency.Front()
	if oldest == nil {
		return
	}
	delete(limiter.buckets, oldest.Value.(string))
	limiter.recency.Remove(oldest)
}
