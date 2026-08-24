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

	"github.com/11DingKing/youth-venue-slot-ledger/internal/audit"
	"github.com/11DingKing/youth-venue-slot-ledger/internal/auth"
	"github.com/11DingKing/youth-venue-slot-ledger/internal/booking"
	"github.com/11DingKing/youth-venue-slot-ledger/internal/checkin"
	"github.com/11DingKing/youth-venue-slot-ledger/internal/closure"
	"github.com/11DingKing/youth-venue-slot-ledger/internal/config"
	"github.com/11DingKing/youth-venue-slot-ledger/internal/database"
	"github.com/11DingKing/youth-venue-slot-ledger/internal/httpapi"
	"github.com/11DingKing/youth-venue-slot-ledger/internal/identity"
	"github.com/11DingKing/youth-venue-slot-ledger/internal/repository"
	"github.com/11DingKing/youth-venue-slot-ledger/internal/schedule"
	"github.com/11DingKing/youth-venue-slot-ledger/internal/worker"
)

func main() {
	logger := slog.New(slog.NewJSONHandler(os.Stdout, &slog.HandlerOptions{Level: slog.LevelInfo}))
	if err := run(logger); err != nil {
		logger.Error("server stopped", "error", err)
		os.Exit(1)
	}
}

func run(logger *slog.Logger) error {
	cfg, err := config.Load()
	if err != nil {
		return err
	}
	rootCtx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	db, err := database.Open(rootCtx, cfg.DBPath)
	if err != nil {
		return err
	}
	defer db.Close()

	store := repository.New(db)
	authService := auth.New(store, cfg.SessionTTL, time.Now)
	if err := authService.EnsureBootstrapOperator(rootCtx, cfg.BootstrapOperatorEmail, cfg.BootstrapOperatorPassword); err != nil {
		return err
	}
	bookingService := booking.New(store, time.Now, 15*time.Minute)
	closureService := closure.New(store, time.Now, cfg.WorkerMaxAttempts)
	workerRunner := worker.New(store, logger, cfg.WorkerPollInterval, cfg.WorkerLease, time.Now)
	closureHandlers := worker.NewClosureHandlers(store, bookingService)
	maintenance := worker.NewSessionMaintenance(authService)
	if err := workerRunner.Register("closure_compensation", closureHandlers.Compensation); err != nil {
		return err
	}
	if err := workerRunner.Register("session_maintenance", maintenance.RevokeExpired); err != nil {
		return err
	}
	if err := worker.EnqueueSessionMaintenance(rootCtx, store, time.Now().UTC(), cfg.WorkerMaxAttempts); err != nil {
		return err
	}
	go func() {
		if err := workerRunner.Run(rootCtx); err != nil && !errors.Is(err, context.Canceled) {
			logger.Error("worker stopped", "error", err)
			stop()
		}
	}()

	handler := httpapi.New(httpapi.Dependencies{
		DB: db, Logger: logger, Auth: authService,
		Identity: identity.New(store, time.Now), Schedule: schedule.New(store, time.Now),
		Bookings: bookingService, CheckIn: checkin.New(store, time.Now), Closures: closureService,
		Audit: audit.New(store),
	})
	httpServer := &http.Server{
		Addr: cfg.HTTPAddr, Handler: handler, ReadHeaderTimeout: 5 * time.Second,
		ReadTimeout: 15 * time.Second, WriteTimeout: 30 * time.Second, IdleTimeout: 60 * time.Second,
	}
	serverErrors := make(chan error, 1)
	go func() {
		logger.Info("server listening", "address", cfg.HTTPAddr, "database", cfg.DBPath)
		serverErrors <- httpServer.ListenAndServe()
	}()

	select {
	case <-rootCtx.Done():
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		if err := httpServer.Shutdown(shutdownCtx); err != nil {
			return err
		}
		return nil
	case err := <-serverErrors:
		if errors.Is(err, http.ErrServerClosed) {
			return nil
		}
		return err
	}
}
