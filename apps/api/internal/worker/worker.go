// Package worker drains the pgmq queues that pg_cron asks the API to work
// through (ADR 0003, ADR 0017).
//
// A drain reads each queue in small batches, hands every message to the
// queue's handler, and archives it once the handler succeeds. A failed
// message becomes visible again after the visibility timeout; after its last
// attempt (counted by pgmq's read_ct) it is archived and logged, so one bad
// message never blocks a queue for good.
package worker

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
)

// Message is one queue message as the handler sees it.
type Message struct {
	ID      int64
	Attempt int32 // 1 on the first read
	Body    json.RawMessage
}

// Handler does the work of one message. A nil error means done: the message
// is archived. Handlers must be safe to repeat, because a message can be
// read again after a crash or a timeout.
type Handler func(ctx context.Context, m Message) error

// Queue is one pgmq queue and how to drain it.
type Queue struct {
	Name        string
	Handle      Handler
	MaxAttempts int32
	// Visibility is how long a read message stays hidden from other reads;
	// it must be longer than Drainer.Budget plus one message's work.
	Visibility time.Duration
}

// Counts is what one drain did with one queue.
type Counts struct {
	Done    int `json:"done"`
	Failed  int `json:"failed"`
	GivenUp int `json:"given_up"`
}

// Drainer drains its queues, one after the other.
type Drainer struct {
	Pool   *pgxpool.Pool
	Log    *slog.Logger
	Queues []Queue
	// Budget bounds a drain: no message is started after it is spent.
	Budget time.Duration
	// MessageTimeout bounds one handler call, so a drain ends at most this
	// long after its budget.
	MessageTimeout time.Duration
}

const batchSize = 5

// Drain works through every queue until it is empty or the budget is spent,
// and reports per queue. It returns an error only when a queue cannot be read
// or a message cannot be archived; handler failures are counted, not
// returned.
func (d *Drainer) Drain(ctx context.Context) (map[string]Counts, error) {
	deadline := time.Now().Add(d.Budget)
	report := make(map[string]Counts, len(d.Queues))
	for _, q := range d.Queues {
		var c Counts
		// The visibility timeout outlasts a drain, so a message failed in
		// this drain is not read again before the next one.
		for time.Now().Before(deadline) {
			msgs, err := d.read(ctx, q)
			if err != nil {
				return report, err
			}
			if len(msgs) == 0 {
				break
			}
			for _, m := range msgs {
				// A message read but not started becomes visible again
				// after its visibility timeout; its read counts as an
				// attempt, which the limit below allows for.
				if !time.Now().Before(deadline) {
					break
				}
				if err := d.handle(ctx, q, m, &c); err != nil {
					return report, err
				}
			}
		}
		report[q.Name] = c
	}
	return report, nil
}

func (d *Drainer) handle(ctx context.Context, q Queue, m Message, c *Counts) error {
	// A message read more often than allowed (an attempt that crashed or
	// timed out before it could be counted) is given up unhandled.
	if m.Attempt > q.MaxAttempts {
		c.GivenUp++
		d.Log.Error("queue message given up", "queue", q.Name, "msg_id", m.ID, "attempt", m.Attempt, "error", "too many reads")
		return d.archive(ctx, q, m)
	}
	hctx, cancel := context.WithTimeout(ctx, d.MessageTimeout)
	err := q.Handle(hctx, m)
	cancel()
	if err == nil {
		c.Done++
		return d.archive(ctx, q, m)
	}
	c.Failed++
	if m.Attempt < q.MaxAttempts {
		d.Log.Warn("queue message failed", "queue", q.Name, "msg_id", m.ID, "attempt", m.Attempt, "error", err)
		return nil
	}
	c.GivenUp++
	d.Log.Error("queue message given up", "queue", q.Name, "msg_id", m.ID, "attempt", m.Attempt, "error", err)
	return d.archive(ctx, q, m)
}

func (d *Drainer) read(ctx context.Context, q Queue) ([]Message, error) {
	rows, err := d.Pool.Query(ctx, `select msg_id, read_ct, message::text from pgmq.read($1, $2, $3)`,
		q.Name, int32(q.Visibility/time.Second), int32(batchSize)) //nolint:gosec // G115: seconds of a fixed timeout
	if err != nil {
		return nil, fmt.Errorf("read %s: %w", q.Name, err)
	}
	defer rows.Close()
	var out []Message
	for rows.Next() {
		var m Message
		var body string
		if err := rows.Scan(&m.ID, &m.Attempt, &body); err != nil {
			return nil, fmt.Errorf("read %s: %w", q.Name, err)
		}
		m.Body = json.RawMessage(body)
		out = append(out, m)
	}
	return out, rows.Err()
}

func (d *Drainer) archive(ctx context.Context, q Queue, m Message) error {
	if _, err := d.Pool.Exec(ctx, `select pgmq.archive($1, $2::bigint)`, q.Name, m.ID); err != nil {
		return fmt.Errorf("archive %s %d: %w", q.Name, m.ID, err)
	}
	return nil
}
