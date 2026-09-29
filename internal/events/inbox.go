package events

import (
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/tvdavies/docket/internal/store"
	"github.com/tvdavies/docket/internal/workspace"
)

// InboxOptions configures an inbox read.
type InboxOptions struct {
	Actor    string // recipient; events on tasks assigned to this actor
	All      bool   // ignore the assignee filter — every unread event
	MarkRead bool   // advance the cursor past everything read
}

// afterInboxSnapshot runs between an inbox snapshot and its acknowledgement.
// Tests use it to append an event deterministically inside that window.
var afterInboxSnapshot = func() {}

// Inbox returns unread events for an actor since their last cursor. With
// MarkRead, the cursor advances to the end of the same snapshot that produced
// the returned events, so an event appended after the read stays unread. This
// is immediate read acknowledgement; durable consumers use PeekInbox and
// AckInbox instead.
func Inbox(ws *workspace.Workspace, opts InboxOptions) ([]Event, error) {
	var out []Event
	read := func() error {
		snapshot, err := ReadSnapshot(ws, Cursor(ws, opts.Actor))
		if err != nil {
			return err
		}
		out = filterInbox(snapshot.Events, opts.Actor, opts.All)
		if !opts.MarkRead {
			return nil
		}
		afterInboxSnapshot()
		return writeInboxCursor(ws, opts.Actor, snapshot.End, snapshot.EndHash)
	}
	if !opts.MarkRead {
		return out, read()
	}
	// The actor lock serialises read-and-advance so concurrent readers cannot
	// move the cursor backwards over each other's acknowledgements.
	return out, store.WithLock(inboxLockFile(ws, opts.Actor), read)
}

func filterInbox(evs []Event, actor string, all bool) []Event {
	var out []Event
	for _, ev := range evs {
		if all || ev.Assignee == actor {
			out = append(out, ev)
		}
	}
	return out
}

// ErrInboxHistoryChanged reports that the event log no longer matches an
// actor's acknowledged position or a checkpoint's read boundary. A durable
// consumer must recover explicitly (ResetInbox) rather than acknowledge
// replacement history it never received.
var ErrInboxHistoryChanged = errors.New("event log history changed since the inbox position was acknowledged")

// ErrInboxCheckpointStale reports a checkpoint whose starting position no
// longer matches the actor's cursor, so applying it could skip or replay
// events.
var ErrInboxCheckpointStale = errors.New("inbox checkpoint is stale")

// InboxBatch is one durable inbox read. Checkpoint is an opaque token that
// AckInbox accepts after the consumer has durably recorded Events.
type InboxBatch struct {
	Actor      string  `json:"actor"`
	All        bool    `json:"all"`
	Events     []Event `json:"events"`
	From       int     `json:"from"`
	To         int     `json:"to"`
	Checkpoint string  `json:"checkpoint"`
}

// inboxToken is the decoded checkpoint. Positions are physical non-empty line
// counts, matching Cursor, never advisory event sequence numbers.
type inboxToken struct {
	Version   int    `json:"v"`
	Workspace string `json:"ws"`
	Actor     string `json:"actor"`
	Mode      string `json:"mode"`
	From      int    `json:"from"`
	FromHash  string `json:"from_hash,omitempty"`
	To        int    `json:"to"`
	ToHash    string `json:"to_hash,omitempty"`
}

const (
	inboxTokenVersion = 1
	inboxTokenPrefix  = "dkinbox1."
)

func inboxMode(all bool) string {
	if all {
		return "all"
	}
	return "assigned"
}

// PeekInbox returns unread events and a checkpoint without moving the cursor.
// Repeating the call before acknowledgement returns the same events plus any
// appended since. It fails with ErrInboxHistoryChanged if the log was
// truncated or rewritten beneath the acknowledged position.
func PeekInbox(ws *workspace.Workspace, actor string, all bool) (InboxBatch, error) {
	var batch InboxBatch
	err := store.WithLock(inboxLockFile(ws, actor), func() error {
		position, verifiedHash, verified := inboxPosition(ws, actor)
		snapshot, err := ReadSnapshot(ws, position)
		if err != nil {
			return err
		}
		if snapshot.Found < position {
			return fmt.Errorf("%w: inbox cursor %d is beyond the log's %d records", ErrInboxHistoryChanged, position, snapshot.Found)
		}
		if verified && snapshot.StartHash != verifiedHash {
			return fmt.Errorf("%w: records before inbox cursor %d were rewritten", ErrInboxHistoryChanged, position)
		}
		token := inboxToken{
			Version:   inboxTokenVersion,
			Workspace: workspaceIdentity(ws),
			Actor:     actor,
			Mode:      inboxMode(all),
			From:      position,
			FromHash:  snapshot.StartHash,
			To:        snapshot.End,
			ToHash:    snapshot.EndHash,
		}
		encoded, err := encodeInboxToken(token)
		if err != nil {
			return err
		}
		events := filterInbox(snapshot.Events, actor, all)
		if events == nil {
			events = []Event{}
		}
		batch = InboxBatch{Actor: actor, All: all, Events: events, From: position, To: snapshot.End, Checkpoint: encoded}
		return nil
	})
	return batch, err
}

// InboxAck describes an applied acknowledgement.
type InboxAck struct {
	Position int  `json:"position"`
	Applied  bool `json:"applied"` // false when the cursor already covered this checkpoint
}

// AckInbox advances an actor's cursor to a checkpoint from PeekInbox. It
// validates the workspace, actor, filter mode, exact log prefix, and that the
// cursor is still where the read began. Acknowledging a checkpoint the cursor
// already covers succeeds without moving backwards.
func AckInbox(ws *workspace.Workspace, actor string, all bool, checkpoint string) (InboxAck, error) {
	token, err := decodeInboxToken(checkpoint)
	if err != nil {
		return InboxAck{}, err
	}
	if identity := workspaceIdentity(ws); token.Workspace != identity {
		return InboxAck{}, fmt.Errorf("inbox checkpoint belongs to workspace %s, not %s", token.Workspace, identity)
	}
	if token.Actor != actor {
		return InboxAck{}, fmt.Errorf("inbox checkpoint belongs to actor %q, not %q", token.Actor, actor)
	}
	if token.Mode != inboxMode(all) {
		return InboxAck{}, fmt.Errorf("inbox checkpoint was read in %q mode; acknowledge it with the same filter (--all only if it was read with --all)", token.Mode)
	}
	var result InboxAck
	err = store.WithLock(inboxLockFile(ws, actor), func() error {
		hash, found, err := PrefixHash(ws, token.To)
		if err != nil {
			return err
		}
		if found < token.To || hash != token.ToHash {
			return fmt.Errorf("%w: the records read for this checkpoint are no longer the log prefix", ErrInboxHistoryChanged)
		}
		position, verifiedHash, verified := inboxPosition(ws, actor)
		switch {
		case position >= token.To:
			// Already acknowledged (a duplicate, or a later checkpoint already
			// applied). The prefix check above guarantees the cursor still
			// refers to the history this checkpoint read.
			result = InboxAck{Position: position}
			return nil
		case position != token.From:
			return fmt.Errorf("%w: it starts at position %d but the inbox cursor is at %d; read the inbox again", ErrInboxCheckpointStale, token.From, position)
		case verified && verifiedHash != token.FromHash:
			return fmt.Errorf("%w: the inbox cursor was acknowledged against different history", ErrInboxCheckpointStale)
		}
		if err := writeInboxCursor(ws, actor, token.To, token.ToHash); err != nil {
			return err
		}
		result = InboxAck{Position: token.To, Applied: true}
		return nil
	})
	return result, err
}

// ResetInbox is explicit recovery for a durable consumer after history
// changed: the cursor returns to the start of the log, so every current event
// is offered again and the consumer's deduplication absorbs repeats.
func ResetInbox(ws *workspace.Workspace, actor string) error {
	return store.WithLock(inboxLockFile(ws, actor), func() error {
		return writeInboxCursor(ws, actor, 0, "")
	})
}

// workspaceIdentity binds checkpoints to one store regardless of the symlink
// path used to open it.
func workspaceIdentity(ws *workspace.Workspace) string {
	if resolved, err := filepath.EvalSymlinks(ws.Root); err == nil {
		return resolved
	}
	return ws.Root
}

func encodeInboxToken(token inboxToken) (string, error) {
	data, err := json.Marshal(token)
	if err != nil {
		return "", err
	}
	return inboxTokenPrefix + base64.RawURLEncoding.EncodeToString(data), nil
}

func decodeInboxToken(value string) (inboxToken, error) {
	var token inboxToken
	encoded, ok := strings.CutPrefix(strings.TrimSpace(value), inboxTokenPrefix)
	if !ok {
		return token, fmt.Errorf("unrecognised inbox checkpoint format")
	}
	data, err := base64.RawURLEncoding.DecodeString(encoded)
	if err != nil {
		return token, fmt.Errorf("malformed inbox checkpoint: %w", err)
	}
	decoder := json.NewDecoder(strings.NewReader(string(data)))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&token); err != nil {
		return token, fmt.Errorf("malformed inbox checkpoint: %w", err)
	}
	if token.Version != inboxTokenVersion {
		return token, fmt.Errorf("unsupported inbox checkpoint version %d", token.Version)
	}
	if token.From < 0 || token.To < token.From || (token.To > 0 && token.ToHash == "") || (token.From > 0 && token.FromHash == "") {
		return token, fmt.Errorf("malformed inbox checkpoint: invalid positions")
	}
	return token, nil
}

// cursorFile maps an actor id to its cursor path, sanitising the name.
func cursorFile(ws *workspace.Workspace, actor string) string {
	safe := strings.Map(func(r rune) rune {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9', r == '-', r == '_':
			return r
		default:
			return '_'
		}
	}, actor)
	if safe == "" {
		safe = "default"
	}
	return filepath.Join(ws.CursorsDir(), safe+".cursor")
}

// inboxCheckpointFile stores the prefix hash for the position in the plain
// numeric cursor file. The cursor file keeps its original format so older
// binaries (and existing callers of Cursor) read it unchanged; a checkpoint
// whose position disagrees with the cursor is ignored as unverified.
func inboxCheckpointFile(ws *workspace.Workspace, actor string) string {
	return strings.TrimSuffix(cursorFile(ws, actor), ".cursor") + ".checkpoint"
}

func inboxLockFile(ws *workspace.Workspace, actor string) string {
	return cursorFile(ws, actor) + ".lock"
}

// Cursor returns a consumer's current event-log position. Missing or invalid
// cursor files start at zero, so a newly registered consumer sees history.
func Cursor(ws *workspace.Workspace, actor string) int {
	data, err := os.ReadFile(cursorFile(ws, actor))
	if err != nil {
		return 0
	}
	n, err := strconv.Atoi(strings.TrimSpace(string(data)))
	if err != nil {
		return 0
	}
	return n
}

type inboxCheckpointState struct {
	Position   int    `json:"position"`
	PrefixHash string `json:"prefix_hash"`
}

// inboxPosition returns the actor's cursor and, when a matching checkpoint
// exists, the prefix hash it was acknowledged against. Cursors written by
// earlier versions have no checkpoint and are accepted unverified; the next
// acknowledgement records one (lazy migration).
func inboxPosition(ws *workspace.Workspace, actor string) (int, string, bool) {
	position := Cursor(ws, actor)
	data, err := os.ReadFile(inboxCheckpointFile(ws, actor))
	if err != nil {
		return position, "", false
	}
	var state inboxCheckpointState
	if json.Unmarshal(data, &state) != nil || state.Position != position {
		return position, "", false
	}
	return position, state.PrefixHash, true
}

func writeInboxCursor(ws *workspace.Workspace, actor string, position int, prefixHash string) error {
	if err := AdvanceCursor(ws, actor, position); err != nil {
		return err
	}
	data, err := json.Marshal(inboxCheckpointState{Position: position, PrefixHash: prefixHash})
	if err != nil {
		return err
	}
	return store.WriteAtomic(inboxCheckpointFile(ws, actor), append(data, '\n'), 0o644)
}

// AdvanceCursor durably records a consumer's event-log position.
func AdvanceCursor(ws *workspace.Workspace, actor string, n int) error {
	return store.WriteAtomic(cursorFile(ws, actor), []byte(strconv.Itoa(n)+"\n"), 0o644)
}
