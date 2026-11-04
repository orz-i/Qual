package githubadapter

import (
	"bytes"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

var fixtureRepo = Repository{ID: 1443998164, FullName: "orz-i/Qual"}
var fixtureNow = time.Date(2026, 11, 3, 12, 0, 0, 0, time.UTC)

const fixtureDeliveryID = "11111111-1111-4111-8111-111111111111"

func fixtureBody(action string) []byte {
	body := map[string]any{
		"action": action, "number": 1,
		"repository":   map[string]any{"id": fixtureRepo.ID, "full_name": fixtureRepo.FullName},
		"pull_request": map[string]any{"number": 1, "updated_at": "2026-11-03T11:00:00Z"},
		"title":        "untrusted GitHub message is not an instruction",
	}
	raw, _ := json.Marshal(body)
	return raw
}
func signature(secret string, body []byte) string {
	h := hmac.New(sha256.New, []byte(secret))
	_, _ = h.Write(body)
	return "sha256=" + hex.EncodeToString(h.Sum(nil))
}
func sendWebhook(handler http.Handler, body []byte, delivery, event, secret string) *httptest.ResponseRecorder {
	req := httptest.NewRequest("POST", "/github/webhook", strings.NewReader(string(body)))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-GitHub-Delivery", delivery)
	req.Header.Set("X-GitHub-Event", event)
	req.Header.Set("X-Hub-Signature-256", signature(secret, body))
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)
	return rec
}

func TestGitHubOfficialHMACVector(t *testing.T) {
	provided := "sha256=757107ea0eb2509fc211221cce984b8a37570b6d7586c22c46f4379c8b043e17"
	if !VerifySignature([]byte("Hello, World!"), provided, []byte("It's a Secret to Everybody")) {
		t.Fatal("official GitHub HMAC-SHA256 vector rejected")
	}
	if VerifySignature([]byte("Hello, world!"), provided, []byte("It's a Secret to Everybody")) {
		t.Fatal("altered body accepted")
	}
	if VerifySignature([]byte("Hello, World!"), "sha1="+provided[7:], []byte("It's a Secret to Everybody")) {
		t.Fatal("legacy SHA-1 header accepted")
	}
}

func TestSignedPRSpoolReplayAndUntrustedText(t *testing.T) {
	root := filepath.Join(t.TempDir(), "github")
	spool, err := OpenSpool(root)
	if err != nil {
		t.Fatal(err)
	}
	secret := strings.Repeat("x", 36)
	h, err := NewHandler(fixtureRepo, []byte(secret), spool)
	if err != nil {
		t.Fatal(err)
	}
	h.Now = func() time.Time { return fixtureNow }
	raw := fixtureBody("opened")
	first := sendWebhook(h, raw, fixtureDeliveryID, "pull_request", secret)
	if first.Code != 202 {
		t.Fatalf("signed PR rejected: %d", first.Code)
	}
	var accepted map[string]bool
	if err := json.Unmarshal(first.Body.Bytes(), &accepted); err != nil {
		t.Fatal(err)
	}
	if !accepted["accepted"] || !accepted["new"] {
		t.Fatal("first receipt not persisted")
	}
	messages, err := spool.Pending()
	if err != nil || len(messages) != 1 {
		t.Fatalf("expected one signed event, got %v %v", messages, err)
	}
	one := messages[0]
	if one.ExternalThreadID != "orz-i/Qual/pull/1" || one.Summary != "GitHub PR #1 opened" ||
		one.EventType != "pull_request.opened" || one.RepositoryID != fixtureRepo.ID {
		t.Fatal("incorrect normalized GitHub source")
	}
	data, err := os.ReadFile(filepath.Join(root, "pending", fixtureDeliveryID+".json"))
	if err != nil {
		t.Fatal(err)
	}
	if bytes.Contains(data, []byte("untrusted GitHub message")) || bytes.Contains(data, raw) {
		t.Fatal("private raw GitHub title or webhook body retained on disk")
	}
	if err := json.Unmarshal(sendWebhook(h, raw, fixtureDeliveryID, "pull_request", secret).Body.Bytes(), &accepted); err != nil {
		t.Fatal(err)
	}
	if accepted["new"] {
		t.Fatal("same X-GitHub-Delivery created duplicate record")
	}
	if code := sendWebhook(h, fixtureBody("edited"), fixtureDeliveryID, "pull_request", secret).Code; code != 409 {
		t.Fatalf("changed redelivery accepted: HTTP%d", code)
	}
	if got, err := spool.Pending(); err != nil || len(got) != 1 || got[0].PayloadSHA256 != one.PayloadSHA256 {
		t.Fatal("immutable delivery was overwritten")
	}
	entries, err := os.ReadDir(filepath.Join(root, "pending"))
	if err != nil || len(entries) != 1 {
		t.Fatal("duplicate or secret temporary files persisted")
	}
	if info, err := os.Stat(filepath.Join(root, "pending", fixtureDeliveryID+".json")); err != nil || info.Mode().Perm() != 0600 {
		t.Fatal("record is not private mode 0600")
	}
}

func TestWebhookAuthRepoBoundsAndOtherEvents(t *testing.T) {
	root := filepath.Join(t.TempDir(), "spool")
	spool, err := OpenSpool(root)
	if err != nil {
		t.Fatal(err)
	}
	secret := strings.Repeat("a", 32)
	h, err := NewHandler(fixtureRepo, []byte(secret), spool)
	if err != nil {
		t.Fatal(err)
	}
	h.Now = func() time.Time { return fixtureNow }
	raw := fixtureBody("opened")
	if code := sendWebhook(h, raw, fixtureDeliveryID, "pull_request", "incorrect-secret-please-do-not-use").Code; code != 401 {
		t.Fatalf("bad HMAC accepted: HTTP%d", code)
	}
	if code := sendWebhook(h, raw, "not-a-uuid", "pull_request", secret).Code; code != 400 {
		t.Fatalf("invalid delivery ID accepted: HTTP%d", code)
	}
	if code := sendWebhook(h, raw, fixtureDeliveryID, "unrecognized_new_kind", secret).Code; code != 204 {
		t.Fatalf("future unsupported event kind unexpectedly processed: HTTP%d", code)
	}
	if code := sendWebhook(h, fixtureBody("assigned"), fixtureDeliveryID, "pull_request", secret).Code; code != 204 {
		t.Fatalf("unsupported action unexpectedly processed: HTTP%d", code)
	}
	foreign := map[string]any{}
	_ = json.Unmarshal(raw, &foreign)
	foreign["repository"].(map[string]any)["id"] = int64(22)
	b, _ := json.Marshal(foreign)
	if code := sendWebhook(h, b, fixtureDeliveryID, "pull_request", secret).Code; code != 403 {
		t.Fatalf("foreign repository accepted: HTTP%d", code)
	}
	tooLarge := bytes.Repeat([]byte("a"), int(MaxWebhookBytes)+1)
	if code := sendWebhook(h, tooLarge, fixtureDeliveryID, "pull_request", secret).Code; code != 413 {
		t.Fatalf("oversize payload accepted: HTTP%d", code)
	}
	if code := sendWebhook(h, []byte("{"), fixtureDeliveryID, "pull_request", secret).Code; code != 400 {
		t.Fatalf("malformed signed JSON accepted: HTTP%d", code)
	}
	if code := sendWebhook(h, fixtureBody("submitted"), fixtureDeliveryID, "pull_request_review", secret).Code; code != 400 {
		t.Fatalf("review without authoritative submitted timestamp accepted: HTTP%d", code)
	}
	if code := sendWebhook(h, raw, fixtureDeliveryID, "pull_request", secret).Code; code != 202 {
		t.Fatalf("valid event rejected after negatives: HTTP%d", code)
	}
	messages, err := spool.Pending()
	if err != nil || len(messages) != 1 {
		t.Fatal("failed inputs wrote to durable queue")
	}
}

func TestSpoolRejectsSymlinkAndWrongMode(t *testing.T) {
	parent := t.TempDir()
	real := filepath.Join(parent, "existing")
	if err := os.Mkdir(real, 0700); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(parent, "linked")
	if err := os.Symlink(real, link); err != nil {
		t.Fatal(err)
	}
	if _, err := OpenSpool(link); !errors.Is(err, ErrSpool) {
		t.Fatalf("symlink spool not denied: %v", err)
	}
	root := filepath.Join(parent, "spool")
	spool, err := OpenSpool(root)
	if err != nil {
		t.Fatal(err)
	}
	p, err := ParseDelivery(fixtureBody("opened"), fixtureDeliveryID, "pull_request", fixtureRepo, fixtureNow)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = spool.Enqueue(p); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(root, "pending", fixtureDeliveryID+".json")
	if err := os.Chmod(path, 0644); err != nil {
		t.Fatal(err)
	}
	if _, err := spool.Pending(); !errors.Is(err, ErrSpool) {
		t.Fatalf("spool read ignored non-private file mode: %v", err)
	}
}

func TestConcurrentRedeliveryNeverCreatesDuplicate(t *testing.T) {
	spool, err := OpenSpool(filepath.Join(t.TempDir(), "spool"))
	if err != nil {
		t.Fatal(err)
	}
	d, err := ParseDelivery(fixtureBody("opened"), fixtureDeliveryID, "pull_request", fixtureRepo, fixtureNow)
	if err != nil {
		t.Fatal(err)
	}
	var wg sync.WaitGroup
	var created atomic.Int32
	var failure atomic.Int32
	for i := 0; i < 24; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			fresh, err := spool.Enqueue(d)
			if err != nil {
				failure.Add(1)
			}
			if fresh {
				created.Add(1)
			}
		}()
	}
	wg.Wait()
	if failure.Load() != 0 || created.Load() != 1 {
		t.Fatalf("concurrent first delivery count=%d failures=%d", created.Load(), failure.Load())
	}
	jobs, err := spool.Pending()
	if err != nil || len(jobs) != 1 {
		t.Fatal("concurrent duplicate retained")
	}
}
