// Command api runs the jade-invoicing HTTP API. On Vercel it is started by the
// Go framework preset, which sets PORT (ADR 0004).
package main

import (
	"context"
	"errors"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"
	_ "time/tzdata" // tenant time zones decide dates; the runtime may lack zoneinfo

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/amadeusmozartissagei/jade-invoicing/apps/api/internal/db"
	"github.com/amadeusmozartissagei/jade-invoicing/apps/api/internal/httpserver"
	"github.com/amadeusmozartissagei/jade-invoicing/apps/api/internal/invoicepdf"
	"github.com/amadeusmozartissagei/jade-invoicing/apps/api/internal/objstore"
)

func main() {
	log := slog.New(slog.NewJSONHandler(os.Stdout, nil))
	if err := run(log); err != nil {
		log.Error("server stopped", "error", err)
		os.Exit(1)
	}
}

func run(log *slog.Logger) error {
	port := os.Getenv("PORT")
	if port == "" {
		port = "8080" // the local server in openapi.yaml
	}

	// The API's own role, jade_api, through the transaction pooler. Never
	// logged: the URL holds the password.
	var pool *pgxpool.Pool
	if url := os.Getenv("JADE_API_DATABASE_URL"); url != "" {
		var err error
		if pool, err = db.New(url); err != nil {
			// pgx redacts passwords from a URL it cannot parse only on a
			// best-effort basis, so its message is not logged.
			return errors.New("JADE_API_DATABASE_URL is not a valid database URL")
		}
		defer pool.Close()
	} else {
		log.Warn("JADE_API_DATABASE_URL is not set; /v1 endpoints will answer 500")
	}

	renderer, files, drainToken := pdfConfig(log)
	if renderer != nil {
		defer func() { _ = renderer.Close() }()
	}

	srv := &http.Server{
		Addr: ":" + port,
		Handler: httpserver.New(httpserver.Config{
			Logger:     log,
			Commit:     os.Getenv("VERCEL_GIT_COMMIT_SHA"),
			DB:         pool,
			Renderer:   renderer,
			Files:      files,
			DrainToken: drainToken,
		}),
		ReadHeaderTimeout: 5 * time.Second,
		ReadTimeout:       15 * time.Second,
		WriteTimeout:      30 * time.Second,
		IdleTimeout:       60 * time.Second,
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	errc := make(chan error, 1)
	go func() {
		log.Info("listening", "addr", srv.Addr)
		errc <- srv.ListenAndServe()
	}()

	select {
	case err := <-errc:
		return err
	case <-ctx.Done():
	}

	shutdownCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if err := srv.Shutdown(shutdownCtx); err != nil {
		return err
	}
	if err := <-errc; !errors.Is(err, http.ErrServerClosed) {
		return err
	}
	return nil
}

// pdfConfig reads the settings for PDFs (ADR 0017, D10). Each missing part
// is logged once and leaves its feature off; none stops the server, so
// /healthz and the rest of /v1 keep working.
func pdfConfig(log *slog.Logger) (*invoicepdf.Renderer, *objstore.Store, string) {
	// With the typst_embedded build tag (Vercel), an empty path means the
	// embedded binary; without it, JADE_TYPST_BIN must name one.
	renderer, err := invoicepdf.New(invoicepdf.Options{TypstPath: os.Getenv("JADE_TYPST_BIN")})
	if err != nil {
		log.Warn("PDF rendering is off", "error", err)
	}

	var files *objstore.Store
	if os.Getenv("JADE_S3_ENDPOINT") != "" {
		files, err = objstore.New(objstore.Config{
			Endpoint:        os.Getenv("JADE_S3_ENDPOINT"),
			Region:          os.Getenv("JADE_S3_REGION"),
			AccessKeyID:     os.Getenv("JADE_S3_ACCESS_KEY_ID"),
			SecretAccessKey: os.Getenv("JADE_S3_SECRET_ACCESS_KEY"),
			Bucket:          objstore.InvoiceFilesBucket,
		})
		if err != nil {
			log.Warn("PDF storage is off", "error", err)
		}
	} else {
		log.Warn("JADE_S3_ENDPOINT is not set; PDFs are not stored")
	}

	token := os.Getenv("JADE_DRAIN_TOKEN")
	if token == "" {
		log.Warn("JADE_DRAIN_TOKEN is not set; /internal/queue/drain answers 401")
	}
	return renderer, files, token
}
