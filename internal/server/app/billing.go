package app

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"sync"
	"time"
)

var ErrUnpaid = errors.New("New deploys are paused until this panel's subscription is paid; running apps keep running.")

var ErrNoBilling = errors.New("This panel has no billing.")

type Billing struct {
	URL, Token string
	HTTP       *http.Client

	mu   sync.Mutex
	last *BillingState
	at   time.Time
}

type BillingState struct {
	State       string `json:"state"`
	TrialEndsMs int64  `json:"trial_ends,omitempty"`
	Deploys     bool   `json:"deploys"`
	CardURL     string `json:"card_url,omitempty"`
}

func (a *App) BillingState(ctx context.Context) (BillingState, error) {
	b := a.Billing
	if b == nil {
		return BillingState{}, ErrNoBilling
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	now := a.Now()
	if b.last != nil && now.Sub(b.at) < 30*time.Second {
		return *b.last, nil
	}
	s, err := b.ask(ctx)
	switch {
	case err == nil:
		b.last, b.at = &s, now
	case b.last != nil:
		slog.Warn("billing: the front did not answer", "err", err)
		return *b.last, nil
	default:
		return BillingState{}, err
	}
	return s, nil
}

func (b *Billing) ask(ctx context.Context) (BillingState, error) {
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, "GET", b.URL, nil)
	if err != nil {
		return BillingState{}, err
	}
	req.Header.Set("Authorization", "Bearer "+b.Token)
	hc := b.HTTP
	if hc == nil {
		hc = http.DefaultClient
	}
	res, err := hc.Do(req)
	if err != nil {
		return BillingState{}, err
	}
	defer res.Body.Close()
	if res.StatusCode != 200 {
		return BillingState{}, fmt.Errorf("the cloud's front answered %d", res.StatusCode)
	}
	var s BillingState
	return s, json.NewDecoder(io.LimitReader(res.Body, 64<<10)).Decode(&s)
}

func (a *App) unpaid(ctx context.Context) error {
	if a.Billing == nil {
		return nil
	}
	s, err := a.BillingState(ctx)
	if err != nil {
		slog.Warn("billing: no answer from the front yet; deploys go on", "err", err)
		return nil
	}
	if !s.Deploys {
		return ErrUnpaid
	}
	return nil
}
