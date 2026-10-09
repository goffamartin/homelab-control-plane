package controller

import (
	"errors"
	"time"
)

var (
	ErrACKTimeout    = errors.New("hlcp controller: command acknowledgement timed out")
	ErrResultTimeout = errors.New("hlcp controller: command outcome remains unresolved")
)

type RetryPolicy struct {
	ACKTimeout    time.Duration
	ResultTimeout time.Duration
	MaxAttempts   int
}

func DefaultRetryPolicy() RetryPolicy {
	return RetryPolicy{ACKTimeout: 5 * time.Second, ResultTimeout: 30 * time.Second, MaxAttempts: 6}
}

func (policy RetryPolicy) Validate() error {
	if policy.ACKTimeout <= 0 || policy.ACKTimeout > time.Minute || policy.ResultTimeout <= 0 || policy.ResultTimeout > 24*time.Hour || policy.MaxAttempts < 1 || policy.MaxAttempts > 100 {
		return errors.New("invalid command retry policy: ACK 0..1m, result 0..24h, attempts 1..100")
	}
	if policy.ACKTimeout > policy.ResultTimeout {
		return errors.New("command ACK timeout must not exceed result timeout")
	}
	return nil
}
