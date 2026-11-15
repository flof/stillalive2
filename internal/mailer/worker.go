package mailer

import (
	"context"
	"fmt"
	"log/slog"
	"time"

	"stillalive/internal/store"
)

// Worker drains the email outbox.
type Worker struct {
	store       *store.Store
	sender      Sender
	logger      *slog.Logger
	interval    time.Duration
	maxAttempts int
}

// NewWorker creates an outbox worker. maxAttempts controls how often a failing
// message is retried before it is parked in the failed state.
func NewWorker(st *store.Store, sender Sender, logger *slog.Logger, interval time.Duration, maxAttempts int) *Worker {
	if interval <= 0 {
		interval = 10 * time.Second
	}
	if maxAttempts < 1 {
		maxAttempts = 5
	}
	return &Worker{store: st, sender: sender, logger: logger, interval: interval, maxAttempts: maxAttempts}
}

// Run processes the outbox until the context is cancelled.
func (w *Worker) Run(ctx context.Context) {
	ticker := time.NewTicker(w.interval)
	defer ticker.Stop()
	for {
		if err := w.Process(ctx); err != nil && ctx.Err() == nil {
			w.logger.Error("email outbox", "error", err)
		}
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}
	}
}

// Process attempts to deliver all currently due messages.
func (w *Worker) Process(ctx context.Context) error {
	now := time.Now().UTC()
	messages, err := w.store.PendingEmails(ctx, now, 25)
	if err != nil {
		return err
	}
	var firstErr error
	for _, e := range messages {
		err := w.sender.Send(ctx, Message{To: e.To, Subject: e.Subject, HTML: e.BodyHTML, Text: e.BodyText})
		if err == nil {
			if markErr := w.store.MarkEmailSent(ctx, e.ID, now); markErr != nil && firstErr == nil {
				firstErr = markErr
			}
			w.logger.Info("email sent", "to", e.To, "subject", e.Subject)
			continue
		}

		attempts := e.Attempts + 1
		giveUp := attempts >= w.maxAttempts
		next := now.Add(time.Duration(attempts) * 5 * time.Minute)
		if markErr := w.store.MarkEmailFailed(ctx, e.ID, err.Error(), next, giveUp); markErr != nil && firstErr == nil {
			firstErr = markErr
		}
		w.logger.Warn("email delivery failed",
			"to", e.To, "attempt", attempts, "give_up", giveUp, "error", err)
	}
	return firstErr
}

// SendNow is a convenience for transactional mail that must not wait for the
// next tick, such as registration and password resets.
func (w *Worker) SendNow(ctx context.Context, m Message) error {
	if err := w.sender.Send(ctx, m); err != nil {
		return fmt.Errorf("send mail: %w", err)
	}
	return nil
}
