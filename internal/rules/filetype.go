package rules

import (
	"path"
	"strings"
)

var extTypes = map[string]FileType{
	".go":       TypeGo,
	".md":       TypeMarkdown,
	".markdown": TypeMarkdown,
	".pl":       TypePerl,
	".pm":       TypePerl,
	".yml":      TypeYAML,
	".yaml":     TypeYAML,
}

// TypeOf derives a file's type from its extension. It returns TypeUnknown for
// any extension not in the table; it never inspects content, since v1 knows
// only extensions, runes and lines.
func TypeOf(p string) FileType {
	t, ok := extTypes[strings.ToLower(path.Ext(p))]
	if !ok {
		return TypeUnknown
	}
	return t
}
