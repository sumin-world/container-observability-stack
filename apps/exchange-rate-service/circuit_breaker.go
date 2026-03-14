package main

import (
	"sync"
	"time"
)

// State represents the circuit breaker state.
type State int

const (
	StateClosed   State = 0 // normal — requests pass through
	StateOpen     State = 1 // tripped — requests blocked
	StateHalfOpen State = 2 // testing — limited requests allowed
)

func (s State) String() string {
	switch s {
	case StateClosed:
		return "closed"
	case StateOpen:
		return "open"
	case StateHalfOpen:
		return "half-open"
	default:
		return "unknown"
	}
}

// CircuitBreaker implements a simple circuit breaker pattern.
//
// How it works:
//   - Starts in CLOSED state (all requests pass through)
//   - When consecutive failures reach the threshold → trips to OPEN
//   - In OPEN state, all requests are rejected immediately
//   - After a cooldown period → moves to HALF-OPEN
//   - In HALF-OPEN, one request is allowed through as a test
//   - If it succeeds → back to CLOSED
//   - If it fails → back to OPEN
type CircuitBreaker struct {
	mu               sync.RWMutex
	state            State
	failureCount     int
	failureThreshold int
	cooldownDuration time.Duration
	lastFailureTime  time.Time
}

// NewCircuitBreaker creates a circuit breaker with the given thresholds.
func NewCircuitBreaker(failureThreshold int, cooldownDuration time.Duration) *CircuitBreaker {
	return &CircuitBreaker{
		state:            StateClosed,
		failureThreshold: failureThreshold,
		cooldownDuration: cooldownDuration,
	}
}

// Allow checks whether a request should be allowed through.
func (cb *CircuitBreaker) Allow() bool {
	cb.mu.RLock()
	state := cb.state
	lastFail := cb.lastFailureTime
	cb.mu.RUnlock()

	switch state {
	case StateClosed:
		return true

	case StateOpen:
		// Check if cooldown has elapsed → transition to half-open
		if time.Since(lastFail) > cb.cooldownDuration {
			cb.mu.Lock()
			if cb.state == StateOpen { // double-check after acquiring write lock
				cb.state = StateHalfOpen
			}
			cb.mu.Unlock()
			return true
		}
		return false

	case StateHalfOpen:
		// Allow one probe request
		return true

	default:
		return false
	}
}

// RecordSuccess records a successful request.
func (cb *CircuitBreaker) RecordSuccess() {
	cb.mu.Lock()
	defer cb.mu.Unlock()

	cb.failureCount = 0
	cb.state = StateClosed
}

// RecordFailure records a failed request and potentially trips the breaker.
func (cb *CircuitBreaker) RecordFailure() {
	cb.mu.Lock()
	defer cb.mu.Unlock()

	cb.failureCount++
	cb.lastFailureTime = time.Now()

	if cb.failureCount >= cb.failureThreshold {
		cb.state = StateOpen
	}
}

// GetState returns the current state of the circuit breaker.
func (cb *CircuitBreaker) GetState() State {
	cb.mu.RLock()
	defer cb.mu.RUnlock()
	return cb.state
}
