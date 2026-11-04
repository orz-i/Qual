package githubadapter

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"syscall"
	"time"
)

const MaxDelivered = 10000
const MaxRecordBytes int64 = 8192
const MaxTransitions = 24

// Spool is an owner-only persistent queue of normalized fact metadata.
// No webhook JSON, external titles or credentials are written to disk.
type Spool struct {
	Root string
	// Test-scoped probe; unset in normal operations to use kernel statfs.
	// Never accept this from HTTP, Webhook or untrusted Mapping configuration.
	probeDisk func(string) (filesystemStats, error)
}

func safeDir(path string) error {
	fi, err := os.Lstat(path)
	if errors.Is(err, os.ErrNotExist) {
		if err := os.Mkdir(path, 0700); err != nil {
			return err
		}
		fi, err = os.Lstat(path)
	}
	if err != nil {
		return err
	}
	if !fi.IsDir() || fi.Mode().Perm() != 0700 || fi.Mode()&os.ModeSymlink != 0 {
		return ErrSpool
	}
	if s, ok := fi.Sys().(*syscall.Stat_t); ok && int(s.Uid) != os.Getuid() {
		return ErrSpool
	}
	return nil
}

func safeParent(path string) error {
	info, err := os.Lstat(path)
	if err != nil || !info.IsDir() || info.Mode()&os.ModeSymlink != 0 ||
		info.Mode().Perm()&0022 != 0 {
		return ErrSpool
	}
	if owner, ok := info.Sys().(*syscall.Stat_t); !ok || int(owner.Uid) != os.Getuid() {
		return ErrSpool
	}
	return nil
}

func OpenSpool(root string) (*Spool, error) {
	if root == "" || !filepath.IsAbs(root) {
		return nil, ErrSpool
	}
	// Parent must be owned by this process and not writable by any other
	// user. A 0700 child alone cannot protect the spool if its parent is
	// attacker-writable (rename/symlink replacement before locking).
	if err := safeParent(filepath.Dir(root)); err != nil {
		return nil, err
	}
	// Only a truly non-existent root is allowed to receive new ACTIVE
	// origin witnesses. Existing directories missing their witnesses MUST
	// fail closed, including restored snapshots with deleted quarantine
	// markers. Never silently auto-upgrade an ambiguous legacy spool.
	_, statErr := os.Lstat(root)
	fresh := errors.Is(statErr, os.ErrNotExist)
	if statErr != nil && !fresh {
		return nil, statErr
	}
	if err := safeDir(root); err != nil {
		return nil, err
	}
	if fresh {
		if err := createFreshOrigin(root); err != nil {
			return nil, err
		}
	}
	for _, sub := range []string{"pending", "delivered", "archived", "archive-index"} {
		if err := safeDir(filepath.Join(root, sub)); err != nil {
			return nil, err
		}
	}
	spool := &Spool{Root: root}
	// Validate any restore quarantine metadata even for read-only OpenSpool;
	// a malformed/mispermissioned marker cannot silently become an active
	// queue. Ordinary never-restored pilot spools continue to open normally.
	recovery, err := spool.RecoveryStatus()
	if err != nil {
		return nil, err
	}
	if !recovery.Quarantined {
		if err := requireFreshOrigin(root); err != nil {
			return nil, err
		}
	}
	// ACTIVE spools may safely complete crash recovery before new writes.
	// QUARANTINED restored spools are only for read-only inspection and
	// external source reconciliation: never silently delete Pending or
	// move Delivered/Archived facts when opening an old signed snapshot.
	err = spool.withLock("worker", func() error {
		return spool.withLock("queue", func() error {
			if recovery.Quarantined {
				return spool.validateQuarantinedReadOnly()
			}
			if err := spool.reconcileLocked(); err != nil {
				return err
			}
			return spool.reconcileArchiveLocked()
		})
	})
	if err != nil {
		return nil, err
	}
	return spool, nil
}

func (s *Spool) path(sub, id string) (string, error) {
	if !deliveryIDRE.MatchString(id) || (sub != "pending" && sub != "delivered" && sub != "archived") {
		return "", ErrSpool
	}
	return filepath.Join(s.Root, sub, id+".json"), nil
}

func trustedFile(path string) ([]byte, error) {
	info, err := os.Lstat(path)
	if err != nil {
		return nil, err
	}
	if !info.Mode().IsRegular() || info.Mode().Perm() != 0600 || info.Size() > MaxRecordBytes ||
		info.Mode()&os.ModeSymlink != 0 {
		return nil, ErrSpool
	}
	if st, ok := info.Sys().(*syscall.Stat_t); ok && int(st.Uid) != os.Getuid() {
		return nil, ErrSpool
	}
	// Recheck the opened inode: Lstat followed by ordinary Open is a
	// symlink-swap race. O_NOFOLLOW + Fstat bind trust to the same handle.
	fd, err := syscall.Open(path, syscall.O_RDONLY|syscall.O_NOFOLLOW|syscall.O_CLOEXEC, 0)
	if err != nil {
		return nil, ErrSpool
	}
	file := os.NewFile(uintptr(fd), path)
	defer file.Close()
	var opened syscall.Stat_t
	if err := syscall.Fstat(fd, &opened); err != nil {
		return nil, ErrSpool
	}
	if opened.Mode&syscall.S_IFMT != syscall.S_IFREG ||
		opened.Mode&0777 != 0600 || opened.Size > MaxRecordBytes ||
		int(opened.Uid) != os.Getuid() {
		return nil, ErrSpool
	}
	return io.ReadAll(io.LimitReader(file, MaxRecordBytes+1))
}

func (s *Spool) read(sub, id string) (Delivery, bool, error) {
	path, err := s.path(sub, id)
	if err != nil {
		return Delivery{}, false, err
	}
	data, err := trustedFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return Delivery{}, false, nil
	}
	if err != nil {
		return Delivery{}, false, err
	}
	var saved Delivery
	if err := json.Unmarshal(data, &saved); err != nil {
		return Delivery{}, false, ErrSpool
	}
	if saved.DeliveryID != id || saved.RepositoryID <= 0 || saved.PayloadSHA256 == "" ||
		(saved.PinnedTarget != nil && !validTarget(*saved.PinnedTarget)) ||
		!validHistory(saved.History) ||
		(saved.State != "pending" && saved.State != "blocked_mapping" &&
			saved.State != "blocked_auth" && saved.State != "conflict" &&
			saved.State != "retry" && saved.State != "manual_review" && saved.State != "delivered") ||
		((sub == "delivered" || sub == "archived") && saved.State != "delivered") ||
		(sub == "pending" && saved.State == "delivered") {
		return Delivery{}, false, ErrSpool
	}
	return saved, true, nil
}

func (s *Spool) withLock(name string, run func() error) error {
	if name != "queue" && name != "worker" {
		return ErrSpool
	}
	path := filepath.Join(s.Root, name+".lock")
	fd, err := syscall.Open(path, syscall.O_RDWR|syscall.O_CREAT|syscall.O_NOFOLLOW, 0600)
	if err != nil {
		return err
	}
	file := os.NewFile(uintptr(fd), path)
	defer file.Close()
	if _, err := trustedFile(path); err != nil {
		return err
	}
	if err := syscall.Flock(fd, syscall.LOCK_EX); err != nil {
		return err
	}
	defer syscall.Flock(fd, syscall.LOCK_UN)
	return run()
}

func (s *Spool) atomicWrite(path string, d Delivery) error {
	// Refuse mutations before consuming recovery reserve. File and directory
	// fsync must also succeed before a webhook may acknowledge 202.
	if err := s.diskPreflight(); err != nil {
		return err
	}
	data, err := json.Marshal(d)
	if err != nil {
		return err
	}
	if int64(len(data)) > MaxRecordBytes {
		return ErrSpool
	}
	dir := filepath.Dir(path)
	f, err := os.CreateTemp(dir, ".pending-*")
	if err != nil {
		return err
	}
	tmp := f.Name()
	defer os.Remove(tmp)
	if err := f.Chmod(0600); err != nil {
		f.Close()
		return err
	}
	if _, err := f.Write(data); err != nil {
		f.Close()
		return err
	}
	if err := f.Sync(); err != nil {
		f.Close()
		return err
	}
	if err := f.Close(); err != nil {
		return err
	}
	if err := os.Rename(tmp, path); err != nil {
		return err
	}
	opened, err := os.Open(dir)
	if err != nil {
		return err
	}
	defer opened.Close()
	return opened.Sync()
}

func validHistory(entries []Transition) bool {
	if len(entries) > MaxTransitions {
		return false
	}
	for _, e := range entries {
		if e.At.IsZero() || e.Attempts < 0 || e.OperatorUID < 0 ||
			len(e.ErrorCode) > 100 || e.HTTPStatus < 0 ||
			(e.Kind != "state" && e.Kind != "operator_requeue") {
			return false
		}
		if e.Kind == "operator_requeue" &&
			e.ReasonCode != "auth-restored" &&
			e.ReasonCode != "link-repaired" &&
			e.ReasonCode != "transient-verified" {
			return false
		}
	}
	return true
}
func historyPrefix(a, b []Transition) bool {
	if len(a) > len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

func sameHistory(a, b []Transition) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

func validTarget(t Target) bool {
	return deliveryIDRE.MatchString(t.ProjectID) &&
		deliveryIDRE.MatchString(t.WorkItemID) && deliveryIDRE.MatchString(t.ThreadID)
}

func sameTarget(a, b *Target) bool {
	if a == nil || b == nil {
		return a == nil && b == nil
	}
	return *a == *b
}

func syncDirectory(path string) error {
	dir, err := os.Open(path)
	if err != nil {
		return err
	}
	defer dir.Close()
	return dir.Sync()
}

// reconcileLocked runs under the queue lock. The durable Delivered receipt
// is authoritative if a crash left the identical Pending record behind.
// A disagreement is an integrity failure, not permission to overwrite it.
func (s *Spool) reconcileLocked() error {
	pendingDir := filepath.Join(s.Root, "pending")
	entries, err := os.ReadDir(pendingDir)
	if err != nil {
		return err
	}
	changed := false
	for _, entry := range entries {
		if !strings.HasSuffix(entry.Name(), ".json") {
			continue
		}
		id := strings.TrimSuffix(entry.Name(), ".json")
		old, exists, err := s.read("pending", id)
		if err != nil || !exists {
			return ErrSpool
		}
		hot, hotDone, err := s.read("delivered", id)
		if err != nil {
			return err
		}
		cold, coldDone, err := s.read("archived", id)
		if err != nil || hotDone && coldDone {
			return ErrSpool
		}
		if !hotDone && !coldDone {
			continue
		}
		done := hot
		if coldDone {
			done = cold
			indexed, exists, err := s.readArchiveIndex(done.PayloadSHA256)
			if err != nil || !exists || indexed.DeliveryID != id {
				return ErrSpool
			}
		}
		if old.stableIdentity() != done.stableIdentity() ||
			old.RepositoryID != done.RepositoryID || old.RepositoryName != done.RepositoryName ||
			old.ThreadKind != done.ThreadKind || old.Summary != done.Summary ||
			!old.ReceivedAt.Equal(done.ReceivedAt) || !sameTarget(old.PinnedTarget, done.PinnedTarget) ||
			!historyPrefix(old.History, done.History) || done.DeliveredEventID == "" {
			return ErrConflict
		}
		path, _ := s.path("pending", id)
		if err := os.Remove(path); err != nil {
			return err
		}
		changed = true
	}
	if changed {
		return syncDirectory(pendingDir)
	}
	return nil
}

// payloadSeenLocked scans bounded, trusted disk records rather than relying
// on an untrusted, unsigned X-GitHub-Delivery header for replay prevention.
// No secondary index can get out of sync in a power-loss window.
func (s *Spool) payloadSeenLocked(in Delivery) (bool, error) {
	// A content-addressed receipt remains queryable even after a completed
	// delivery moves out of the bounded hot directory. Never evict this key.
	if saved, exists, err := s.readArchiveIndex(in.PayloadSHA256); err != nil {
		return false, err
	} else if exists {
		if saved.RepositoryID != in.RepositoryID ||
			saved.RepositoryName != in.RepositoryName {
			return false, ErrConflict
		}
		return true, nil
	}
	for _, sub := range []string{"pending", "delivered"} {
		entries, err := os.ReadDir(filepath.Join(s.Root, sub))
		if err != nil {
			return false, err
		}
		for _, entry := range entries {
			if !strings.HasSuffix(entry.Name(), ".json") {
				continue
			}
			id := strings.TrimSuffix(entry.Name(), ".json")
			d, found, err := s.read(sub, id)
			if err != nil || !found {
				return false, ErrSpool
			}
			if d.PayloadSHA256 != in.PayloadSHA256 {
				continue
			}
			if d.RepositoryID != in.RepositoryID || d.RepositoryName != in.RepositoryName {
				return false, ErrConflict
			}
			return true, nil
		}
	}
	return false, nil
}

// PinTarget persists the first approved destination BEFORE external HTTP.
// The pin is immutable through retries, mapping changes and process restarts.
func (s *Spool) PinTarget(in Delivery, target Target) (Delivery, error) {
	if !validTarget(target) {
		return Delivery{}, ErrInvalid
	}
	var result Delivery
	err := s.withLock("queue", func() error {
		if err := s.requireActive(); err != nil {
			return err
		}
		saved, found, err := s.read("pending", in.DeliveryID)
		if err != nil {
			return err
		}
		if !found || saved.stableIdentity() != in.stableIdentity() ||
			saved.RepositoryID != in.RepositoryID || saved.RepositoryName != in.RepositoryName {
			return ErrConflict
		}
		if saved.PinnedTarget != nil {
			if *saved.PinnedTarget != target {
				return ErrConflict
			}
			result = saved
			return nil
		}
		saved.PinnedTarget = &target
		path, _ := s.path("pending", in.DeliveryID)
		if err := s.atomicWrite(path, saved); err != nil {
			return err
		}
		result = saved
		return nil
	})
	return result, err
}

// Enqueue persists metadata before returning a 2xx to GitHub.
// An identical redelivery (same X-GitHub-Delivery) is an acknowledged no-op.
// Same identity with altered content is always a conflict.
func (s *Spool) Enqueue(d Delivery) (bool, error) {
	if !deliveryIDRE.MatchString(d.DeliveryID) || d.State != "pending" || len(d.Summary) > MaxSummary {
		return false, ErrInvalid
	}
	created := false
	err := s.withLock("queue", func() error {
		if err := s.requireActive(); err != nil {
			return err
		}
		if err := s.reconcileLocked(); err != nil {
			return err
		}
		for _, dir := range []string{"pending", "delivered", "archived"} {
			saved, exists, err := s.read(dir, d.DeliveryID)
			if err != nil {
				return err
			}
			if exists {
				if saved.stableIdentity() != d.stableIdentity() || saved.RepositoryID != d.RepositoryID ||
					saved.RepositoryName != d.RepositoryName || saved.ThreadKind != d.ThreadKind ||
					saved.Summary != d.Summary {
					return ErrConflict
				}
				return nil
			}
		}
		if seen, err := s.payloadSeenLocked(d); err != nil {
			return err
		} else if seen {
			// The signature covers the same bytes; a different, UNSIGNED
			// Delivery ID cannot mint a second event or queue reservation.
			return nil
		}
		pending, err := os.ReadDir(filepath.Join(s.Root, "pending"))
		if err != nil {
			return err
		}
		delivered, err := os.ReadDir(filepath.Join(s.Root, "delivered"))
		if err != nil {
			return err
		}
		if len(pending) >= MaxPending || len(delivered) >= MaxDelivered {
			return ErrFull
		}
		p, err := s.path("pending", d.DeliveryID)
		if err != nil {
			return err
		}
		if err := s.atomicWrite(p, d); err != nil {
			return err
		}
		created = true
		return nil
	})
	return created, err
}

func (s *Spool) Pending() ([]Delivery, error) {
	result := []Delivery{}
	err := s.withLock("queue", func() error {
		files, err := os.ReadDir(filepath.Join(s.Root, "pending"))
		if err != nil {
			return err
		}
		for _, entry := range files {
			if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".json") {
				continue
			}
			id := strings.TrimSuffix(entry.Name(), ".json")
			d, ok, err := s.read("pending", id)
			if err != nil {
				return err
			}
			if !ok {
				return fmt.Errorf("lost pending delivery %s", id)
			}
			result = append(result, d)
		}
		return nil
	})
	sort.Slice(result, func(i, j int) bool { return result[i].DeliveryID < result[j].DeliveryID })
	return result, err
}

func (s *Spool) Update(d Delivery, completed bool) error {
	return s.withLock("queue", func() error {
		if err := s.requireActive(); err != nil {
			return err
		}
		if err := s.reconcileLocked(); err != nil {
			return err
		}
		old, ok, err := s.read("pending", d.DeliveryID)
		if err != nil {
			return err
		}
		if !ok || old.stableIdentity() != d.stableIdentity() ||
			old.RepositoryID != d.RepositoryID || old.RepositoryName != d.RepositoryName ||
			old.ThreadKind != d.ThreadKind || old.Summary != d.Summary ||
			!old.ReceivedAt.Equal(d.ReceivedAt) || !sameTarget(old.PinnedTarget, d.PinnedTarget) ||
			!sameHistory(old.History, d.History) || d.Attempts < old.Attempts {
			return ErrConflict
		}
		if completed && !deliveryIDRE.MatchString(d.DeliveredEventID) {
			return ErrInvalid
		}
		sub := "pending"
		if completed {
			sub = "delivered"
			d.State = "delivered"
		}
		if len(d.History) >= MaxTransitions {
			return ErrFull // preserve existing evidence instead of truncating it
		}
		d.History = append(d.History, Transition{
			At: time.Now().UTC(), Kind: "state", State: d.State,
			Attempts: d.Attempts, HTTPStatus: d.LastHTTPStatus,
			ErrorCode: d.LastErrorCode,
		})
		path, err := s.path(sub, d.DeliveryID)
		if err != nil {
			return err
		}
		if err := s.atomicWrite(path, d); err != nil {
			return err
		}
		if completed {
			oldPath, _ := s.path("pending", d.DeliveryID)
			if err := os.Remove(oldPath); err != nil {
				return err
			}
			// Sync the source directory too. A crash might otherwise
			// resurrect Pending; reconciliation still prevents replay.
			return syncDirectory(filepath.Join(s.Root, "pending"))
		}
		return nil
	})
}

// RequeueAfterReview does not erase the actual Attempt counter or history.
// A human operator must independently review the last failure before calling
// this explicit local action. Conflicting GitHub/Qual identities remain blocked.
func (s *Spool) RequeueAfterReview(id, reasonCode string) error {
	if !deliveryIDRE.MatchString(id) ||
		(reasonCode != "auth-restored" && reasonCode != "link-repaired" && reasonCode != "transient-verified") {
		return ErrInvalid
	}
	return s.withLock("worker", func() error {
		return s.withLock("queue", func() error {
			if err := s.requireActive(); err != nil {
				return err
			}
			item, exists, err := s.read("pending", id)
			if err != nil {
				return err
			}
			if !exists || (item.State != "manual_review" && item.State != "blocked_auth" &&
				!(item.State == "blocked_mapping" && (item.LastErrorCode == "QUAL_WORK_ITEM_OR_THREAD_LINK_NOT_FOUND" ||
					item.LastErrorCode == "PINNED_TARGET_CHANGED"))) {
				return ErrConflict
			}
			// Reject reason codes for unrelated failure states.
			switch item.State {
			case "blocked_auth":
				if reasonCode != "auth-restored" {
					return ErrInvalid
				}
			case "blocked_mapping":
				if reasonCode != "link-repaired" {
					return ErrInvalid
				}
			case "manual_review":
				if reasonCode != "transient-verified" {
					return ErrInvalid
				}
			}
			if len(item.History) >= MaxTransitions {
				return ErrFull
			}
			item.History = append(item.History, Transition{
				At: time.Now().UTC(), Kind: "operator_requeue",
				State: item.State, Attempts: item.Attempts,
				HTTPStatus: item.LastHTTPStatus, ErrorCode: item.LastErrorCode,
				ReasonCode: reasonCode, OperatorUID: os.Getuid(),
			})
			item.State = "pending"
			item.NextAttemptAt = time.Time{}
			item.LastErrorCode = "MANUAL_RETRY_APPROVED"
			path, err := s.path("pending", id)
			if err != nil {
				return err
			}
			return s.atomicWrite(path, item)
		})
	})
}

// CapacityStatus exposes only counts and saturation, never raw payloads or
// credentials. The pilot fails closed rather than deleting old delivered
// identities, which would permit replay after a retention window.
type CapacityStatus struct {
	RestoreQuarantined           bool   `json:"restore_quarantined"`
	RequiresSourceReconciliation bool   `json:"requires_source_reconciliation"`
	PendingUsed                  int    `json:"pending_used"`
	PendingLimit                 int    `json:"pending_limit"`
	DeliveredUsed                int    `json:"delivered_used"`
	DeliveredLimit               int    `json:"delivered_limit"`
	ArchivedUsed                 int    `json:"archived_used"`
	DiskTotalBytes               uint64 `json:"disk_total_bytes"`
	DiskAvailableBytes           uint64 `json:"disk_available_bytes"`
	DiskReserveBytes             uint64 `json:"disk_reserve_bytes"`
	DiskNearLimit                bool   `json:"disk_near_limit"`
	DiskIngressBlocked           bool   `json:"disk_ingress_blocked"`
	NearLimit                    bool   `json:"near_limit"`
	IngressBlocked               bool   `json:"ingress_blocked"`
}

func (s *Spool) Capacity() (CapacityStatus, error) {
	counts, err := s.Counts()
	if err != nil {
		return CapacityStatus{}, err
	}
	pending := 0
	for state, count := range counts {
		if state != "delivered" && state != "archived" {
			pending += count
		}
	}
	delivered := counts["delivered"]
	disk, err := s.disk()
	if err != nil {
		return CapacityStatus{}, err // fail closed, never report fake capacity
	}
	diskNear := disk.AvailableBytes <= diskWarnFreeBytes ||
		disk.AvailableBytes <= disk.TotalBytes/5
	diskBlocked := disk.AvailableBytes < diskReserveBytes
	recovery, err := s.RecoveryStatus()
	if err != nil {
		return CapacityStatus{}, err
	}
	return CapacityStatus{
		RestoreQuarantined:           recovery.Quarantined,
		RequiresSourceReconciliation: recovery.RequiresSourceReconciliation,
		PendingUsed:                  pending, PendingLimit: MaxPending,
		DeliveredUsed: delivered, DeliveredLimit: MaxDelivered,
		ArchivedUsed:       counts["archived"],
		DiskTotalBytes:     disk.TotalBytes,
		DiskAvailableBytes: disk.AvailableBytes,
		DiskReserveBytes:   diskReserveBytes,
		DiskNearLimit:      diskNear,
		DiskIngressBlocked: diskBlocked,
		NearLimit:          pending*5 >= MaxPending*4 || delivered*5 >= MaxDelivered*4 || diskNear,
		IngressBlocked:     pending >= MaxPending || delivered >= MaxDelivered || diskBlocked || recovery.Quarantined,
	}, nil
}

func (s *Spool) Counts() (map[string]int, error) {
	pending, err := s.Pending()
	if err != nil {
		return nil, err
	}
	states := map[string]int{}
	for _, item := range pending {
		states[item.State]++
	}
	files, err := os.ReadDir(filepath.Join(s.Root, "delivered"))
	if err != nil {
		return nil, err
	}
	states["delivered"] = len(files)
	archived, err := os.ReadDir(filepath.Join(s.Root, "archived"))
	if err != nil {
		return nil, err
	}
	states["archived"] = len(archived)
	return states, nil
}
