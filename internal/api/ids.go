package api

import "strings"

// maxSlugLen is the longest generated upstream or key ID.
const maxSlugLen = 32

// slugify turns a display name into a valid configuration ID: lowercase ASCII
// letters and digits, single dashes between runs, at most 32 characters. It
// falls back when the name has no usable characters (e.g. a CJK name).
func slugify(name, fallback string) string {
	var b strings.Builder
	lastDash := false
	for _, r := range strings.ToLower(strings.TrimSpace(name)) {
		if b.Len() >= maxSlugLen {
			break
		}
		switch {
		case r >= 'a' && r <= 'z', r >= '0' && r <= '9':
			b.WriteRune(r)
			lastDash = false
		case !lastDash && b.Len() > 0:
			b.WriteByte('-')
			lastDash = true
		}
	}
	out := strings.TrimRight(b.String(), "-")
	if out == "" {
		return fallback
	}
	return out
}
