// Package setup provides local provisioning primitives. Planning never writes
// files, generates credentials, executes commands or changes service state.
package setup

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"io/fs"
	"sort"
	"strings"
	"sync"
)

const (
	MaximumFiles     = 32
	MaximumFileBytes = 1 << 20
	MaximumPlanBytes = 8 << 20
)

var (
	ErrInvalidInput        = errors.New("setup: invalid or oversized input")
	ErrInvalidExisting     = errors.New("setup: existing file failed validation")
	ErrExistingConflict    = errors.New("setup: existing file differs; replacement was not explicitly selected")
	ErrUnsafePath          = errors.New("setup: unsafe path, file type, permissions or ownership")
	ErrReadFailed          = errors.New("setup: file cannot be safely read")
	ErrMissingDirectory    = errors.New("setup: target directory is absent; planning does not create it")
	ErrStalePlan           = errors.New("setup: filesystem state changed since planning")
	ErrClosed              = errors.New("setup: planner is closed")
	ErrUnsupportedPlatform = errors.New("setup: native provisioning source is supported only on Linux")
)

type Owner struct{ UID, GID uint32 }

// FileIdentity is source-defined stable metadata, not a public preview field.
type FileIdentity struct{ Device, Inode uint64 }

// Snapshot contains private source bytes; it is not a user-visible report.
type Snapshot struct {
	Exists   bool
	Data     []byte
	Mode     fs.FileMode
	Owner    Owner
	Identity FileIdentity
}

// Source must be bounded, read-only and reject symlinks/non-regular files.
// Native implementations pin a private, explicitly owned directory descriptor.
type Source interface {
	Owner() Owner
	Read(name string, limit int64) (Snapshot, error)
	Close() error
}

// Intent is a developer-supplied local input, not a remote command or report.
// Validate must be a pure semantic validator, not an executable/script probe.
// AllowReplace selects replacement in the plan only; it is not write consent.
type Intent struct {
	Name         string
	Data         []byte
	Mode         fs.FileMode
	AllowReplace bool
	Validate     func([]byte) error
}

type Change struct {
	Name   string `json:"name"`
	Action string `json:"action"`
	Mode   uint32 `json:"mode"`
}

type Preview struct {
	ReadOnly bool     `json:"read_only"`
	Changes  []Change `json:"changes"`
}

type plannedFile struct {
	change Change
	before Snapshot
	data   []byte
}

// Plan deliberately keeps file contents and validators out of exported reports.
// Applying it requires explicit consent and a transactional native source.
type Plan struct {
	planner *Planner
	files   []plannedFile
}

func (plan *Plan) Preview() Preview {
	preview := Preview{ReadOnly: true, Changes: []Change{}}
	if plan != nil {
		for _, file := range plan.files {
			preview.Changes = append(preview.Changes, file.change)
		}
	}
	return preview
}

func (plan Plan) MarshalJSON() ([]byte, error) { return json.Marshal(plan.Preview()) }
func (plan Plan) String() string               { data, _ := plan.MarshalJSON(); return string(data) }
func (plan Plan) GoString() string             { return plan.String() }

type Planner struct {
	mutex  sync.Mutex
	source Source
	owner  Owner
	closed bool
	fault  func(string) error // Test seam only; never exposes arbitrary errors.
}

func NewPlanner(source Source) (*Planner, error) {
	if source == nil {
		return nil, ErrInvalidInput
	}
	return &Planner{source: source, owner: source.Owner()}, nil
}

func OpenPlanner(directory string, owner Owner) (*Planner, error) {
	source, err := openNativeSource(directory, owner)
	if err != nil {
		return nil, err
	}
	return NewPlanner(source)
}

// OpenPlannerTrustingOwnerGroup is an explicit user-profile exception, never a
// global relaxation. The final directory/files remain private; only ancestors
// owned by the non-root user and their primary group may be group-writable.
func OpenPlannerTrustingOwnerGroup(directory string, owner Owner) (*Planner, error) {
	source, err := openNativeSourceWithTrust(directory, owner, true)
	if err != nil {
		return nil, err
	}
	return NewPlanner(source)
}

func (planner *Planner) Close() error {
	planner.mutex.Lock()
	defer planner.mutex.Unlock()
	if planner.closed {
		return nil
	}
	planner.closed = true
	if err := planner.source.Close(); err != nil {
		return ErrReadFailed
	}
	return nil
}

func (planner *Planner) Build(ctx context.Context, intents []Intent) (*Plan, error) {
	planner.mutex.Lock()
	defer planner.mutex.Unlock()
	if planner.closed {
		return nil, ErrClosed
	}
	if len(intents) == 0 || len(intents) > MaximumFiles {
		return nil, ErrInvalidInput
	}
	intents = append([]Intent(nil), intents...)
	sort.Slice(intents, func(i, j int) bool { return intents[i].Name < intents[j].Name })
	inputBytes := 0
	for index, intent := range intents {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		if !validName(intent.Name) || (index > 0 && intent.Name == intents[index-1].Name) || !validMode(intent.Mode) || intent.Validate == nil || len(intent.Data) > MaximumFileBytes {
			return nil, ErrInvalidInput
		}
		inputBytes += len(intent.Data)
		if inputBytes > MaximumPlanBytes {
			return nil, ErrInvalidInput
		}
		intents[index].Data = bytes.Clone(intent.Data)
		if intent.Validate(bytes.Clone(intents[index].Data)) != nil {
			return nil, ErrInvalidInput
		}
	}
	plan := &Plan{planner: planner}
	existingBytes := 0
	for _, intent := range intents {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		before, err := planner.read(intent.Name)
		if err != nil {
			return nil, err
		}
		existingBytes += len(before.Data)
		if existingBytes > MaximumPlanBytes {
			return nil, ErrInvalidExisting
		}
		action := "create"
		if before.Exists {
			if intent.Validate(bytes.Clone(before.Data)) != nil {
				return nil, ErrInvalidExisting
			}
			if bytes.Equal(before.Data, intent.Data) && before.Mode == intent.Mode {
				action = "unchanged"
			} else {
				if !intent.AllowReplace {
					return nil, ErrExistingConflict
				}
				action = "replace"
			}
		}
		plan.files = append(plan.files, plannedFile{change: Change{Name: intent.Name, Action: action, Mode: uint32(intent.Mode)}, before: before, data: intent.Data})
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	return plan, nil
}

// Recheck detects drift without creating locks/backups/journals or writing files.
// It is not an atomic write precondition; future Apply must recheck under a lock.
func (planner *Planner) Recheck(ctx context.Context, plan *Plan) error {
	planner.mutex.Lock()
	defer planner.mutex.Unlock()
	return planner.recheck(ctx, plan)
}

func (planner *Planner) recheck(ctx context.Context, plan *Plan) error {
	if planner.closed {
		return ErrClosed
	}
	if plan == nil || plan.planner != planner {
		return ErrInvalidInput
	}
	for _, file := range plan.files {
		if err := ctx.Err(); err != nil {
			return err
		}
		current, err := planner.read(file.change.Name)
		if err != nil {
			return err
		}
		if current.Exists != file.before.Exists || current.Mode != file.before.Mode || current.Owner != file.before.Owner || current.Identity != file.before.Identity || !bytes.Equal(current.Data, file.before.Data) {
			return ErrStalePlan
		}
	}
	return ctx.Err()
}

func (planner *Planner) read(name string) (Snapshot, error) {
	return planner.readLimit(name, MaximumFileBytes)
}

// Snapshot returns private bounded local source bytes, never a public report.
func (planner *Planner) Snapshot(name string, limit int64) (Snapshot, error) {
	planner.mutex.Lock()
	defer planner.mutex.Unlock()
	if planner.closed {
		return Snapshot{}, ErrClosed
	}
	if !validName(name) || limit <= 0 || limit > MaximumFileBytes {
		return Snapshot{}, ErrInvalidInput
	}
	return planner.readLimit(name, limit)
}

func (planner *Planner) readLimit(name string, limit int64) (Snapshot, error) {
	snapshot, err := planner.source.Read(name, limit)
	if err != nil {
		if errors.Is(err, ErrUnsafePath) {
			return Snapshot{}, ErrUnsafePath
		}
		if errors.Is(err, ErrInvalidExisting) {
			return Snapshot{}, ErrInvalidExisting
		}
		return Snapshot{}, ErrReadFailed
	}
	if !snapshot.Exists {
		if len(snapshot.Data) != 0 || snapshot.Mode != 0 || snapshot.Owner != (Owner{}) || snapshot.Identity != (FileIdentity{}) {
			return Snapshot{}, ErrReadFailed
		}
		return Snapshot{}, nil
	}
	if snapshot.Owner != planner.owner || snapshot.Mode&^fs.ModePerm != 0 || snapshot.Mode&0o022 != 0 {
		return Snapshot{}, ErrUnsafePath
	}
	if int64(len(snapshot.Data)) > limit {
		return Snapshot{}, ErrInvalidExisting
	}
	snapshot.Data = bytes.Clone(snapshot.Data)
	return snapshot, nil
}

func validMode(mode fs.FileMode) bool { return mode == 0o600 || mode == 0o644 }

func validName(name string) bool {
	if name == "" || name == "." || name == ".." || len(name) > 128 || strings.HasPrefix(name, ".hlcp-") {
		return false
	}
	for _, ch := range name {
		if ch >= 'a' && ch <= 'z' || ch >= 'A' && ch <= 'Z' || ch >= '0' && ch <= '9' || ch == '.' || ch == '_' || ch == '-' {
			continue
		}
		return false
	}
	return true
}

// ReadPrivateInput is for bounded stdin/file secret input, never argv or env.
// It does not trim, parse, display or log contents and reports controlled errors.
func ReadPrivateInput(reader io.Reader, limit int64) ([]byte, error) {
	if reader == nil || limit <= 0 || limit > MaximumFileBytes {
		return nil, ErrInvalidInput
	}
	data, err := io.ReadAll(io.LimitReader(reader, limit+1))
	if err != nil || int64(len(data)) > limit {
		return nil, ErrInvalidInput
	}
	return data, nil
}
