// emrd is the nz-open-emr walking-skeleton server: one binary, one
// Postgres, everything else embedded.
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

	"github.com/sekickivuk-hue/nz-open-emr/internal/api"
	"github.com/sekickivuk-hue/nz-open-emr/internal/audit"
	"github.com/sekickivuk-hue/nz-open-emr/internal/db"
	"github.com/sekickivuk-hue/nz-open-emr/internal/projection"

	// Module init() registrations — add one import per new module.
	_ "github.com/sekickivuk-hue/nz-open-emr/module/allergies"
	_ "github.com/sekickivuk-hue/nz-open-emr/module/careteam"
	_ "github.com/sekickivuk-hue/nz-open-emr/module/encounters"
	_ "github.com/sekickivuk-hue/nz-open-emr/module/family"
	_ "github.com/sekickivuk-hue/nz-open-emr/module/problems"
)

func main() {
	log := slog.New(slog.NewTextHandler(os.Stderr, nil))
	if err := run(log); err != nil {
		log.Error("fatal", "err", err)
		os.Exit(1)
	}
}

func run(log *slog.Logger) error {
	url := os.Getenv("DATABASE_URL")
	if url == "" {
		return errors.New("DATABASE_URL is required")
	}
	addr := os.Getenv("LISTEN_ADDR")
	if addr == "" {
		addr = ":8080"
	}

	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	pool, err := db.Connect(ctx, url)
	if err != nil {
		return err
	}
	defer pool.Close()
	if err := db.Migrate(ctx, pool); err != nil {
		return err
	}

	// Refuse to serve over a corrupted audit chain.
	rep, err := audit.Verify(ctx, pool)
	if err != nil {
		return err
	}
	if !rep.OK {
		log.Error("audit chain verification FAILED — refusing to start",
			"brokenSeq", rep.BrokenSeq, "reason", rep.Reason)
		return errors.New("audit chain broken")
	}
	log.Info("audit chain verified", "events", rep.Checked)

	proj := &projection.Projector{Pool: pool, Log: log}
	go proj.Run(ctx)

	srv := &http.Server{Addr: addr, Handler: api.New(pool, proj)}
	go func() {
		<-ctx.Done()
		shutCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		srv.Shutdown(shutCtx)
	}()
	log.Info("emrd listening", "addr", addr)
	if err := srv.ListenAndServe(); !errors.Is(err, http.ErrServerClosed) {
		return err
	}
	return nil
}
