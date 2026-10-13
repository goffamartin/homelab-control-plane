package setup

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
)

const (
	transactionArea      = ".hlcp-setup-transaction"
	backupArea           = ".hlcp-setup-backup"
	manifestName         = "manifest.json"
	nextManifestName     = "manifest.next"
	maximumManifestBytes = 64 * 1024
)

var (
	ErrConsentRequired    = errors.New("setup: explicit consent is required")
	ErrApplyUnavailable   = errors.New("setup: transactional application is unavailable for this source")
	ErrLocked             = errors.New("setup: directory is owned by another setup transaction")
	ErrWriteFailed        = errors.New("setup: transaction filesystem operation failed")
	ErrRecoveryRequired   = errors.New("setup: transaction requires explicit recovery")
	ErrRecoveryConflict   = errors.New("setup: state is unrecognized or externally changed; preserved for inspection")
	ErrInvalidTransaction = errors.New("setup: invalid transaction metadata; preserved for inspection")
	ErrBackupRetained     = errors.New("setup: retained backup must be explicitly discarded before another change")
)

// A private seam for mocked effects; native sources implement this descriptor-
// relative API without any execution, network or service operations.
type transactionStore interface {
	lock() (func(), error)
	binding() error
	rootIdentity() (FileIdentity, error)
	areaExists(string) (bool, error)
	makeArea(string) error
	readArea(string, string) (Snapshot, error)
	writeExclusive(string, string, []byte, fs.FileMode) (Snapshot, error)
	move(string, string, string, string) error // RENAME_NOREPLACE, never overwrite.
	exchange(string, string, string, string) error
	syncArea(string) error
	syncFile(string, string) error
	listArea(string) ([]string, error)
	unlinkFile(string, string) error
	removeArea(string) error
}

type ApplyResult struct {
	Changed        int  `json:"changed"`
	BackupRetained bool `json:"backup_retained"`
	Recovered      bool `json:"recovered"`
}

type TransactionStatus struct {
	State   string `json:"state"`
	Changed int    `json:"changed"`
}

type fileStamp struct {
	Exists   bool         `json:"exists"`
	Identity FileIdentity `json:"identity"`
	Owner    Owner        `json:"owner"`
	Mode     fs.FileMode  `json:"mode"`
	Size     int          `json:"size"`
	Digest   string       `json:"digest"`
}

type transactionFile struct {
	Name   string    `json:"name"`
	Action string    `json:"action"`
	Before fileStamp `json:"before"`
	After  fileStamp `json:"after"`
}

type diskTransaction struct {
	Version int               `json:"version"`
	Phase   string            `json:"phase"`
	Root    FileIdentity      `json:"root"`
	Owner   Owner             `json:"owner"`
	Files   []transactionFile `json:"files"`
}

func stamp(snapshot Snapshot) fileStamp {
	if !snapshot.Exists {
		return fileStamp{}
	}
	digest := sha256.Sum256(snapshot.Data)
	return fileStamp{Exists: true, Identity: snapshot.Identity, Owner: snapshot.Owner, Mode: snapshot.Mode, Size: len(snapshot.Data), Digest: hex.EncodeToString(digest[:])}
}

func slot(index int) string { return fmt.Sprintf("stage-%02d", index) }

func transactionError(err error) error {
	for _, known := range []error{ErrConsentRequired, ErrApplyUnavailable, ErrLocked, ErrUnsafePath, ErrStalePlan, ErrRecoveryRequired, ErrRecoveryConflict, ErrInvalidTransaction, ErrBackupRetained, ErrClosed, ErrInvalidInput, context.Canceled, context.DeadlineExceeded} {
		if errors.Is(err, known) {
			return known
		}
	}
	return ErrWriteFailed
}

func (planner *Planner) store() (transactionStore, error) {
	if planner.closed {
		return nil, ErrClosed
	}
	store, ok := planner.source.(transactionStore)
	if !ok {
		return nil, ErrApplyUnavailable
	}
	return store, nil
}

func (planner *Planner) checkpoint(ctx context.Context, store transactionStore, point string) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if err := store.binding(); err != nil {
		return transactionError(err)
	}
	if planner.fault != nil && planner.fault(point) != nil {
		return ErrWriteFailed
	}
	return nil
}

// Apply modifies files only after consent. There is no service orchestration.
// One committed backup is retained; a later mutating plan cannot erase it.
func (planner *Planner) Apply(ctx context.Context, plan *Plan, confirmed bool) (ApplyResult, error) {
	planner.mutex.Lock()
	defer planner.mutex.Unlock()
	if !confirmed {
		return ApplyResult{}, ErrConsentRequired
	}
	store, err := planner.store()
	if err != nil {
		return ApplyResult{}, err
	}
	if err := ctx.Err(); err != nil {
		return ApplyResult{}, err
	}
	unlock, err := store.lock()
	if err != nil {
		return ApplyResult{}, transactionError(err)
	}
	defer unlock()
	if err := planner.checkpoint(ctx, store, "preflight"); err != nil {
		return ApplyResult{}, err
	}
	if pending, err := store.areaExists(transactionArea); err != nil {
		return ApplyResult{}, transactionError(err)
	} else if pending {
		return ApplyResult{}, ErrRecoveryRequired
	}
	if err := planner.recheck(ctx, plan); err != nil {
		return ApplyResult{}, err
	}
	changed := 0
	for _, file := range plan.files {
		if file.change.Action != "unchanged" {
			changed++
		}
	}
	retained, err := store.areaExists(backupArea)
	if err != nil {
		return ApplyResult{}, transactionError(err)
	}
	if retained {
		record, err := readTransaction(store, backupArea, planner.owner)
		if err != nil {
			return ApplyResult{}, err
		}
		if record.Phase != "committed" {
			return ApplyResult{}, ErrRecoveryRequired
		}
		if err := verifyRetained(store, backupArea, record); err != nil {
			return ApplyResult{}, err
		}
		if changed != 0 {
			return ApplyResult{}, ErrBackupRetained
		}
	}
	if changed == 0 {
		return ApplyResult{BackupRetained: retained}, nil
	}
	root, err := store.rootIdentity()
	if err != nil {
		return ApplyResult{}, transactionError(err)
	}
	record := diskTransaction{Version: 1, Phase: "staging", Root: root, Owner: planner.owner}
	for _, file := range plan.files {
		if file.change.Action == "unchanged" {
			continue
		}
		record.Files = append(record.Files, transactionFile{Name: file.change.Name, Action: file.change.Action, Before: stamp(file.before), After: stamp(Snapshot{Exists: true, Data: file.data, Mode: fs.FileMode(file.change.Mode), Owner: planner.owner})})
	}
	if err := store.makeArea(transactionArea); err != nil {
		if exists, checkErr := store.areaExists(transactionArea); checkErr != nil || exists {
			return ApplyResult{}, errors.Join(transactionError(err), ErrRecoveryRequired)
		}
		return ApplyResult{}, transactionError(err)
	}
	operationErr := func() error {
		if err := writeTransaction(store, transactionArea, record); err != nil {
			return err
		}
		if err := store.syncArea(""); err != nil {
			return err
		}
		if err := planner.checkpoint(ctx, store, "staging"); err != nil {
			return err
		}
		index := 0
		for _, file := range plan.files {
			if file.change.Action == "unchanged" {
				continue
			}
			if err := planner.checkpoint(ctx, store, "stage:"+file.change.Name); err != nil {
				return err
			}
			staged, err := store.writeExclusive(transactionArea, slot(index), file.data, fs.FileMode(file.change.Mode))
			if err != nil {
				if staged.Exists && staged.Identity.Inode != 0 {
					record.Files[index].After.Identity = staged.Identity
					_ = writeTransaction(store, transactionArea, record)
				}
				return err
			}
			record.Files[index].After = stamp(staged)
			wanted := stamp(Snapshot{Exists: true, Data: file.data, Mode: fs.FileMode(file.change.Mode), Owner: planner.owner})
			actual := record.Files[index].After
			actual.Identity = FileIdentity{}
			if actual != wanted {
				return ErrRecoveryConflict
			}
			if err := writeTransaction(store, transactionArea, record); err != nil {
				return err
			}
			if err := planner.checkpoint(ctx, store, "staged:"+file.change.Name); err != nil {
				return err
			}
			index++
		}
		record.Phase = "prepared"
		if err := writeTransaction(store, transactionArea, record); err != nil {
			return err
		}
		if err := planner.recheck(ctx, plan); err != nil {
			return err
		}
		for _, file := range record.Files {
			if file.Before.Exists {
				if err := store.syncFile("", file.Name); err != nil {
					return err
				}
			}
		}
		for index, file := range record.Files {
			if err := planner.checkpoint(ctx, store, "commit:"+file.Name); err != nil {
				return err
			}
			current, err := store.readArea("", file.Name)
			if err != nil {
				return err
			}
			if stamp(current) != file.Before {
				return ErrStalePlan
			}
			staged, err := store.readArea(transactionArea, slot(index))
			if err != nil || stamp(staged) != file.After {
				return ErrRecoveryConflict
			}
			if file.Action == "create" {
				err = store.move(transactionArea, slot(index), "", file.Name)
			} else {
				err = store.exchange(transactionArea, slot(index), "", file.Name)
			}
			if err != nil {
				return err
			}
			if err := syncMutation(store, transactionArea); err != nil {
				return err
			}
			previous, readErr := store.readArea(transactionArea, slot(index))
			if readErr != nil || stamp(previous) != file.Before {
				// EXCHANGE preserves a racing editor's displaced entry. Restore
				// it when our candidate is still at the target, then stop.
				if file.Action == "replace" {
					current, currentErr := store.readArea("", file.Name)
					if currentErr == nil && stamp(current) == file.After {
						_ = store.exchange(transactionArea, slot(index), "", file.Name)
						_ = syncMutation(store, transactionArea)
					}
				}
				return ErrRecoveryConflict
			}
			if err := planner.checkpoint(ctx, store, "committed-file:"+file.Name); err != nil {
				return err
			}
		}
		for _, file := range record.Files {
			current, err := store.readArea("", file.Name)
			if err != nil || stamp(current) != file.After {
				return ErrRecoveryConflict
			}
		}
		for index, file := range record.Files {
			previous, err := store.readArea(transactionArea, slot(index))
			if err != nil || stamp(previous) != file.Before {
				return ErrRecoveryConflict
			}
		}
		if err := planner.checkpoint(ctx, store, "commit-marker"); err != nil {
			return err
		}
		record.Phase = "committed"
		if err := writeTransaction(store, transactionArea, record); err != nil {
			return err
		}
		if err := planner.checkpoint(ctx, store, "published"); err != nil {
			return err
		}
		return publishBackup(store, record)
	}()
	if operationErr == nil {
		return ApplyResult{Changed: changed, BackupRetained: true}, nil
	}
	// Cancellation does not prevent restoring already modified files. Inspect the
	// durable record, not the in-memory phase: the commit marker may have landed.
	status, recoveryErr := planner.recoverLocked(store)
	if recoveryErr != nil {
		return ApplyResult{}, errors.Join(transactionError(operationErr), ErrRecoveryRequired)
	}
	if status.State == "committed" {
		return ApplyResult{Changed: changed, BackupRetained: true, Recovered: true}, nil
	}
	return ApplyResult{}, transactionError(operationErr)
}

// Inspect is read-only: no locks, recovery, chmod or namespace changes.
func (planner *Planner) Inspect(ctx context.Context) (TransactionStatus, error) {
	planner.mutex.Lock()
	defer planner.mutex.Unlock()
	store, err := planner.store()
	if err != nil {
		return TransactionStatus{}, err
	}
	if err := ctx.Err(); err != nil {
		return TransactionStatus{}, err
	}
	for _, area := range []string{transactionArea, backupArea} {
		exists, err := store.areaExists(area)
		if err != nil {
			return TransactionStatus{State: "invalid"}, transactionError(err)
		}
		if !exists {
			continue
		}
		record, err := readTransaction(store, area, planner.owner)
		if err != nil {
			return TransactionStatus{State: "invalid"}, err
		}
		state := "pending"
		if record.Phase == "committed" {
			if err := verifyRetained(store, area, record); err != nil {
				return TransactionStatus{State: "invalid"}, err
			}
		}
		if area == backupArea && record.Phase == "committed" {
			state = "committed"
		}
		if record.Phase == "discarding" {
			state = "discarding"
		}
		return TransactionStatus{State: state, Changed: len(record.Files)}, nil
	}
	return TransactionStatus{State: "none"}, nil
}

func (planner *Planner) Recover(ctx context.Context, confirmed bool) (TransactionStatus, error) {
	planner.mutex.Lock()
	defer planner.mutex.Unlock()
	if !confirmed {
		return TransactionStatus{}, ErrConsentRequired
	}
	store, err := planner.store()
	if err != nil {
		return TransactionStatus{}, err
	}
	if err := ctx.Err(); err != nil {
		return TransactionStatus{}, err
	}
	unlock, err := store.lock()
	if err != nil {
		return TransactionStatus{}, transactionError(err)
	}
	defer unlock()
	return planner.recoverLocked(store)
}

func (planner *Planner) recoverLocked(store transactionStore) (TransactionStatus, error) {
	if err := store.binding(); err != nil {
		return TransactionStatus{}, transactionError(err)
	}
	exists, err := store.areaExists(transactionArea)
	if err != nil {
		return TransactionStatus{}, transactionError(err)
	}
	if !exists {
		backup, err := store.areaExists(backupArea)
		if err != nil {
			return TransactionStatus{}, transactionError(err)
		}
		if !backup {
			return TransactionStatus{State: "none"}, nil
		}
		entries, err := store.listArea(backupArea)
		if err != nil {
			return TransactionStatus{}, transactionError(err)
		}
		if len(entries) == 0 {
			if err := store.removeArea(backupArea); err != nil {
				return TransactionStatus{}, transactionError(err)
			}
			if err := store.syncArea(""); err != nil {
				return TransactionStatus{}, transactionError(err)
			}
			return TransactionStatus{State: "none"}, nil
		}
		record, err := readTransaction(store, backupArea, planner.owner)
		if err != nil {
			return TransactionStatus{}, err
		}
		if record.Phase == "discarding" {
			if err := cleanup(store, backupArea, record); err != nil {
				return TransactionStatus{}, err
			}
			return TransactionStatus{State: "none"}, nil
		}
		if record.Phase != "committed" {
			return TransactionStatus{}, ErrInvalidTransaction
		}
		if err := verifyRetained(store, backupArea, record); err != nil {
			return TransactionStatus{}, err
		}
		if err := store.syncArea(""); err != nil {
			return TransactionStatus{}, transactionError(err)
		}
		return TransactionStatus{State: "committed", Changed: len(record.Files)}, nil
	}
	entries, err := store.listArea(transactionArea)
	if err != nil {
		return TransactionStatus{}, transactionError(err)
	}
	if len(entries) == 0 {
		if err := store.removeArea(transactionArea); err != nil {
			return TransactionStatus{}, transactionError(err)
		}
		if err := store.syncArea(""); err != nil {
			return TransactionStatus{}, transactionError(err)
		}
		return TransactionStatus{State: "none"}, nil
	}
	record, err := readTransaction(store, transactionArea, planner.owner)
	if err != nil {
		return TransactionStatus{}, err
	}
	switch record.Phase {
	case "committed":
		if err := publishBackup(store, record); err != nil {
			return TransactionStatus{}, transactionError(err)
		}
		return TransactionStatus{State: "committed", Changed: len(record.Files)}, nil
	case "prepared":
		if err := rollback(store, record); err != nil {
			return TransactionStatus{}, err
		}
		record.Phase = "rolled_back"
		if err := writeTransaction(store, transactionArea, record); err != nil {
			return TransactionStatus{}, transactionError(err)
		}
	case "staging", "rolled_back":
	default:
		return TransactionStatus{}, ErrInvalidTransaction
	}
	if err := cleanup(store, transactionArea, record); err != nil {
		return TransactionStatus{}, err
	}
	return TransactionStatus{State: "none"}, nil
}

func rollback(store transactionStore, record diskTransaction) error {
	for index := len(record.Files) - 1; index >= 0; index-- {
		file := record.Files[index]
		current, err := store.readArea("", file.Name)
		if err != nil {
			return ErrRecoveryConflict
		}
		staged, err := store.readArea(transactionArea, slot(index))
		if err != nil {
			return ErrRecoveryConflict
		}
		if stamp(current) == file.Before && stamp(staged) == file.After {
			continue
		}
		if stamp(current) != file.After || stamp(staged) != file.Before {
			return ErrRecoveryConflict
		}
		if err := store.binding(); err != nil {
			return transactionError(err)
		}
		if file.Action == "create" {
			err = store.move("", file.Name, transactionArea, slot(index))
		} else {
			err = store.exchange("", file.Name, transactionArea, slot(index))
		}
		if err != nil {
			return transactionError(err)
		}
		if err := syncMutation(store, transactionArea); err != nil {
			return transactionError(err)
		}
		restored, err := store.readArea("", file.Name)
		if err != nil || stamp(restored) != file.Before {
			return ErrRecoveryConflict
		}
		captured, err := store.readArea(transactionArea, slot(index))
		if err != nil || stamp(captured) != file.After {
			// A non-cooperating editor can race the check and reverse rename.
			// Put its captured entry back without deleting it or a new target.
			if file.Action == "create" {
				_ = store.move(transactionArea, slot(index), "", file.Name)
			} else {
				_ = store.exchange(transactionArea, slot(index), "", file.Name)
			}
			_ = syncMutation(store, transactionArea)
			return ErrRecoveryConflict
		}
	}
	return nil
}

// DiscardBackup never deletes user targets and requires separate consent.
// Its durable phase makes interruption during private backup cleanup resumable.
func (planner *Planner) DiscardBackup(ctx context.Context, confirmed bool) error {
	planner.mutex.Lock()
	defer planner.mutex.Unlock()
	if !confirmed {
		return ErrConsentRequired
	}
	store, err := planner.store()
	if err != nil {
		return err
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	unlock, err := store.lock()
	if err != nil {
		return transactionError(err)
	}
	defer unlock()
	if err := store.binding(); err != nil {
		return transactionError(err)
	}
	if pending, err := store.areaExists(transactionArea); err != nil {
		return transactionError(err)
	} else if pending {
		return ErrRecoveryRequired
	}
	exists, err := store.areaExists(backupArea)
	if err != nil {
		return transactionError(err)
	}
	if !exists {
		return nil
	}
	record, err := readTransaction(store, backupArea, planner.owner)
	if err != nil {
		return err
	}
	if record.Phase != "committed" && record.Phase != "discarding" {
		return ErrInvalidTransaction
	}
	if record.Phase == "committed" {
		if err := verifyRetained(store, backupArea, record); err != nil {
			return err
		}
		record.Phase = "discarding"
		if err := writeTransaction(store, backupArea, record); err != nil {
			return transactionError(err)
		}
	}
	return cleanup(store, backupArea, record)
}

func cleanup(store transactionStore, area string, record diskTransaction) error {
	entries, err := store.listArea(area)
	if err != nil {
		return transactionError(err)
	}
	allowed := map[string]bool{manifestName: true, nextManifestName: true}
	for index := range record.Files {
		allowed[slot(index)] = true
	}
	for _, name := range entries {
		if !allowed[name] {
			return ErrRecoveryConflict
		}
	}
	for index, file := range record.Files {
		current, err := store.readArea(area, slot(index))
		if err != nil {
			return ErrRecoveryConflict
		}
		if !current.Exists {
			continue
		}
		if record.Phase != "staging" {
			expected := file.After
			if area == backupArea {
				expected = file.Before
			}
			if stamp(current) != expected {
				return ErrRecoveryConflict
			}
		} else if file.After.Identity.Inode == 0 || current.Identity != file.After.Identity {
			return ErrRecoveryConflict
		}
		if err := store.unlinkFile(area, slot(index)); err != nil {
			return transactionError(err)
		}
	}
	// Remove metadata last. A corrupt/missing main record is never interpreted
	// as an empty transaction except for an actually empty owned directory.
	if err := store.unlinkFile(area, nextManifestName); err != nil {
		return transactionError(err)
	}
	// Persist data cleanup before removing its recovery record. An empty
	// orphan directory is then distinguishable from missing/corrupt metadata.
	if err := store.syncArea(area); err != nil {
		return transactionError(err)
	}
	if err := store.unlinkFile(area, manifestName); err != nil {
		return transactionError(err)
	}
	if err := store.syncArea(area); err != nil {
		return transactionError(err)
	}
	if err := store.removeArea(area); err != nil {
		return transactionError(err)
	}
	return transactionErrorOrNil(store.syncArea(""))
}

func transactionErrorOrNil(err error) error {
	if err == nil {
		return nil
	}
	return transactionError(err)
}
func syncMutation(store transactionStore, area string) error {
	if err := store.syncArea(area); err != nil {
		return err
	}
	return store.syncArea("")
}

func verifyRetained(store transactionStore, area string, record diskTransaction) error {
	entries, err := store.listArea(area)
	if err != nil {
		return transactionError(err)
	}
	allowed := map[string]bool{manifestName: true, nextManifestName: true}
	for index, file := range record.Files {
		if file.Before.Exists {
			allowed[slot(index)] = true
		}
		current, err := store.readArea(area, slot(index))
		if err != nil || stamp(current) != file.Before {
			return ErrRecoveryConflict
		}
	}
	for _, name := range entries {
		if !allowed[name] {
			return ErrRecoveryConflict
		}
	}
	return nil
}

func publishBackup(store transactionStore, record diskTransaction) error {
	if err := verifyRetained(store, transactionArea, record); err != nil {
		return err
	}
	if err := store.binding(); err != nil {
		return err
	}
	if err := store.move("", transactionArea, "", backupArea); err != nil {
		return err
	}
	return store.syncArea("")
}

func writeTransaction(store transactionStore, area string, record diskTransaction) error {
	data, err := json.Marshal(record)
	if err != nil || len(data) > maximumManifestBytes {
		return ErrInvalidTransaction
	}
	oldNext, err := store.readArea(area, nextManifestName)
	if err != nil {
		return err
	}
	if oldNext.Exists {
		previous, err := parseTransaction(store, oldNext, record.Owner)
		if err != nil || !sameIntent(previous, record) {
			return ErrInvalidTransaction
		}
		if err := store.unlinkFile(area, nextManifestName); err != nil {
			return err
		}
	}
	created, err := store.writeExclusive(area, nextManifestName, data, 0o600)
	if err != nil {
		// Only remove the file proven to have been created by this write, never
		// a pre-existing collision or an unrecognized replacement.
		if created.Exists {
			current, readErr := store.readArea(area, nextManifestName)
			if readErr == nil && current.Identity == created.Identity {
				_ = store.unlinkFile(area, nextManifestName)
				_ = store.syncArea(area)
			}
		}
		return err
	}
	current, err := store.readArea(area, manifestName)
	if err != nil {
		return err
	}
	if current.Exists {
		err = store.exchange(area, nextManifestName, area, manifestName)
	} else {
		err = store.move(area, nextManifestName, area, manifestName)
	}
	if err != nil {
		return err
	}
	if err := store.syncArea(area); err != nil {
		return err
	}
	if err := store.unlinkFile(area, nextManifestName); err != nil {
		return err
	}
	return store.syncArea(area)
}

func readTransaction(store transactionStore, area string, owner Owner) (diskTransaction, error) {
	snapshot, err := store.readArea(area, manifestName)
	if err != nil {
		return diskTransaction{}, ErrInvalidTransaction
	}
	record, err := parseTransaction(store, snapshot, owner)
	if err != nil {
		return record, err
	}
	next, err := store.readArea(area, nextManifestName)
	if err != nil {
		return record, ErrInvalidTransaction
	}
	if next.Exists {
		companion, err := parseTransaction(store, next, owner)
		if err != nil || !sameIntent(record, companion) {
			return record, ErrInvalidTransaction
		}
		if record.Phase == "staging" {
			for index := range record.Files {
				if record.Files[index].After.Identity.Inode == 0 {
					record.Files[index].After.Identity = companion.Files[index].After.Identity
				}
			}
		}
	}
	return record, nil
}

func sameIntent(first, second diskTransaction) bool {
	if first.Root != second.Root || first.Owner != second.Owner || len(first.Files) != len(second.Files) {
		return false
	}
	for index, a := range first.Files {
		b := second.Files[index]
		a.After.Identity = FileIdentity{}
		b.After.Identity = FileIdentity{}
		if a != b {
			return false
		}
	}
	return true
}

func parseTransaction(store transactionStore, snapshot Snapshot, owner Owner) (diskTransaction, error) {
	if !snapshot.Exists || snapshot.Mode != 0o600 || snapshot.Owner != owner || len(snapshot.Data) > maximumManifestBytes {
		return diskTransaction{}, ErrInvalidTransaction
	}
	var record diskTransaction
	decoder := json.NewDecoder(bytes.NewReader(snapshot.Data))
	decoder.DisallowUnknownFields()
	if decoder.Decode(&record) != nil {
		return record, ErrInvalidTransaction
	}
	if err := decoder.Decode(new(any)); !errors.Is(err, io.EOF) {
		return record, ErrInvalidTransaction
	}
	canonical, err := json.Marshal(record)
	if err != nil || !bytes.Equal(bytes.TrimSpace(snapshot.Data), canonical) {
		return record, ErrInvalidTransaction
	}
	root, err := store.rootIdentity()
	if err != nil || record.Root != root || record.Owner != owner || record.Version != 1 || len(record.Files) == 0 || len(record.Files) > MaximumFiles {
		return record, ErrInvalidTransaction
	}
	if record.Phase != "staging" && record.Phase != "prepared" && record.Phase != "committed" && record.Phase != "rolled_back" && record.Phase != "discarding" {
		return record, ErrInvalidTransaction
	}
	beforeBytes, afterBytes := 0, 0
	for index, file := range record.Files {
		if !validName(file.Name) || index > 0 && file.Name <= record.Files[index-1].Name || file.Action != "create" && file.Action != "replace" {
			return record, ErrInvalidTransaction
		}
		if file.Action == "create" && file.Before != (fileStamp{}) || file.Action == "replace" && !validStamp(file.Before, owner, false) || !validStamp(file.After, owner, record.Phase == "staging") || !validMode(file.After.Mode) {
			return record, ErrInvalidTransaction
		}
		beforeBytes += file.Before.Size
		afterBytes += file.After.Size
		if beforeBytes > MaximumPlanBytes || afterBytes > MaximumPlanBytes {
			return record, ErrInvalidTransaction
		}
	}
	return record, nil
}

func validStamp(value fileStamp, owner Owner, unprepared bool) bool {
	if !value.Exists || value.Owner != owner || value.Mode&^fs.ModePerm != 0 || value.Mode&0o022 != 0 || value.Size < 0 || value.Size > MaximumFileBytes || len(value.Digest) != 64 {
		return false
	}
	decoded, err := hex.DecodeString(value.Digest)
	if err != nil || len(decoded) != 32 {
		return false
	}
	return unprepared || value.Identity.Inode != 0
}
