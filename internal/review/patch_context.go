package review

import (
	"regexp"
	"strconv"
	"strings"
)

var hunkHeader = regexp.MustCompile(`^@@ -([0-9]+)(?:,([0-9]+))? \+([0-9]+)(?:,([0-9]+))? @@`)

func PatchContext(patch, side string, start, end int) (string, bool) {
	if start > end {
		start, end = end, start
	}
	if start < 1 || end-start >= 200 || (side != "additions" && side != "deletions") {
		return "", false
	}
	selected := make(map[int]string)
	oldLine, newLine, inHunk := 0, 0, false
	for _, line := range strings.Split(patch, "\n") {
		if match := hunkHeader.FindStringSubmatch(line); len(match) == 5 {
			oldLine, _ = strconv.Atoi(match[1])
			newLine, _ = strconv.Atoi(match[3])
			inHunk = true
			continue
		}
		if !inHunk || line == "" || strings.HasPrefix(line, "\\ No newline") {
			continue
		}
		prefix := line[0]
		switch prefix {
		case ' ':
			if side == "additions" {
				selected[newLine] = "  " + line[1:]
			} else {
				selected[oldLine] = "  " + line[1:]
			}
			oldLine++
			newLine++
		case '+':
			if side == "additions" {
				selected[newLine] = "+ " + line[1:]
			}
			newLine++
		case '-':
			if side == "deletions" {
				selected[oldLine] = "- " + line[1:]
			}
			oldLine++
		}
	}
	lines := make([]string, 0, end-start+1)
	for line := start; line <= end; line++ {
		value, ok := selected[line]
		if !ok {
			return "", false
		}
		lines = append(lines, value)
	}
	return strings.Join(lines, "\n"), true
}
