package httpserver

import (
	"context"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/amadeusmozartissagei/jade-invoicing/apps/api/internal/api"
	"github.com/amadeusmozartissagei/jade-invoicing/apps/api/internal/problem"
	"github.com/amadeusmozartissagei/jade-invoicing/apps/api/internal/worker"
)

// PDFs (ADR 0017): issuing enqueues a render, the drain endpoint renders, and
// GET /files hands out presigned download URLs.

// downloadTTL is how long a presigned URL works (ADR 0017, P5).
const downloadTTL = 5 * time.Minute

// Drain settings (ADR 0017, P2 and D4): a failed render is tried again a
// minute later. A drain starts no message after 15 seconds and gives one
// message at most 8, so it answers within 23 seconds: below the server's
// 30-second write timeout and pg_net's 25 seconds.
const (
	drainVisibility = 60 * time.Second
	drainBudget     = 15 * time.Second
	drainPerMessage = 8 * time.Second
)

const renderAttempts = 5

// enqueueRender asks for the document's PDF, in the issue transaction, so a
// failed issue asks for nothing (ADR 0017, D1). The message holds IDs only:
// pgmq's tables have no RLS.
func enqueueRender(ctx context.Context, id uuid.UUID) error {
	t := tenantFrom(ctx)
	if _, err := t.tx.Exec(ctx, `select pgmq.send($1, jsonb_build_object('tenant_id', $2::text, 'invoice_id', $3::text))`,
		worker.RenderQueue, t.tenant.ID.String(), id.String()); err != nil {
		return fmt.Errorf("enqueue render: %w", err)
	}
	return nil
}

func (h *handlers) ListInvoiceFiles(ctx context.Context, req api.ListInvoiceFilesRequestObject) (api.ListInvoiceFilesResponseObject, error) {
	q := queries(ctx)
	if _, err := q.GetInvoice(ctx, req.Id); errors.Is(err, pgx.ErrNoRows) {
		return api.ListInvoiceFiles404ApplicationProblemPlusJSONResponse{
			NotFoundApplicationProblemPlusJSONResponse: api.NotFoundApplicationProblemPlusJSONResponse{Body: invoiceNotFound(ctx)},
		}, nil
	} else if err != nil {
		return nil, fmt.Errorf("read invoice: %w", err)
	}
	rows, err := q.ListInvoiceFiles(ctx, req.Id)
	if err != nil {
		return nil, fmt.Errorf("list files: %w", err)
	}
	out := api.ListInvoiceFiles200JSONResponse{}
	out.Body.Data = make([]api.InvoiceFile, 0, len(rows))
	if len(rows) == 0 {
		return out, nil
	}
	if h.files == nil {
		return nil, errors.New("storage not configured: JADE_S3_* is not set")
	}
	expires := time.Now().Add(downloadTTL).UTC().Truncate(time.Second)
	for _, f := range rows {
		u, err := h.files.PresignGet(ctx, f.StoragePath, downloadTTL)
		if err != nil {
			return nil, err
		}
		out.Body.Data = append(out.Body.Data, api.InvoiceFile{
			Kind: api.InvoiceFileKind(f.Kind), ContentType: f.ContentType, ByteSize: f.ByteSize,
			Sha256: hex.EncodeToString(f.Sha256), Url: u, UrlExpiresAt: expires,
		})
	}
	return out, nil
}

// drainHandler is POST /internal/queue/drain, which pg_cron calls through
// pg_net (ADR 0017, P7 and D4). It is not part of the public contract.
func drainHandler(cfg Config) http.Handler {
	log, pool, token := cfg.Logger, cfg.DB, cfg.DrainToken
	want := sha256.Sum256([]byte(token))
	// A queue whose handler cannot work is not read at all, so its messages
	// wait without using up attempts (ADR 0017, D10).
	var queues []worker.Queue
	if cfg.Renderer != nil && cfg.Files != nil {
		queues = append(queues, worker.Queue{
			Name: worker.RenderQueue, Handle: worker.RenderPDF(pool, log, cfg.Renderer, cfg.Files),
			MaxAttempts: renderAttempts, Visibility: drainVisibility,
		})
	}
	return http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		id := RequestID(req.Context())
		if !drainAuthorized(req.Header.Get("Authorization"), token, want) {
			unauthorized(w, id)
			return
		}
		if pool == nil {
			log.Error("drain", "request_id", id, "error", errDBNotConfigured)
			problem.Write(w, problem.New(problem.Internal, id, ""))
			return
		}
		d := worker.Drainer{Pool: pool, Log: log, Budget: drainBudget, MessageTimeout: drainPerMessage, Queues: queues}
		report, err := d.Drain(req.Context())
		if err != nil {
			log.Error("drain", "request_id", id, "error", err)
			problem.Write(w, problem.New(problem.Internal, id, ""))
			return
		}
		w.Header().Set("Content-Type", "application/json")
		w.Header().Set("Cache-Control", "no-store")
		_ = json.NewEncoder(w).Encode(map[string]any{"queues": report})
	})
}

// drainAuthorized compares hashes in constant time, so neither the token's
// content nor its length leaks through timing. An unset token admits no one.
func drainAuthorized(header, token string, want [32]byte) bool {
	if token == "" {
		return false
	}
	scheme, got, ok := strings.Cut(header, " ")
	if !ok || !strings.EqualFold(scheme, "Bearer") {
		return false
	}
	sum := sha256.Sum256([]byte(got))
	return subtle.ConstantTimeCompare(sum[:], want[:]) == 1
}
