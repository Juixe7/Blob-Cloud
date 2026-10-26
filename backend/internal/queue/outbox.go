package queue

import (
	"context"
	"database/sql"
	"log/slog"
	"time"
)

type OutboxDispatcher struct {
	db        *sql.DB
	publisher Publisher
	log       *slog.Logger
}

func NewOutboxDispatcher(db *sql.DB, publisher Publisher, log *slog.Logger) *OutboxDispatcher {
	return &OutboxDispatcher{db: db, publisher: publisher, log: log}
}

func (d *OutboxDispatcher) Run(ctx context.Context) {
	ticker := time.NewTicker(5 * time.Second)
	defer ticker.Stop()
	for {
		if err := d.DispatchOnce(ctx); err != nil && ctx.Err() == nil {
			d.log.Error("dispatch file processing outbox", "err", err)
		}
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}
	}
}

func (d *OutboxDispatcher) DispatchOnce(ctx context.Context) error {
	return d.dispatch(ctx)
}

func (d *OutboxDispatcher) dispatch(ctx context.Context) error {
	tx, err := d.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()
	rows, err := tx.QueryContext(ctx, `
		SELECT o.file_id::text, o.user_id::text
		FROM file_processing_outbox o
		JOIN files f ON f.id = o.file_id
		WHERE f.status = 'PROCESSING' AND o.next_attempt_at <= NOW()
		ORDER BY o.next_attempt_at, o.created_at
		LIMIT 20 FOR UPDATE OF o SKIP LOCKED`)
	if err != nil {
		return err
	}
	var messages []ThumbnailMessage
	for rows.Next() {
		var msg ThumbnailMessage
		if err := rows.Scan(&msg.FileID, &msg.UserID); err != nil {
			_ = rows.Close()
			return err
		}
		messages = append(messages, msg)
	}
	if err := rows.Err(); err != nil {
		_ = rows.Close()
		return err
	}
	_ = rows.Close()
	for _, msg := range messages {
		if _, err := tx.ExecContext(ctx, `UPDATE file_processing_outbox SET attempts = attempts + 1,
			next_attempt_at = NOW() + INTERVAL '30 seconds' WHERE file_id = $1`, msg.FileID); err != nil {
			return err
		}
	}
	if err := tx.Commit(); err != nil {
		return err
	}
	for _, msg := range messages {
		if err := d.publisher.PublishThumbnailJob(ctx, msg); err != nil {
			d.log.Error("file processing job publish failed; will retry", "file_id", msg.FileID, "err", err)
			continue
		}
		// Keep the outbox row until the worker records a terminal scan state.
		_, _ = d.db.ExecContext(ctx, `UPDATE file_processing_outbox SET next_attempt_at = NOW() + INTERVAL '10 minutes' WHERE file_id = $1`, msg.FileID)
	}
	return nil
}
