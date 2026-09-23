package pool

import (
	"errors"
	"fmt"
	"math/rand"
	"sync"
	"sync/atomic"
	"time"

	"overclock/pkg/auth"
)

// KeyState tracks the health and telemetry of an individual API key or OAuth account.
type KeyState struct {
	Key             string
	Index           int
	TotalRequests   uint64
	SuccessRequests uint64
	RateLimitErrors uint64
	OtherErrors     uint64
	CooldownUntil   time.Time
	LastUsed        time.Time
	IsOAuth         bool
	Account         *auth.OAuthAccount
}

// GetAuthToken returns the token to use (refreshes automatically if OAuth).
func (s *KeyState) GetAuthToken() (string, error) {
	if s.IsOAuth && s.Account != nil {
		return s.Account.GetValidToken()
	}
	return s.Key, nil
}

// DisplayLabel returns a friendly masked label for terminal output.
func (s *KeyState) DisplayLabel() string {
	if s.IsOAuth && s.Account != nil {
		email := s.Account.Email
		if email == "" {
			email = s.Account.Name
		}
		return fmt.Sprintf("oauth:%s", email)
	}
	return MaskKey(s.Key)
}

// KeyPool manages a thread-safe rotating pool of API keys and OAuth accounts with auto-failover.
type KeyPool struct {
	mu           sync.RWMutex
	keys         []*KeyState
	currentIndex uint64
	baseCooldown time.Duration
}

// NewAuthPool initializes the pool with both API keys and OAuth profiles.
func NewAuthPool(rawKeys []string, accounts []*auth.OAuthAccount) (*KeyPool, error) {
	if len(rawKeys) == 0 && len(accounts) == 0 {
		return nil, errors.New("nenhuma chave de API ou conta OAuth encontrada")
	}

	var states []*KeyState
	idx := 0

	for _, k := range rawKeys {
		if k != "" {
			states = append(states, &KeyState{
				Key:     k,
				Index:   idx,
				IsOAuth: false,
			})
			idx++
		}
	}

	for _, acc := range accounts {
		if acc != nil {
			states = append(states, &KeyState{
				Key:     acc.AccessToken,
				Index:   idx,
				IsOAuth: true,
				Account: acc,
			})
			idx++
		}
	}

	return &KeyPool{
		keys:         states,
		baseCooldown: 20 * time.Second,
	}, nil
}

// NewKeyPool maintains backward compatibility for raw API key lists.
func NewKeyPool(rawKeys []string) (*KeyPool, error) {
	return NewAuthPool(rawKeys, nil)
}

// Size returns total count of active keys/accounts in pool.
func (p *KeyPool) Size() int {
	p.mu.RLock()
	defer p.mu.RUnlock()
	return len(p.keys)
}

// Acquire selects the next healthy API key or OAuth account using round-robin.
func (p *KeyPool) Acquire() (*KeyState, time.Duration, error) {
	p.mu.Lock()
	defer p.mu.Unlock()

	n := len(p.keys)
	if n == 0 {
		return nil, 0, errors.New("empty auth pool")
	}

	now := time.Now()
	var earliestWait time.Duration = -1
	var earliestKey *KeyState

	startIdx := int(atomic.AddUint64(&p.currentIndex, 1) % uint64(n))
	for i := 0; i < n; i++ {
		idx := (startIdx + i) % n
		state := p.keys[idx]

		if now.After(state.CooldownUntil) {
			state.LastUsed = now
			atomic.AddUint64(&state.TotalRequests, 1)
			return state, 0, nil
		}

		wait := state.CooldownUntil.Sub(now)
		if earliestWait < 0 || wait < earliestWait {
			earliestWait = wait
			earliestKey = state
		}
	}

	return earliestKey, earliestWait, nil
}

// MarkSuccess records a successful API call.
func (p *KeyPool) MarkSuccess(state *KeyState) {
	p.mu.Lock()
	defer p.mu.Unlock()
	atomic.AddUint64(&state.SuccessRequests, 1)
}

// MarkRateLimited puts the key into quarantine cooldown and applies jitter.
func (p *KeyPool) MarkRateLimited(state *KeyState, retryAfter time.Duration) time.Duration {
	p.mu.Lock()
	defer p.mu.Unlock()

	atomic.AddUint64(&state.RateLimitErrors, 1)

	cooldown := retryAfter
	if cooldown <= 0 {
		factor := float64(state.RateLimitErrors)
		if factor > 5 {
			factor = 5
		}
		jitter := time.Duration(rand.Int63n(int64(2 * time.Second)))
		cooldown = p.baseCooldown*time.Duration(factor) + jitter
	}

	state.CooldownUntil = time.Now().Add(cooldown)
	return cooldown
}

// MarkError records generic failure.
func (p *KeyPool) MarkError(state *KeyState) {
	p.mu.Lock()
	defer p.mu.Unlock()
	atomic.AddUint64(&state.OtherErrors, 1)
}

// MaskKey returns a safely obfuscated key string for logging (e.g., AIzaSy...9xK).
func MaskKey(key string) string {
	if len(key) <= 8 {
		return "****"
	}
	return fmt.Sprintf("%s...%s", key[:6], key[len(key)-4:])
}
