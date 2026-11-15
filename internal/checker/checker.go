package checker

import (
	"context"
	"fmt"
	"log/slog"
	"net/http"
	"time"

	"stillalive/internal/config"
	"stillalive/internal/emailmsg"
	"stillalive/internal/store"
)

// Checker evaluates monitor due times and queues alert mail.
type Checker struct {
	store  *store.Store
	cfg    config.Config
	logger *slog.Logger
	client *http.Client
}

// New creates a checker.
func New(st *store.Store, cfg config.Config, logger *slog.Logger) *Checker {
	return &Checker{
		store:  st,
		cfg:    cfg,
		logger: logger,
		client: &http.Client{Timeout: 10 * time.Second},
	}
}

// RunEvery evaluates immediately and then on every tick until the context is
// cancelled.
func (c *Checker) RunEvery(ctx context.Context, interval time.Duration) {
	if interval <= 0 {
		interval = time.Minute
	}
	if err := c.Run(ctx); err != nil && ctx.Err() == nil {
		c.logger.Error("monitor evaluation", "error", err)
	}
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			if err := c.Run(ctx); err != nil && ctx.Err() == nil {
				c.logger.Error("monitor evaluation", "error", err)
			}
		}
	}
}

// Run evaluates at the current time.
func (c *Checker) Run(ctx context.Context) error {
	return c.RunAt(ctx, time.Now().UTC())
}

// RunAt evaluates at a fixed instant. Tests use this for deterministic
// lifecycle checks.
func (c *Checker) RunAt(ctx context.Context, now time.Time) error {
	var alerts, allClears int

	err := c.store.InTx(ctx, func(tx *store.Store) error {
		due, err := tx.DueCandidates(ctx, now)
		if err != nil {
			return err
		}
		for i := range due {
			cand := &due[i]

			switch {
			case !cand.IsOverdue:
				// First missed check-in of this incident.
				if cand.UserConfirmed {
					if err := enqueueAlert(ctx, tx, c.cfg, cand, 1, now); err != nil {
						return err
					}
					alerts++
				}
				if err := tx.RaiseAlert(ctx, cand.ID, now); err != nil {
					return err
				}

			case cand.AlertEmailCount < c.cfg.MaxAlertEmails && reminderDue(cand.LastAlertEmailAt, now, c.cfg.ReminderInterval):
				if cand.UserConfirmed {
					if err := enqueueAlert(ctx, tx, c.cfg, cand, cand.AlertEmailCount+1, now); err != nil {
						return err
					}
					alerts++
				}
				if err := tx.BumpAlert(ctx, cand.ID, now); err != nil {
					return err
				}
			}
		}

		recovered, err := tx.OverdueCandidates(ctx, now)
		if err != nil {
			return err
		}
		for i := range recovered {
			cand := &recovered[i]
			if cand.UserConfirmed {
				msg := emailmsg.AllClear(c.cfg, *cand)
				if _, err := tx.EnqueueEmail(ctx, msg.To, msg.Subject, msg.HTML, msg.Text, now); err != nil {
					return err
				}
				allClears++
			}
			if err := tx.ClearAlert(ctx, cand.ID, now); err != nil {
				return err
			}
		}
		return nil
	})
	if err != nil {
		return fmt.Errorf("evaluate monitors: %w", err)
	}

	if alerts > 0 || allClears > 0 {
		c.logger.Info("monitor evaluation", "alerts", alerts, "all_clears", allClears)
	}
	c.selfPing(ctx)
	return nil
}

func enqueueAlert(ctx context.Context, tx *store.Store, cfg config.Config, cand *store.Candidate, number int, now time.Time) error {
	msg := emailmsg.Alert(cfg, *cand, number)
	if _, err := tx.EnqueueEmail(ctx, msg.To, msg.Subject, msg.HTML, msg.Text, now); err != nil {
		return err
	}
	return nil
}

func reminderDue(last *time.Time, now time.Time, interval time.Duration) bool {
	if last == nil {
		return true
	}
	return !now.Before(last.Add(interval))
}

// selfPing optionally notifies another Still Alive instance (or any URL) that
// this instance is alive.
func (c *Checker) selfPing(ctx context.Context) {
	if c.cfg.PostPingURL == "" {
		return
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.cfg.PostPingURL, nil)
	if err != nil {
		c.logger.Warn("outbound ping: build request", "error", err)
		return
	}
	resp, err := c.client.Do(req)
	if err != nil {
		c.logger.Warn("outbound ping failed", "url", c.cfg.PostPingURL, "error", err)
		return
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode > 299 {
		c.logger.Warn("outbound ping status", "url", c.cfg.PostPingURL, "status", resp.StatusCode)
	}
}
