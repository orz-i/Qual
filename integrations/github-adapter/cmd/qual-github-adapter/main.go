// qual-github-adapter is an EXTERNAL source verifier and durable delivery
// connector, not a Qual Agent executor or an embedded Qual webhook endpoint.
package main

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"net"
	"net/http"
	"os"
	"os/signal"
	"strconv"
	"syscall"
	"time"

	"github.com/orz-i/Qual/integrations/github-adapter/githubadapter"
)

func getenv(key, fallback string) string {
	if x := os.Getenv(key); x != "" {
		return x
	}
	return fallback
}
func config() (githubadapter.Repository, *githubadapter.Spool, error) {
	id, err := strconv.ParseInt(os.Getenv("QUAL_GITHUB_REPO_ID"), 10, 64)
	if err != nil || id <= 0 {
		return githubadapter.Repository{}, nil, githubadapter.ErrInvalid
	}
	repo := githubadapter.Repository{ID: id, FullName: os.Getenv("QUAL_GITHUB_REPOSITORY")}
	if repo.FullName == "" {
		return githubadapter.Repository{}, nil, githubadapter.ErrInvalid
	}
	if v := os.Getenv("QUAL_GITHUB_INSTALLATION_ID"); v != "" {
		installID, err := strconv.ParseInt(v, 10, 64)
		if err != nil || installID <= 0 {
			return githubadapter.Repository{}, nil, githubadapter.ErrInvalid
		}
		repo.InstallID = installID
	}
	spool, err := githubadapter.OpenSpool(os.Getenv("QUAL_GITHUB_SPOOL_DIR"))
	return repo, spool, err
}
func output(data any) {
	bytes, err := json.Marshal(data)
	if err != nil {
		log.Fatal("failed to encode sanitized result")
	}
	fmt.Println(string(bytes))
}
func run() error {
	if len(os.Args) < 2 {
		return fmt.Errorf("usage: qual-github-adapter serve|once|inspect|requeue|archive|backup --out <private.zip> --approve|restore --file <private.zip> --into <new-dir> --approve")
	}
	command := os.Args[1]
	if command == "requeue" {
		if len(os.Args) != 6 || os.Args[3] != "--approve" || os.Args[4] != "--reason-code" {
			return fmt.Errorf("manual requeue requires delivery ID, --approve and --reason-code")
		}
	} else if command == "archive" {
		if len(os.Args) != 4 || os.Args[3] != "--approve" {
			return fmt.Errorf("archiving requires a bounded count and explicit --approve")
		}
	} else if command == "backup" {
		if len(os.Args) != 5 || os.Args[2] != "--out" || os.Args[4] != "--approve" {
			return fmt.Errorf("signed backup requires --out <new-private-zip> --approve")
		}
	} else if command == "restore" {
		if len(os.Args) != 7 || os.Args[2] != "--file" ||
			os.Args[4] != "--into" || os.Args[6] != "--approve" {
			return fmt.Errorf("isolated restore requires --file <signed-zip> --into <new-dir> --approve")
		}
		_, stats, err := githubadapter.RestoreSignedBackup(os.Args[3], os.Args[5],
			[]byte(os.Getenv("QUAL_GITHUB_BACKUP_HMAC_KEY")))
		if err != nil {
			return fmt.Errorf("isolated signed restore refused; check Operator key and owner-only archives")
		}
		output(map[string]any{"isolated_restore_verified": true,
			"record_count": stats.RecordCount, "archive_sha256": stats.ArchiveSHA256})
		return nil
	} else if len(os.Args) != 2 || (command != "serve" && command != "once" && command != "inspect") {
		return fmt.Errorf("unknown command or unexpected argument")
	}
	repo, spool, err := config()
	if err != nil {
		return fmt.Errorf("adapter repository/spool preflight failed")
	}
	switch command {
	case "backup":
		stats, err := spool.CreateSignedBackup(os.Args[3], []byte(os.Getenv("QUAL_GITHUB_BACKUP_HMAC_KEY")))
		if err != nil {
			return fmt.Errorf("signed snapshot refused; inspect owner-only source and destination")
		}
		output(map[string]any{"signed_backup_ready": true,
			"record_count": stats.RecordCount, "archive_sha256": stats.ArchiveSHA256})
	case "archive":
		limit, err := strconv.Atoi(os.Args[2])
		if err != nil || limit < 1 || limit > 500 {
			return fmt.Errorf("archive count must be 1..500")
		}
		archived, err := spool.ArchiveDelivered(limit)
		if err != nil {
			return fmt.Errorf("archive refused; inspect owner-only spool and restore index integrity")
		}
		output(archived)
	case "requeue":
		if err := spool.RequeueAfterReview(os.Args[2], os.Args[5]); err != nil {
			return fmt.Errorf("delivery not approved for retry; inspect the durable spool")
		}
		output(map[string]any{"requeued_after_operator_review": true, "delivery_id": os.Args[2],
			"reason_code": os.Args[5], "raw_payload_retained": false})
	case "inspect":
		counts, err := spool.Counts()
		if err != nil {
			return fmt.Errorf("spool status unreadable")
		}
		capacity, err := spool.Capacity()
		if err != nil {
			return fmt.Errorf("spool capacity unreadable")
		}
		output(map[string]any{"repository_id": repo.ID, "repository": repo.FullName,
			"states": counts, "capacity": capacity})
	case "once":
		publisher := githubadapter.Publisher{
			Spool: spool, Repository: repo, MapPath: os.Getenv("QUAL_GITHUB_MAPPING_FILE"),
			QualURL: getenv("QUAL_API_URL", "http://127.0.0.1:18080"),
			Token:   os.Getenv("QUAL_ADAPTER_TOKEN"),
		}
		ctx, cancel := context.WithTimeout(context.Background(), 45*time.Second)
		defer cancel()
		stats, err := publisher.PublishOnce(ctx)
		if err != nil {
			return fmt.Errorf("Qual delivery failed closed; inspect mapping/credentials")
		}
		output(stats)
	case "serve":
		secret := []byte(os.Getenv("QUAL_GITHUB_WEBHOOK_SECRET"))
		handler, err := githubadapter.NewHandler(repo, secret, spool)
		if err != nil {
			return fmt.Errorf("GitHub HMAC secret missing or too short")
		}
		hostport := getenv("QUAL_GITHUB_LISTEN_ADDR", "127.0.0.1:18717")
		host, port, err := net.SplitHostPort(hostport)
		if err != nil || host != "127.0.0.1" {
			return fmt.Errorf("only loopback webhook listener is permitted")
		}
		n, err := strconv.Atoi(port)
		if err != nil || n < 1024 || n > 65535 {
			return fmt.Errorf("invalid webhook listen port")
		}
		mux := http.NewServeMux()
		mux.Handle("POST /github/webhook", handler)
		mux.HandleFunc("GET /healthz", func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Cache-Control", "no-store")
			w.WriteHeader(200)
			_, _ = w.Write([]byte("ok"))
		})
		server := &http.Server{
			Addr: hostport, Handler: mux, ReadHeaderTimeout: 4 * time.Second,
			ReadTimeout: 6 * time.Second, WriteTimeout: 8 * time.Second,
			IdleTimeout: 12 * time.Second, MaxHeaderBytes: 8192,
		}
		ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
		defer stop()
		go func() {
			<-ctx.Done()
			shutdown, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			_ = server.Shutdown(shutdown)
		}()
		log.Print("Qual GitHub adapter listening on pinned loopback address; no Qual permission granted")
		if err := server.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			return fmt.Errorf("loopback GitHub webhook adapter failed to listen")
		}
	}
	return nil
}
func main() {
	if err := run(); err != nil {
		// Deliberately exclude arbitrary system errors, URLs and any credential.
		fmt.Fprintln(os.Stderr, "QUAL_GITHUB_ADAPTER_REFUSED:", err)
		os.Exit(2)
	}
}
