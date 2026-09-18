package policy

import (
	"path"
	"strings"
)

// matchGlob reports whether a slash-separated path matches a glob pattern.
//
// It extends path.Match with a `**` segment meaning zero or more path segments,
// which path.Match cannot express because its `*` never crosses a separator.
// Within a segment the semantics are exactly path.Match's. A malformed pattern
// never matches; policy load rejects those up front via validateGlob.
func matchGlob(pattern, name string) bool {
	return matchSegments(strings.Split(pattern, "/"), strings.Split(name, "/"))
}

func matchSegments(pat, name []string) bool {
	for len(pat) > 0 {
		if pat[0] == "**" {
			return matchAfterDoubleStar(pat[1:], name)
		}
		if len(name) == 0 {
			return false
		}
		if ok, err := path.Match(pat[0], name[0]); err != nil || !ok {
			return false
		}
		pat, name = pat[1:], name[1:]
	}
	return len(name) == 0
}

// matchAfterDoubleStar reports whether rest matches some suffix of name, rest
// being the pattern following a `**`. Every suffix is tried, including the empty
// one and the whole of name, because `**` spans zero or more segments.
func matchAfterDoubleStar(rest, name []string) bool {
	// Fast path, not a correctness guard: the loop below reaches the same answer
	// for an empty rest, since matchSegments(nil, nil) is true.
	if len(rest) == 0 {
		return true
	}
	for i := 0; i <= len(name); i++ {
		if matchSegments(rest, name[i:]) {
			return true
		}
	}
	return false
}

// validateGlob reports an error for a pattern path.Match would reject, so a
// policy that cannot select files refuses the run instead of silently matching
// nothing.
func validateGlob(pattern string) error {
	for _, seg := range strings.Split(pattern, "/") {
		if seg == "**" {
			continue
		}
		if _, err := path.Match(seg, ""); err != nil {
			return err
		}
	}
	return nil
}
