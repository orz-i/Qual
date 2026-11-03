// qual-ops performs explicit local operator-only disaster-recovery drills.
// It NEVER restores into an existing database or invokes agents, tests or CI.
package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"os"
	"os/signal"
	"syscall"
	"time"

	"qual/internal/operations/backup"
)

func main() {
	if err := run(os.Args[1:]); err != nil {
		// Do not print DSNs, file content, S3 keys or secrets in error logs.
		fmt.Fprintln(os.Stderr, "qual-ops:", err)
		os.Exit(1)
	}
}
func run(args []string) error {
	if len(args) == 0 {
		return errors.New("usage: qual-ops backup -out DIR | verify -path DIR | restore-drill -path DIR")
	}
	fs := flag.NewFlagSet("qual-ops "+args[0], flag.ContinueOnError)
	out := fs.String("out", "", "new private backup directory")
	path := fs.String("path", "", "existing signed backup directory")
	if err := fs.Parse(args[1:]); err != nil {
		return err
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	ctx, cancel := context.WithTimeout(ctx, 5*time.Minute)
	defer cancel()
	switch args[0] {
	case "verify":
		if *path == "" {
			return errors.New("-path backup directory required")
		}
		m, err := backup.Verify(*path)
		if err != nil {
			return err
		}
		return json.NewEncoder(os.Stdout).Encode(map[string]any{"verified": true,
			"database": m.SourceDatabase, "table_count": len(m.Tables), "object_count": len(m.Objects),
			"database_size_bytes": m.DatabaseSize})
	case "backup", "restore-drill":
		dsn := os.Getenv("DATABASE_URL")
		if dsn == "" {
			return errors.New("DATABASE_URL loopback test/development database required")
		}
		service, err := backup.New(ctx, dsn)
		if err != nil {
			return err
		}
		defer service.Close()
		if args[0] == "backup" {
			if *out == "" {
				return errors.New("-out new private backup directory required")
			}
			m, err := service.Backup(ctx, *out)
			if err != nil {
				return err
			}
			return json.NewEncoder(os.Stdout).Encode(map[string]any{
				"verified": true, "database": m.SourceDatabase, "table_count": len(m.Tables),
				"object_count": len(m.Objects), "database_size_bytes": m.DatabaseSize, "backup_path": *out,
			})
		}
		if *path == "" {
			return errors.New("-path trusted signed backup directory required")
		}
		result, err := service.RestoreDrill(ctx, *path)
		if err != nil {
			return err
		}
		return json.NewEncoder(os.Stdout).Encode(result)
	default:
		return errors.New("unknown qual-ops command")
	}
}
