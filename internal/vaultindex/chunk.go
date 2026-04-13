package vaultindex

import (
	"strings"
)

const maxChunkRunes = 1800
const overlapRunes = 120

// Chunk is one searchable slice of a Markdown file.
type Chunk struct {
	Index   int
	Heading string
	Text    string
}

// ChunkMarkdown splits note text into heading-based sections, then subdivides long runs.
func ChunkMarkdown(src string) []Chunk {
	body := stripYAMLFrontmatter(strings.TrimSpace(src))
	if body == "" {
		return nil
	}
	sections := splitByHeadings(body)
	var out []Chunk
	idx := 0
	for _, sec := range sections {
		parts := splitLong(sec.text, maxChunkRunes, overlapRunes)
		for _, p := range parts {
			p = strings.TrimSpace(p)
			if p == "" {
				continue
			}
			out = append(out, Chunk{Index: idx, Heading: sec.heading, Text: p})
			idx++
		}
	}
	for i := range out {
		out[i].Index = i
	}
	return out
}

type section struct {
	heading string
	text    string
}

func stripYAMLFrontmatter(s string) string {
	lines := strings.Split(s, "\n")
	if len(lines) < 2 || strings.TrimSpace(lines[0]) != "---" {
		return s
	}
	for i := 1; i < len(lines); i++ {
		if strings.TrimSpace(lines[i]) == "---" {
			return strings.TrimSpace(strings.Join(lines[i+1:], "\n"))
		}
	}
	return s
}

func splitByHeadings(s string) []section {
	lines := strings.Split(s, "\n")
	var curHeading string
	var b strings.Builder
	var secs []section
	flush := func() {
		t := strings.TrimSpace(b.String())
		b.Reset()
		if t != "" {
			secs = append(secs, section{heading: curHeading, text: t})
		}
	}
	for _, ln := range lines {
		trim := strings.TrimSpace(ln)
		if isATXHeading(trim) {
			flush()
			curHeading = strings.TrimSpace(strings.TrimLeft(trim, "#"))
			continue
		}
		if b.Len() > 0 {
			b.WriteByte('\n')
		}
		b.WriteString(ln)
	}
	flush()
	if len(secs) == 0 && strings.TrimSpace(s) != "" {
		return []section{{heading: "", text: strings.TrimSpace(s)}}
	}
	return secs
}

func isATXHeading(line string) bool {
	if line == "" || line[0] != '#' {
		return false
	}
	n := 0
	for n < len(line) && line[n] == '#' {
		n++
		if n > 6 {
			return false
		}
	}
	if n >= len(line) {
		return false
	}
	return line[n] == ' ' || line[n] == '\t'
}

func splitLong(s string, maxRunes, overlap int) []string {
	if maxRunes <= 0 {
		return []string{s}
	}
	runes := []rune(s)
	if len(runes) <= maxRunes {
		return []string{s}
	}
	var parts []string
	start := 0
	for start < len(runes) {
		end := start + maxRunes
		if end > len(runes) {
			end = len(runes)
		} else {
			window := runes[start:end]
			chunk := string(window)
			if j := strings.LastIndex(chunk, "\n\n"); j >= 0 && j >= len(chunk)/8 {
				end = start + len([]rune(chunk[:j]))
			} else if j := strings.LastIndex(chunk, "\n"); j > 0 {
				end = start + len([]rune(chunk[:j]))
			}
			if end <= start {
				end = start + maxRunes
				if end > len(runes) {
					end = len(runes)
				}
			}
		}
		parts = append(parts, string(runes[start:end]))
		if end >= len(runes) {
			break
		}
		next := end - overlap
		if next <= start {
			next = end
		}
		start = next
	}
	return parts
}
