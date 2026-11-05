package main

import (
	"context"
	"fmt"
	"log"
	"log/slog"
	"os"
	"os/signal"
	"strconv"
	"syscall"
	"time"

	"github.com/joho/godotenv"

	"github.com/vrannang1/spendmux/internal/actor"
	"github.com/vrannang1/spendmux/internal/config"
	"github.com/vrannang1/spendmux/internal/core"
	appcrypto "github.com/vrannang1/spendmux/internal/crypto"
	"github.com/vrannang1/spendmux/internal/database"
	"github.com/vrannang1/spendmux/internal/maintenance"
	"github.com/vrannang1/spendmux/internal/router"
	"github.com/vrannang1/spendmux/internal/supervision"
	"github.com/vrannang1/spendmux/internal/telemetry"
	"github.com/vrannang1/spendmux/internal/upstream"
)

func main() {
	if err := godotenv.Load("/etc/spendmux/.env"); err != nil {
		_ = godotenv.Load()
	}
	slog.SetDefault(slog.New(slog.NewJSONHandler(os.Stdout, nil)))

	// Production refuses to start on weak, missing or placeholder secrets.
	if err := config.ValidateProduction(); err != nil {
		log.Fatalf("invalid production configuration: %v", err)
	}
	upstream.ConfigureFromEnv()
	stopTracing := telemetry.Init(context.Background())
	if _, err := appcrypto.VaultKey(); err != nil { // fail fast: sealed keys need it on every request
		log.Fatalf("vault key: %v", err)
	}

	db := database.Get()
	if err := database.Migrate(db); err != nil {
		log.Fatalf("database migration failed: %v", err)
	}
	slog.Info("database ready")

	core.StartCleanup()
	core.StartSessionCleanup()
	stopMaintenance := maintenance.Start()

	// Supervised DataActor: usage-event writes are crash-isolated from HTTP.
	supervision.Start()
	actor.SetDefaultGateway(supervision.Gateway())

	app := router.Setup()

	port := os.Getenv("PORT")
	if port == "" {
		port = "8080"
	}
	go func() {
		slog.Info("server listening", "port", port, "base_url", os.Getenv("BASE_URL"))
		if err := app.Listen(fmt.Sprintf(":%s", port)); err != nil {
			log.Printf("fiber stopped: %v", err)
		}
	}()

	quit := make(chan os.Signal, 1)
	signal.Notify(quit, syscall.SIGINT, syscall.SIGTERM)
	<-quit

	// Shut down in an order that loses nothing: go not-ready so the load
	// balancer stops routing here, let in-flight requests and streams finish
	// (their usage rows are written as they end), stop background jobs, and
	// only then close the database.
	readyDelay := envSeconds("SHUTDOWN_READY_DELAY_SECONDS", 5)
	drain := envSeconds("SHUTDOWN_DRAIN_SECONDS", 120)
	slog.Info("shutting down", "ready_delay", readyDelay.String(), "drain", drain.String())
	router.BeginDrain()
	time.Sleep(readyDelay)
	if err := app.ShutdownWithTimeout(drain); err != nil {
		log.Printf("HTTP drain incomplete after %s: %v", drain, err)
	}
	stopMaintenance()
	supervision.Stop()
	flushCtx, cancelFlush := context.WithTimeout(context.Background(), 5*time.Second)
	if err := stopTracing(flushCtx); err != nil {
		log.Printf("trace flush failed: %v", err)
	}
	cancelFlush()
	if err := database.Close(); err != nil {
		log.Printf("database close failed: %v", err)
	}
}

// envSeconds reads a non-negative duration in seconds from the environment.
func envSeconds(name string, def int) time.Duration {
	if n, err := strconv.Atoi(os.Getenv(name)); err == nil && n >= 0 {
		return time.Duration(n) * time.Second
	}
	return time.Duration(def) * time.Second
}
