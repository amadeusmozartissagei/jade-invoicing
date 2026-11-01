package handler

import (
	"context"
	"encoding/json"
	"net/http"
	"strconv"
	"time"

	"github.com/amadeusmozartissagei/jade-invoicing/spikes/free-tier/db"
)

const (
	queueName  = "spike"
	visibility = 30 // seconds a read message stays hidden before it can be read again
	batchSize  = 10
)

type drained struct {
	MsgID      int64           `json:"msg_id"`
	ReadCount  int32           `json:"read_ct"`
	EnqueuedAt time.Time       `json:"enqueued_at"`
	LagMs      int64           `json:"lag_ms"`
	Message    json.RawMessage `json:"message"`
}

// Queue sends test messages to pgmq (?action=send&n=3) or reads and
// archives a batch (?action=drain). pg_cron calls the drain action.
func Queue(w http.ResponseWriter, r *http.Request) {
	if !db.Authorized(r) {
		http.Error(w, "unauthorized", http.StatusUnauthorized)
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 10*time.Second)
	defer cancel()
	pool, err := db.Pool(ctx)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	var result any
	switch r.URL.Query().Get("action") {
	case "send":
		n, _ := strconv.Atoi(r.URL.Query().Get("n"))
		if n < 1 || n > 100 {
			n = 1
		}
		ids := make([]int64, 0, n)
		for i := 0; i < n; i++ {
			var id int64
			err := pool.QueryRow(ctx,
				"select pgmq.send($1, jsonb_build_object('seq', $2::int, 'sent_at', now()))",
				queueName, i).Scan(&id)
			if err != nil {
				http.Error(w, err.Error(), http.StatusBadGateway)
				return
			}
			ids = append(ids, id)
		}
		result = map[string]any{"sent": ids}

	case "drain":
		rows, err := pool.Query(ctx,
			"select msg_id, read_ct, enqueued_at, message from pgmq.read($1, $2::int, $3::int)",
			queueName, visibility, batchSize)
		if err != nil {
			http.Error(w, err.Error(), http.StatusBadGateway)
			return
		}
		var msgs []drained
		for rows.Next() {
			var m drained
			if err := rows.Scan(&m.MsgID, &m.ReadCount, &m.EnqueuedAt, &m.Message); err != nil {
				rows.Close()
				http.Error(w, err.Error(), http.StatusBadGateway)
				return
			}
			m.LagMs = time.Since(m.EnqueuedAt).Milliseconds()
			msgs = append(msgs, m)
		}
		rows.Close()
		if err := rows.Err(); err != nil {
			http.Error(w, err.Error(), http.StatusBadGateway)
			return
		}
		// Archive only after the work is done: if this function dies first,
		// the message becomes visible again after the visibility timeout.
		for _, m := range msgs {
			if _, err := pool.Exec(ctx, "select pgmq.archive($1, $2::bigint)", queueName, m.MsgID); err != nil {
				http.Error(w, err.Error(), http.StatusBadGateway)
				return
			}
		}
		result = map[string]any{"drained": msgs}

	default:
		http.Error(w, "action must be send or drain", http.StatusBadRequest)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(result)
}
