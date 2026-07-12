package auth

const productionPasswordWorkSlots = 4

type passwordWorkLimiter struct {
	slots chan struct{}
}

var productionPasswordWorkLimiter = &passwordWorkLimiter{
	slots: make(chan struct{}, productionPasswordWorkSlots),
}

func newPasswordWorkLimiter(capacity int) (*passwordWorkLimiter, error) {
	if capacity < 1 {
		return nil, authError(ErrorInvalidConfiguration, "Password work capacity must be positive.", nil)
	}
	return &passwordWorkLimiter{slots: make(chan struct{}, capacity)}, nil
}

func (limiter *passwordWorkLimiter) tryAcquire() (func(), bool) {
	if limiter == nil || limiter.slots == nil {
		return nil, false
	}
	select {
	case limiter.slots <- struct{}{}:
		return func() { <-limiter.slots }, true
	default:
		return nil, false
	}
}
