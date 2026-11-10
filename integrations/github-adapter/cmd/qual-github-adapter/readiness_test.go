package main

import (
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/orz-i/Qual/integrations/github-adapter/githubadapter"
)

func TestAdapterReadinessIsNotUnconditionalHealth(t *testing.T) {
	root := filepath.Join(t.TempDir(), "spool")
	spool, err := githubadapter.OpenSpool(root)
	if err != nil {
		t.Fatal(err)
	}
	handler, err := githubadapter.NewHandler(
		githubadapter.Repository{ID: 1443998164, FullName: "orz-i/Qual"},
		[]byte(strings.Repeat("s", 40)), spool,
	)
	if err != nil {
		t.Fatal(err)
	}
	mux := serveMux(handler, spool)
	check := func(path string, want int) {
		t.Helper()
		r := httptest.NewRecorder()
		mux.ServeHTTP(r, httptest.NewRequest(http.MethodGet, path, nil))
		if r.Code != want {
			t.Fatalf("%s HTTP%d want %d", path, r.Code, want)
		}
		if r.Header().Get("Cache-Control") != "no-store" {
			t.Fatalf("%s missing no-store", path)
		}
	}
	check("/healthz", 200)
	check("/readyz", 200)
	// On-disk origin loss after a running server starts must stop new
	// readiness claims even though the process is still live.
	if err := os.Remove(filepath.Join(root, "active-source-origin-witness.json")); err != nil {
		t.Fatal(err)
	}
	check("/healthz", 200)
	check("/readyz", 503)
}

func TestInspectFlagsReflectTheDiskAndQueueLimits(t *testing.T) {
	repo := githubadapter.Repository{ID: 1443998164, FullName: "orz-i/Qual"}
	c := githubadapter.CapacityStatus{
		PendingUsed: 0, PendingLimit: 1000,
		DeliveredUsed: 0, DeliveredLimit: 10000,
		DiskIngressBlocked: true, IngressBlocked: true,
	}
	inspect := inspection(repo, map[string]int{"pending": 0}, c, githubadapter.RecoveryStatus{})
	if inspect["ingress_allowed"] != false || inspect["qual_publishing_allowed"] != false {
		t.Fatalf("disk exhaustion was advertised as ready: %+v", inspect)
	}
	c.DiskIngressBlocked, c.IngressBlocked = false, true
	c.PendingUsed = 1000
	inspect = inspection(repo, map[string]int{"pending": 1000}, c, githubadapter.RecoveryStatus{})
	if inspect["ingress_allowed"] != false || inspect["qual_publishing_allowed"] != true {
		t.Fatalf("pending-full should block intake but not draining: %+v", inspect)
	}
	c.PendingUsed, c.IngressBlocked = 0, true
	c.DeliveredUsed = 10000
	inspect = inspection(repo, map[string]int{"delivered": 10000}, c, githubadapter.RecoveryStatus{})
	if inspect["ingress_allowed"] != false || inspect["qual_publishing_allowed"] != false {
		t.Fatalf("delivered-full cannot claim writable spool: %+v", inspect)
	}
	c.DeliveredUsed, c.IngressBlocked = 0, false
	quarantined := githubadapter.RecoveryStatus{Quarantined: true, RequiresSourceReconciliation: true}
	inspect = inspection(repo, map[string]int{}, c, quarantined)
	if inspect["ingress_allowed"] != false || inspect["qual_publishing_allowed"] != false {
		t.Fatalf("restored queue advertised ready: %+v", inspect)
	}
}
