package store

import (
	"fmt"
	"os"
	"strconv"
	"strings"
)

// NextID allocates the next id under an exclusive lock. counterPath holds a
// monotonic integer; the allocator takes max(counter, existingMax)+1 so a lost
// or stale counter self-heals against the ids already on disk. The chosen
// number is persisted back atomically. Returns a formatted id like "TASK-0007".
func NextID(counterPath, lockPath, prefix string, padding, existingMax int) (string, error) {
	var id string
	err := WithLock(lockPath, func() error {
		counter := readCounter(counterPath)
		next := counter + 1
		if existingMax+1 > next {
			next = existingMax + 1
		}
		if err := WriteAtomic(counterPath, []byte(strconv.Itoa(next)+"\n"), 0o644); err != nil {
			return err
		}
		id = FormatID(prefix, padding, next)
		return nil
	})
	return id, err
}

func readCounter(path string) int {
	data, err := os.ReadFile(path)
	if err != nil {
		return 0
	}
	n, err := strconv.Atoi(strings.TrimSpace(string(data)))
	if err != nil {
		return 0
	}
	return n
}

// FormatID renders a zero-padded id such as "TASK-0007".
func FormatID(prefix string, padding, n int) string {
	return fmt.Sprintf("%s-%0*d", prefix, padding, n)
}

// ParseIDNumber extracts the numeric suffix of an id like "TASK-0007" → 7.
// Returns false if the id does not carry the given prefix.
func ParseIDNumber(prefix, id string) (int, bool) {
	want := prefix + "-"
	if !strings.HasPrefix(id, want) {
		return 0, false
	}
	n, err := strconv.Atoi(id[len(want):])
	if err != nil {
		return 0, false
	}
	return n, true
}
