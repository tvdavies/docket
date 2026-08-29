package store_test

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/tvdavies/docket/internal/store"
)

func TestAppendLinesAtReturnsCommittedGroupBoundary(t *testing.T) {
	path := filepath.Join(t.TempDir(), "events.jsonl")
	first, err := store.AppendLinesAt(path, [][]byte{[]byte("one"), []byte("two")})
	if err != nil {
		t.Fatal(err)
	}
	second, err := store.AppendLinesAt(path, [][]byte{[]byte("three")})
	if err != nil {
		t.Fatal(err)
	}
	if first != int64(len("one\ntwo\n")) || second != int64(len("one\ntwo\nthree\n")) {
		t.Fatalf("offsets = %d, %d", first, second)
	}
}

func TestAppendLinesKeepsConcurrentGroupsContiguous(t *testing.T) {
	path := filepath.Join(t.TempDir(), "events.jsonl")
	const writers = 24
	var wait sync.WaitGroup
	errors := make(chan error, writers)
	for index := 0; index < writers; index++ {
		wait.Add(1)
		go func(index int) {
			defer wait.Done()
			errors <- store.AppendLines(path, [][]byte{
				[]byte(fmt.Sprintf("%02d-a", index)),
				[]byte(fmt.Sprintf("%02d-b", index)),
			})
		}(index)
	}
	wait.Wait()
	close(errors)
	for err := range errors {
		if err != nil {
			t.Fatal(err)
		}
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	lines := strings.Split(strings.TrimSpace(string(data)), "\n")
	if len(lines) != writers*2 {
		t.Fatalf("line count = %d", len(lines))
	}
	for index := 0; index < len(lines); index += 2 {
		if strings.TrimSuffix(lines[index], "-a") != strings.TrimSuffix(lines[index+1], "-b") {
			t.Fatalf("event group interleaved at %d: %q, %q", index, lines[index], lines[index+1])
		}
	}
}
