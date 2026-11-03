//go:build integration

package integration

import (
	"bytes"
	"context"
	"crypto/hmac"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	webhooks "qual/internal/integration/webhooks"
)

func TestWebhookHMACRetryAndLeastPrivilege(t *testing.T) {
	f := setup(t)
	ctx := context.Background()
	var mu sync.Mutex
	requests := make([]struct{ EventID, DeliveryID string }, 0)
	var secret string
	receiver := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		raw, err := io.ReadAll(io.LimitReader(r.Body, 65536))
		if err != nil {
			t.Error(err)
			return
		}
		stamp := r.Header.Get("X-Qual-Timestamp")
		mu.Lock()
		key := secret
		mu.Unlock()
		expected := webhooks.SignedPayload(key, stamp, raw)
		if !hmac.Equal([]byte(expected), []byte(r.Header.Get("X-Qual-Signature"))) {
			t.Errorf("invalid HMAC signature %s", r.Header.Get("X-Qual-Signature"))
			w.WriteHeader(401)
			return
		}
		var event webhooks.OutgoingEvent
		if err := json.Unmarshal(raw, &event); err != nil {
			t.Error(err)
			w.WriteHeader(400)
			return
		}
		mu.Lock()
		requests = append(requests, struct{ EventID, DeliveryID string }{event.EventID.String(), event.DeliveryID.String()})
		n := len(requests)
		mu.Unlock()
		if event.ProjectID != f.project || event.EventType != "work_item.created" {
			t.Errorf("incorrect event: %+v", event)
		}
		if n == 1 {
			w.WriteHeader(http.StatusServiceUnavailable)
		} else {
			w.WriteHeader(http.StatusNoContent)
		}
	}))
	defer receiver.Close()
	base := evidenceURL(f)
	register := map[string]any{"endpoint": receiver.URL, "event_types": []string{"work_item.created"}}
	if status, _ := testRequest(t, "POST", base+"/webhooks", f.agent, "agent-must-not-register", "", register); status != 403 {
		t.Fatalf("Agent registered webhook: %d", status)
	}
	if status, _ := testRequest(t, "POST", base+"/webhooks", f.viewer, "viewer-must-not-register", "", register); status != 403 {
		t.Fatalf("Viewer registered webhook: %d", status)
	}
	status, created := testRequest(t, "POST", base+"/webhooks", f.admin, "phase5-webhook-first", "", register)
	if status != 201 {
		t.Fatalf("register: %d %v", status, created)
	}
	id := created["id"].(string)
	s := created["secret"].(string)
	if !strings.HasPrefix(s, "whsec_") {
		t.Fatalf("secret prefix missing")
	}
	mu.Lock()
	secret = s
	mu.Unlock()
	status, replay := testRequest(t, "POST", base+"/webhooks", f.admin, "phase5-webhook-first", "", register)
	if status != 201 || replay["id"] != id {
		t.Fatalf("register retry %d %v", status, replay)
	}
	if _, ok := replay["secret"]; ok {
		t.Fatal("webhook secret exposed on replay")
	}
	var nonce, ciphertext []byte
	if err := f.pool.QueryRow(ctx, `SELECT secret_nonce,secret_ciphertext FROM qual.webhook_subscriptions WHERE id=$1`, id).Scan(&nonce, &ciphertext); err != nil {
		t.Fatal(err)
	}
	if bytes.Contains(ciphertext, []byte(s)) || bytes.Equal(ciphertext, []byte(s)) {
		t.Fatal("webhook secret stored in plaintext")
	}
	var idem []byte
	if err := f.pool.QueryRow(ctx, `SELECT response_json FROM qual.idempotency_records WHERE operation='webhook.register' AND project_id=$1`, f.project).Scan(&idem); err != nil {
		t.Fatal(err)
	}
	if bytes.Contains(idem, []byte(s)) {
		t.Fatal("webhook secret persisted in idempotency response")
	}
	if status, _ := testRequest(t, "POST", base+"/webhooks", f.admin, "phase5-webhook-ssrf", "", map[string]any{
		"endpoint": "http://169.254.169.254/latest/meta-data", "event_types": []string{"*"},
	}); status != 422 {
		t.Fatalf("SSRF URL allowed: %d", status)
	}
	if status, _ := testRequest(t, "POST", base+"/webhooks", f.admin, "phase5-webhook-host-bypass", "", map[string]any{
		"endpoint": "https://evil.example.com/callback", "event_types": []string{"*"},
	}); status != 422 {
		t.Fatalf("host allowlist bypass: %d", status)
	}
	create(t, f, f.agent, "trigger webhook delivery")
	worked, err := f.hooks.ProcessOne(ctx)
	if err != nil || !worked {
		t.Fatalf("first webhook send: %t %v", worked, err)
	}
	status, deliveries := testRequest(t, "GET", base+"/webhook-deliveries", f.admin, "", "", nil)
	if status != 200 || len(deliveries["items"].([]any)) != 1 {
		t.Fatalf("delivery history %d %v", status, deliveries)
	}
	first := deliveries["items"].([]any)[0].(map[string]any)
	if first["status"] != "retrying" || first["attempt_count"] != float64(1) {
		t.Fatalf("failed delivery not scheduled: %v", first)
	}
	if _, err := f.pool.Exec(ctx, `UPDATE qual.webhook_deliveries SET next_attempt_at=now()-interval '1 second' WHERE subscription_id=$1`, id); err != nil {
		t.Fatal(err)
	}
	worked, err = f.hooks.ProcessOne(ctx)
	if err != nil || !worked {
		t.Fatalf("second webhook send %t %v", worked, err)
	}
	status, deliveries = testRequest(t, "GET", base+"/webhook-deliveries", f.admin, "", "", nil)
	if status != 200 {
		t.Fatal(status, deliveries)
	}
	last := deliveries["items"].([]any)[0].(map[string]any)
	if last["status"] != "delivered" || last["attempt_count"] != float64(2) {
		t.Fatalf("delivery did not succeed: %v", last)
	}
	mu.Lock()
	seen := append([]struct{ EventID, DeliveryID string }{}, requests...)
	mu.Unlock()
	if len(seen) != 2 || seen[0] != seen[1] {
		t.Fatalf("redelivery changed event/delivery ID: %+v", seen)
	}
	var delivered bool
	if err := f.pool.QueryRow(ctx, `SELECT delivered_at IS NOT NULL FROM qual.outbox_events WHERE id=$1`, seen[0].EventID).Scan(&delivered); err != nil || !delivered {
		t.Fatalf("outbox not marked delivered: %v", err)
	}
	if status, _ := rawHTTP(t, "POST", base+"/webhooks/"+id+"/disable", f.agent, "agent-disable-denied", nil, ""); status != 403 {
		t.Fatalf("Agent disabled webhook: %d", status)
	}
	if status, _ := rawHTTP(t, "POST", base+"/webhooks/"+id+"/disable", f.admin, "admin-disable-webhook", nil, ""); status != 204 {
		t.Fatalf("disable subscription: %d", status)
	}
	if status, _ := rawHTTP(t, "POST", base+"/webhooks/"+id+"/disable", f.admin, "admin-disable-webhook", nil, ""); status != 204 {
		t.Fatalf("disable idempotency replay status=%d", status)
	}
	create(t, f, f.agent, "new event after disabled subscription")
	worked, err = f.hooks.ProcessOne(ctx)
	if err != nil || worked {
		t.Fatalf("disabled subscription triggered a send: %v %v", worked, err)
	}
}
func TestWebhookRetriesDeadAndDestinationPolicy(t *testing.T) {
	f := setup(t)
	receiver := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(500) }))
	defer receiver.Close()
	base := evidenceURL(f)
	status, hook := testRequest(t, "POST", base+"/webhooks", f.admin, "dead-webhook-register", "", map[string]any{
		"endpoint": receiver.URL, "event_types": []string{"work_item.created"},
	})
	if status != 201 {
		t.Fatalf("create webhook: %d %v", status, hook)
	}
	create(t, f, f.agent, "always-failing-webhook")
	ctx := context.Background()
	for i := 0; i < 5; i++ {
		ok, err := f.hooks.ProcessOne(ctx)
		if err != nil || !ok {
			t.Fatalf("attempt %d: %v %v", i, ok, err)
		}
		_, err = f.pool.Exec(ctx, `UPDATE qual.webhook_deliveries SET next_attempt_at=now()-interval '1 second'
    WHERE subscription_id=$1 AND status='retrying'`, hook["id"])
		if err != nil {
			t.Fatal(err)
		}
	}
	status, history := testRequest(t, "GET", base+"/webhook-deliveries", f.admin, "", "", nil)
	if status != 200 || len(history["items"].([]any)) != 1 {
		t.Fatal(status, history)
	}
	delivered := history["items"].([]any)[0].(map[string]any)
	if delivered["status"] != "dead" || delivered["attempt_count"] != float64(5) {
		t.Fatalf("retry limit bypassed %v", delivered)
	}
	if done, err := f.hooks.ProcessOne(ctx); done || err != nil {
		t.Fatalf("dead letter retried %v %v", done, err)
	}
}
func TestWebhookPublicURLPolicy(t *testing.T) {
	cfg := webhooks.Config{AllowedHosts: []string{"callbacks.example.com"}, HasKey: true}
	denied := []string{"http://callbacks.example.com/hook", "https://127.0.0.1/hook",
		"https://169.254.169.254/hook", "https://callbacks.example.com.evil.test/hook",
		"https://user:secret@callbacks.example.com/hook", "https://callbacks.example.com/hook#secret",
		"file:///etc/passwd", "https://localhost/hook"}
	for _, candidate := range denied {
		if err := cfg.ValidateEndpoint(candidate); err == nil {
			t.Errorf("SSRF endpoint accepted: %s", candidate)
		}
	}
	if err := cfg.ValidateEndpoint("https://callbacks.example.com/qual"); err != nil {
		t.Fatalf("allowed HTTPS rejected: %v", err)
	}
	_ = time.Second
}
