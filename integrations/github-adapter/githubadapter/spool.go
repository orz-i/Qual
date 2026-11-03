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

// Spool is an owner-only persistent queue of normalized fact metadata.
// No webhook JSON, external titles or credentials are written to disk.
type Spool struct{ Root string }

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
	if err := safeDir(root); err != nil {
		return nil, err
	}
	for _, sub := range []string{"pending", "delivered"} {
		if err := safeDir(filepath.Join(root, sub)); err != nil {
			return nil, err
		}
	}
	return &Spool{Root: root}, nil
}

func (s *Spool) path(sub, id string) (string, error) {
	if !deliveryIDRE.MatchString(id) || (sub != "pending" && sub != "delivered") {
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
		(saved.State != "pending" && saved.State != "blocked_mapping" &&
			saved.State != "blocked_auth" && saved.State != "conflict" &&
			saved.State != "retry" && saved.State != "manual_review" && saved.State != "delivered") ||
		(sub == "delivered" && saved.State != "delivered") ||
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

// Enqueue persists metadata before returning a 2xx to GitHub.
// An identical redelivery (same X-GitHub-Delivery) is an acknowledged no-op.
// Same identity with altered content is always a conflict.
func (s *Spool) Enqueue(d Delivery) (bool, error) {
	if !deliveryIDRE.MatchString(d.DeliveryID) || d.State != "pending" || len(d.Summary) > MaxSummary {
		return false, ErrInvalid
	}
	created := false
	err := s.withLock("queue", func() error {
		for _, dir := range []string{"pending", "delivered"} {
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
		old, ok, err := s.read("pending", d.DeliveryID)
		if err != nil {
			return err
		}
		if !ok || old.stableIdentity() != d.stableIdentity() ||
			old.RepositoryID != d.RepositoryID || old.RepositoryName != d.RepositoryName ||
			old.ThreadKind != d.ThreadKind || old.Summary != d.Summary ||
			!old.ReceivedAt.Equal(d.ReceivedAt) || d.Attempts < old.Attempts {
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
		}
		return nil
	})
}

// RequeueAfterReview does not erase the actual Attempt counter or history.
// A human operator must independently review the last failure before calling
// this explicit local action. Conflicting GitHub/Qual identities remain blocked.
func (s *Spool) RequeueAfterReview(id string) error {
	if !deliveryIDRE.MatchString(id) {
		return ErrInvalid
	}
	return s.withLock("worker", func() error {
		return s.withLock("queue", func() error {
			item, exists, err := s.read("pending", id)
			if err != nil {
				return err
			}
			if !exists || (item.State != "manual_review" && item.State != "blocked_auth" &&
				!(item.State == "blocked_mapping" && item.LastErrorCode == "QUAL_WORK_ITEM_OR_THREAD_LINK_NOT_FOUND")) {
				return ErrConflict
			}
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
	return states, nil
}
