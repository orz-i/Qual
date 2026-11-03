// qual-maintenance operates durable evidence cleanup and Outbox delivery.
// It never starts Agents, tests, CI or model calls; explicit operator invocation only.
package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	evidencepg "qual/internal/evidence/infrastructure/postgres"
	s3store "qual/internal/evidence/infrastructure/s3store"
	"qual/internal/integration/webhooks"
)

func main() {
	if err := run(os.Args[1:]); err != nil {
		fmt.Fprintln(os.Stderr, "qual-maintenance:", err)
		os.Exit(1)
	}
}
func run(args []string) error {
	if len(args) == 0 {
		return errors.New("usage: qual-maintenance deliver-once|deliver-loop|cleanup-artifacts [-hours 24]")
	}
	dburl := os.Getenv("DATABASE_URL")
	if dburl == "" {
		return errors.New("DATABASE_URL required")
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	pool, err := pgxpool.New(ctx, dburl)
	if err != nil {
		return errors.New("database unavailable")
	}
	defer pool.Close()
	if err := pool.Ping(ctx); err != nil {
		return errors.New("database unavailable")
	}
	switch args[0] {
	case "cleanup-artifacts":
		fs := flag.NewFlagSet("cleanup-artifacts", flag.ContinueOnError)
		hours := fs.Int("hours", 24, "expired pending age in hours (1..720)")
		if err := fs.Parse(args[1:]); err != nil {
			return err
		}
		if *hours < 1 || *hours > 720 {
			return errors.New("hours must be 1..720")
		}
		store, err := s3store.NewFromEnv(ctx)
		if err != nil || store == nil {
			return errors.New("S3 store not configured")
		}
		count, err := (evidencepg.Repository{Pool: pool, Objects: store}).CleanupStale(ctx, store, time.Duration(*hours)*time.Hour)
		if err != nil {
			return err
		}
		fmt.Println("expired pending artifacts cleaned:", count)
		return nil
	case "deliver-once", "deliver-loop":
		cfg, err := webhooks.FromEnv()
		if err != nil {
			return err
		}
		if !cfg.HasKey {
			return errors.New("QUAL_WEBHOOK_ENCRYPTION_KEY required")
		}
		manager := webhooks.Manager{Pool: pool, Config: cfg}
		if args[0] == "deliver-once" {
			processed, err := manager.ProcessOne(ctx)
			if err != nil {
				return err
			}
			if processed {
				fmt.Println("one webhook delivery processed")
			} else {
				fmt.Println("no due webhook deliveries")
			}
			return nil
		}
		ticker := time.NewTicker(time.Second)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return nil
			default:
			}
			processed, err := manager.ProcessOne(ctx)
			if err != nil {
				fmt.Fprintln(os.Stderr, "webhook delivery cycle failed (no secrets):", err)
			}
			if processed && err == nil {
				continue
			}
			select {
			case <-ctx.Done():
				return nil
			case <-ticker.C:
			}
		}
	default:
		return errors.New("unknown maintenance command")
	}
}
