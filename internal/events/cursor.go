package events

import (
	"bufio"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"os"

	"github.com/tvdavies/docket/internal/workspace"
)

// LogCursor is an exact physical byte boundary in events.jsonl. Service-layer
// cursors wrap it in an opaque runtime generation token before exposing it.
type LogCursor struct {
	Offset     int64
	PrefixHash string
}

// LogRecord is one valid event together with the byte offset immediately after
// its newline. Byte offsets follow physical log order and deliberately do not
// rely on advisory event sequence numbers.
type LogRecord struct {
	Event      Event
	Offset     int64
	PrefixHash string
	Reset      bool
}

// LogSize returns the current byte length of events.jsonl. A missing log is an
// empty log.
func LogSize(ws *workspace.Workspace) (int64, error) {
	info, err := os.Stat(ws.EventsFile())
	if err != nil {
		if os.IsNotExist(err) {
			return 0, nil
		}
		return 0, err
	}
	return info.Size(), nil
}

// CurrentLogCursor returns the current complete physical log boundary and its
// prefix checkpoint. Append operations always end in a newline.
func CurrentLogCursor(ws *workspace.Workspace) (LogCursor, error) {
	file, err := os.Open(ws.EventsFile())
	if err != nil {
		if os.IsNotExist(err) {
			return LogCursor{}, nil
		}
		return LogCursor{}, err
	}
	defer file.Close()
	reader := bufio.NewReader(file)
	hash := sha256.New()
	var offset int64
	for {
		line, readErr := reader.ReadBytes('\n')
		if len(line) > 0 && line[len(line)-1] == '\n' {
			offset += int64(len(line))
			_, _ = hash.Write(line)
		}
		if readErr != nil {
			if readErr != io.EOF {
				return LogCursor{}, readErr
			}
			break
		}
	}
	if offset == 0 {
		return LogCursor{}, nil
	}
	return LogCursor{Offset: offset, PrefixHash: hex.EncodeToString(hash.Sum(nil))}, nil
}

// PrefixHashBytes returns a SHA-256 checkpoint for exactly offset bytes.
func PrefixHashBytes(ws *workspace.Workspace, offset int64) (string, error) {
	if offset == 0 {
		return "", nil
	}
	file, err := os.Open(ws.EventsFile())
	if err != nil {
		return "", err
	}
	defer file.Close()
	hash := sha256.New()
	written, err := io.CopyN(hash, file, offset)
	if err != nil || written != offset {
		if err == nil {
			err = io.ErrUnexpectedEOF
		}
		return "", err
	}
	return hex.EncodeToString(hash.Sum(nil)), nil
}

// ValidateLogCursor verifies both the byte boundary and exact prefix content.
func ValidateLogCursor(ws *workspace.Workspace, cursor LogCursor) error {
	file, err := os.Open(ws.EventsFile())
	if err != nil {
		if os.IsNotExist(err) && cursor.Offset == 0 {
			return nil
		}
		return err
	}
	info, err := file.Stat()
	if err != nil {
		_ = file.Close()
		return err
	}
	if cursor.Offset < 0 || cursor.Offset > info.Size() {
		_ = file.Close()
		return fmt.Errorf("event cursor offset %d is outside log size %d", cursor.Offset, info.Size())
	}
	if cursor.Offset > 0 {
		boundary := []byte{0}
		if _, err := file.ReadAt(boundary, cursor.Offset-1); err != nil {
			_ = file.Close()
			return err
		}
		if boundary[0] != '\n' {
			_ = file.Close()
			return fmt.Errorf("event cursor offset %d is not at a line boundary", cursor.Offset)
		}
	}
	_ = file.Close()
	if cursor.Offset == 0 {
		return nil
	}
	value, err := PrefixHashBytes(ws, cursor.Offset)
	if err != nil {
		return err
	}
	if value != cursor.PrefixHash {
		return fmt.Errorf("event cursor prefix does not match current log")
	}
	return nil
}

// ReadFromOffset reads complete event lines after offset and returns the byte
// offset of every valid event. The offset must be at a line boundary; malformed
// lines are skipped while later valid records retain their physical offsets.
func ReadFromOffset(ws *workspace.Workspace, offset int64) ([]LogRecord, int64, error) {
	path := ws.EventsFile()
	file, err := os.Open(path)
	if err != nil {
		if os.IsNotExist(err) && offset == 0 {
			return nil, 0, nil
		}
		return nil, offset, err
	}
	defer file.Close()
	info, err := file.Stat()
	if err != nil {
		return nil, offset, err
	}
	if offset < 0 || offset > info.Size() {
		return nil, offset, fmt.Errorf("event cursor offset %d is outside log size %d", offset, info.Size())
	}
	if offset > 0 {
		boundary := []byte{0}
		if _, err := file.ReadAt(boundary, offset-1); err != nil {
			return nil, offset, err
		}
		if boundary[0] != '\n' {
			return nil, offset, fmt.Errorf("event cursor offset %d is not at a line boundary", offset)
		}
	}
	hash := sha256.New()
	if offset > 0 {
		if _, err := file.Seek(0, io.SeekStart); err != nil {
			return nil, offset, err
		}
		if _, err := io.CopyN(hash, file, offset); err != nil {
			return nil, offset, err
		}
	}
	if _, err := file.Seek(offset, io.SeekStart); err != nil {
		return nil, offset, err
	}
	reader := bufio.NewReader(file)
	position := offset
	records := make([]LogRecord, 0)
	for {
		line, readErr := reader.ReadBytes('\n')
		if len(line) > 0 && line[len(line)-1] == '\n' {
			position += int64(len(line))
			_, _ = hash.Write(line)
			var event Event
			if json.Unmarshal(line[:len(line)-1], &event) == nil {
				records = append(records, LogRecord{Event: event, Offset: position, PrefixHash: hex.EncodeToString(hash.Sum(nil))})
			}
		}
		if readErr != nil {
			if readErr == io.EOF {
				return records, position, nil
			}
			return nil, position, readErr
		}
	}
}
