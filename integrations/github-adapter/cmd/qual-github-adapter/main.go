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
func config(initializingNewSource bool) (githubadapter.Repository, *githubadapter.Spool, error) {
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
	// Never silently seed a new empty source during inspect, serve, once or
	// maintenance: a missing spool could have contained old replay tombstones.
	root := os.Getenv("QUAL_GITHUB_SPOOL_DIR")
	if initializingNewSource {
		spool, err := githubadapter.InitNewSpool(root)
		return repo, spool, err
	}
	spool, err := githubadapter.OpenExistingSpool(root)
	return repo, spool, err
}

// inspection distinguishes new intake capacity from the ability to drain
// existing Pending. A full Pending queue can still be published; disk reserve,
// full Delivered, and restored quarantine block durable delivery changes.
// These flags describe local spool gates, not GitHub/Qual credentials or TLS.
func inspection(repo githubadapter.Repository, states map[string]int,
	capacity githubadapter.CapacityStatus, recovery githubadapter.RecoveryStatus) map[string]any {
	return map[string]any{
		"repository_id": repo.ID, "repository": repo.FullName,
		"states": states, "capacity": capacity, "recovery": recovery,
		"ingress_allowed": !capacity.IngressBlocked && !recovery.Quarantined,
		"qual_publishing_allowed": !recovery.Quarantined && !capacity.DiskIngressBlocked &&
			capacity.DeliveredUsed < capacity.DeliveredLimit,
	}
}

// healthz is process liveness. readyz checks the current on-disk intake gate
// and must report 503 when capacity or origin integrity is unsafe.
func serveMux(handler http.Handler, spool *githubadapter.Spool) *http.ServeMux {
	mux := http.NewServeMux()
	mux.Handle("POST /github/webhook", handler)
	mux.HandleFunc("GET /healthz", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Cache-Control", "no-store")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("ok"))
	})
	mux.HandleFunc("GET /readyz", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Cache-Control", "no-store")
		if err := spool.IngressReadiness(); err != nil {
			http.Error(w, "durable ingress unavailable", http.StatusServiceUnavailable)
			return
		}
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("ready"))
	})
	return mux
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
		return fmt.Errorf("usage: qual-github-adapter init --approve|serve|once|inspect|requeue|archive|backup --out <private.zip> --approve|restore --file <private.zip> --into <new-dir> --approve|reconcile-audit --keys <pinned-public-keys.json> --evidence <signed-ledgers.json>")
	}
	command := os.Args[1]
	if command == "init" {
		if len(os.Args) != 3 || os.Args[2] != "--approve" {
			return fmt.Errorf("new source initialization requires init --approve; never use for a restored or lost spool")
		}
	} else if command == "reconcile-audit" {
		if len(os.Args) != 6 || os.Args[2] != "--keys" || os.Args[4] != "--evidence" {
			return fmt.Errorf("read-only reconciliation requires --keys <pinned-public-keys.json> --evidence <signed-ledgers.json>")
		}
	} else if command == "requeue" {
		if len(os.Args) != 6 || os.Args[3] != "--approve" || os.Args[4] != "--reason-code" {
			return fmt.Errorf("manual requeue requires delivery ID, --approve and --reason-code")
		}
	} else if command == "archive" {
		if len(os.Args) != 4 || os.Args[3] != "--approve" {
			return fmt.Errorf("archiving requires a bounded count and explicit --approve")
		}
	} else if command == "backup" {
		if (len(os.Args) != 5 && len(os.Args) != 6) ||
			os.Args[2] != "--out" || os.Args[4] != "--approve" ||
			(len(os.Args) == 6 && os.Args[5] != "--require-separate-volume") {
			return fmt.Errorf("signed backup requires --out <new-private-zip> --approve [--require-separate-volume]")
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
		// A valid HMAC proves bytes, NOT when the snapshot was captured.
		// Never represent isolated restore as approved production cutover.
		output(map[string]any{"isolated_restore_verified": true,
			"record_count": stats.RecordCount, "archive_sha256": stats.ArchiveSHA256,
			"snapshot_freshness_verified":    false,
			"requires_source_reconciliation": true,
			"recovery_quarantined":           true,
			"ingress_allowed":                false,
			"qual_publishing_allowed":        false})
		return nil
	} else if len(os.Args) != 2 || (command != "serve" && command != "once" && command != "inspect") {
		return fmt.Errorf("unknown command or unexpected argument")
	}
	repo, spool, err := config(command == "init")
	if err != nil {
		return fmt.Errorf("adapter repository/spool preflight failed")
	}
	switch command {
	case "reconcile-audit":
		return runReconciliationAudit(repo, spool, os.Args[3], os.Args[5])
	case "init":
		output(map[string]any{"new_source_initialized": true, "repository_id": repo.ID,
			"repository": repo.FullName, "previous_delivery_history_restored": false,
			"source_reconciliation_verified": false})
	case "backup":
		// A backup on the same device cannot satisfy the independent-volume
		// protection requirement, no matter how valid its detached HMAC is.
		separate, err := backupVolumeSeparation(spool.Root, os.Args[3])
		if err != nil {
			return fmt.Errorf("signed snapshot destination failed filesystem identity verification")
		}
		if len(os.Args) == 6 && !separate {
			return errSeparateBackupVolumeRequired
		}
		stats, err := spool.CreateSignedBackup(os.Args[3], []byte(os.Getenv("QUAL_GITHUB_BACKUP_HMAC_KEY")))
		if err != nil {
			return fmt.Errorf("signed snapshot refused; inspect owner-only source and destination")
		}
		verified, err := backupVolumeSeparation(spool.Root, os.Args[3])
		if err != nil || verified != separate {
			return fmt.Errorf("backup storage identity changed during snapshot; do not trust separation")
		}
		output(map[string]any{"signed_backup_ready": true,
			"record_count": stats.RecordCount, "archive_sha256": stats.ArchiveSHA256,
			"separate_volume_verified": verified, "off_host_verified": false,
			"encrypted": false})
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
		recovery, err := spool.RecoveryStatus()
		if err != nil {
			return fmt.Errorf("recovery quarantine unreadable")
		}
		output(inspection(repo, counts, capacity, recovery))
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
			return fmt.Errorf("Qual delivery failed closed; inspect credentials, mapping and recovery quarantine")
		}
		output(stats)
	case "serve":
		secret := []byte(os.Getenv("QUAL_GITHUB_WEBHOOK_SECRET"))
		handler, err := githubadapter.NewHandler(repo, secret, spool)
		if err != nil {
			return fmt.Errorf("GitHub listener unavailable: HMAC configuration or unreconciled recovered spool")
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
		server := &http.Server{
			Addr: hostport, Handler: serveMux(handler, spool), ReadHeaderTimeout: 4 * time.Second,
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
