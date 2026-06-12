package store

import (
	"bytes"
	"fmt"

	"gopkg.in/yaml.v3"
)

var fence = []byte("---")

// SplitFrontmatter separates a YAML frontmatter block (delimited by leading
// `---` fences) from the markdown body that follows. If the document has no
// frontmatter, meta is empty and the whole input is returned as body.
func SplitFrontmatter(data []byte) (meta []byte, body []byte) {
	trimmed := bytes.TrimLeft(data, "\n\r ")
	if !bytes.HasPrefix(trimmed, fence) {
		return nil, data
	}
	// Find the closing fence after the opening one.
	rest := trimmed[len(fence):]
	// Skip to end of the opening fence line.
	if i := bytes.IndexByte(rest, '\n'); i >= 0 {
		rest = rest[i+1:]
	} else {
		return nil, data
	}
	// Look for a line that is exactly `---`.
	idx := findClosingFence(rest)
	if idx < 0 {
		return nil, data
	}
	meta = rest[:idx]
	body = rest[idx:]
	// Drop the closing fence line from body.
	if i := bytes.IndexByte(body, '\n'); i >= 0 {
		body = body[i+1:]
	} else {
		body = nil
	}
	body = bytes.TrimLeft(body, "\n")
	return meta, body
}

func findClosingFence(data []byte) int {
	offset := 0
	for offset < len(data) {
		lineEnd := bytes.IndexByte(data[offset:], '\n')
		var line []byte
		if lineEnd < 0 {
			line = data[offset:]
		} else {
			line = data[offset : offset+lineEnd]
		}
		if bytes.Equal(bytes.TrimRight(line, "\r "), fence) {
			return offset
		}
		if lineEnd < 0 {
			break
		}
		offset += lineEnd + 1
	}
	return -1
}

// ParseFrontmatter splits the document and unmarshals the frontmatter into v.
func ParseFrontmatter(data []byte, v any) (body string, err error) {
	meta, b := SplitFrontmatter(data)
	if len(bytes.TrimSpace(meta)) > 0 {
		if err := yaml.Unmarshal(meta, v); err != nil {
			return "", fmt.Errorf("parse frontmatter: %w", err)
		}
	}
	return string(b), nil
}

// RenderFrontmatter serialises v as a YAML frontmatter block followed by the
// markdown body, producing a document ParseFrontmatter can round-trip.
func RenderFrontmatter(v any, body string) ([]byte, error) {
	meta, err := yaml.Marshal(v)
	if err != nil {
		return nil, err
	}
	var buf bytes.Buffer
	buf.Write(fence)
	buf.WriteByte('\n')
	buf.Write(meta)
	buf.Write(fence)
	buf.WriteByte('\n')
	if body != "" {
		buf.WriteByte('\n')
		buf.WriteString(body)
		if body[len(body)-1] != '\n' {
			buf.WriteByte('\n')
		}
	}
	return buf.Bytes(), nil
}
