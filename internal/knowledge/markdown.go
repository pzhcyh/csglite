package knowledge

import "strings"

// mapMarkdownText transforms prose only. Code spans, fenced/indented code and
// Markdown escapes must remain literal when validating citations or exporting
// Wiki links; a code example is not a relationship or provenance assertion.
func mapMarkdownText(text string, transform func(string) string) string {
	var out strings.Builder
	start := 0
	protect := func(from, to int) {
		out.WriteString(transform(text[start:from]))
		out.WriteString(text[from:to])
		start = to
	}
	for i := 0; i < len(text); {
		if i == 0 || text[i-1] == '\n' {
			lineEnd := nextLine(text, i)
			line := strings.TrimSuffix(strings.TrimSuffix(text[i:lineEnd], "\n"), "\r")
			indent := len(line) - len(strings.TrimLeft(line, " "))
			if indent <= 3 {
				marker, count := fenceMarker(line[indent:])
				if count >= 3 {
					end := len(text)
					for pos := lineEnd; pos < len(text); {
						closeEnd := nextLine(text, pos)
						candidate := strings.TrimSuffix(strings.TrimSuffix(text[pos:closeEnd], "\n"), "\r")
						spaces := len(candidate) - len(strings.TrimLeft(candidate, " "))
						if spaces <= 3 {
							ch, n := fenceMarker(candidate[spaces:])
							if ch == marker && n >= count && strings.TrimSpace(candidate[spaces+n:]) == "" {
								end = closeEnd
								break
							}
						}
						pos = closeEnd
					}
					protect(i, end)
					i = end
					continue
				}
			}
			if indent >= 4 || strings.HasPrefix(line, "\t") {
				protect(i, lineEnd)
				i = lineEnd
				continue
			}
		}
		if text[i] == '\\' && i+1 < len(text) {
			protect(i, i+2)
			i += 2
			continue
		}
		if text[i] == '`' {
			n := 1
			for i+n < len(text) && text[i+n] == '`' {
				n++
			}
			end := -1
			for pos := i + n; pos < len(text); {
				at := strings.IndexByte(text[pos:], '`')
				if at < 0 {
					break
				}
				pos += at
				count := 1
				for pos+count < len(text) && text[pos+count] == '`' {
					count++
				}
				if count == n {
					end = pos + count
					break
				}
				pos += count
			}
			if end >= 0 {
				protect(i, end)
				i = end
				continue
			}
			i += n
			continue
		}
		i++
	}
	out.WriteString(transform(text[start:]))
	return out.String()
}

func nextLine(text string, start int) int {
	if i := strings.IndexByte(text[start:], '\n'); i >= 0 {
		return start + i + 1
	}
	return len(text)
}

func fenceMarker(line string) (byte, int) {
	if len(line) == 0 || (line[0] != '`' && line[0] != '~') {
		return 0, 0
	}
	n := 1
	for n < len(line) && line[n] == line[0] {
		n++
	}
	return line[0], n
}
