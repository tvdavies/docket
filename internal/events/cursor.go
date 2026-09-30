package events

import (
	"bufio"
	"crypto/sha256"
	"encoding/hex"
	"io"
	"os"

	"github.com/tvdavies/docket/internal/workspace"
)

// LogCursor is an exact physical byte boundary and prefix checkpoint in events.jsonl.
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
