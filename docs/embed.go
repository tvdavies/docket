// Package docs embeds Docket's reference documentation so the binary can print
// the docs that match its own version (`docket docs [topic]`).
package docs

import (
	"embed"
	"io/fs"
	"path"
	"sort"
	"strings"
)

//go:embed *.md plugins/*.md plugin-sdk.d.ts
var files embed.FS

// Topic is one embedded document, addressed by its path without extension.
type Topic struct {
	Name  string `json:"name"`
	Title string `json:"title"`
	File  string `json:"file"`
}

// Topics lists every embedded document, sorted by name.
func Topics() []Topic {
	var topics []Topic
	_ = fs.WalkDir(files, ".", func(file string, entry fs.DirEntry, err error) error {
		if err != nil || entry.IsDir() {
			return err
		}
		data, _ := files.ReadFile(file)
		topics = append(topics, Topic{Name: topicName(file), Title: title(file, string(data)), File: "docs/" + file})
		return nil
	})
	sort.Slice(topics, func(i, j int) bool { return topics[i].Name < topics[j].Name })
	return topics
}

// Read returns a document by topic name. Names are matched with or without
// their extension, so "plugins/authoring" and "plugins/authoring.md" agree.
func Read(name string) (Topic, string, bool) {
	name = strings.TrimPrefix(strings.TrimSpace(name), "docs/")
	for _, topic := range Topics() {
		if topic.Name == name || topic.Name == topicName(name) {
			data, err := files.ReadFile(strings.TrimPrefix(topic.File, "docs/"))
			return topic, string(data), err == nil
		}
	}
	return Topic{}, "", false
}

func topicName(file string) string {
	if strings.HasSuffix(file, ".d.ts") {
		return file
	}
	return strings.TrimSuffix(file, path.Ext(file))
}

func title(file, content string) string {
	if file == "plugin-sdk.d.ts" {
		return "Plugin SDK TypeScript declarations"
	}
	for _, line := range strings.Split(content, "\n") {
		if heading, ok := strings.CutPrefix(line, "# "); ok {
			return strings.TrimSpace(heading)
		}
	}
	return path.Base(file)
}
