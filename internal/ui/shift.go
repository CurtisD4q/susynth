package ui

// lineAffinity is the unchanged prefix and suffix of two versions of the
// buffer, in lines. The edit sits between them.
func lineAffinity(oldLines, newLines []string) (prefix, suffix int) {
	for prefix < len(oldLines) && prefix < len(newLines) && oldLines[prefix] == newLines[prefix] {
		prefix++
	}
	for suffix < len(oldLines)-prefix && suffix < len(newLines)-prefix &&
		oldLines[len(oldLines)-1-suffix] == newLines[len(newLines)-1-suffix] {
		suffix++
	}
	return prefix, suffix
}

// remapLine moves a 0-based line across an edit that changed the line count.
// Lines above the edit stay put. Lines below it shift by the number of lines
// inserted or removed. A line that was deleted is dropped (ok is false).
// Negative lines (an error with no source line) are left alone.
func remapLine(line, oldN, newN, prefix, suffix int) (int, bool) {
	if line < 0 {
		return line, true
	}
	if line < prefix {
		return line, true
	}
	changeEnd := oldN - suffix
	if line >= changeEnd {
		return line + (newN - oldN), true
	}
	// The first line of a rewrite or a split is the same line, still there.
	// A deletion leaves an empty hole, and the marker goes with the line.
	if prefix < newN-suffix && line == prefix {
		return prefix, true
	}
	return 0, false
}

func remapErrs(errs []codeErr, oldLines, newLines []string) []codeErr {
	prefix, suffix := lineAffinity(oldLines, newLines)
	out := errs[:0]
	for _, e := range errs {
		if line, ok := remapLine(e.Line, len(oldLines), len(newLines), prefix, suffix); ok {
			e.Line = line
			out = append(out, e)
		}
	}
	return out
}

func remapMarks(marks map[int]bool, oldLines, newLines []string) map[int]bool {
	if len(marks) == 0 {
		return marks
	}
	prefix, suffix := lineAffinity(oldLines, newLines)
	next := map[int]bool{}
	for line, on := range marks {
		if !on {
			continue
		}
		if n, ok := remapLine(line, len(oldLines), len(newLines), prefix, suffix); ok {
			next[n] = true
		}
	}
	return next
}
