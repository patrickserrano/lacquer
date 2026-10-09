package fragments

import (
	"bytes"
	"fmt"
	"path"
	"strings"
)

// typeDocDefault is the entryPoints line the shipped typedoc.json carries. The
// renderer replaces exactly this line and refuses if it is not there exactly
// once, so a profile edit to it cannot leave the manifest value silently unused.
const typeDocDefault = `  "entryPoints": ["src/index.ts"],`

// ValidateTypeDocEntryPoints checks [web] typedoc_entry_points. Each entry is a
// path (or glob) relative to the component, inside it.
//
// Entry points decide which exports the docs gate checks, so they are a
// project's to set: the doctor's TypeDoc probes pass their own --entryPoints on
// the command line, which overrides the file, so no value here can make a probe
// pass or fail. What the check refuses is a value that would not mean what it
// says: an empty list (TypeDoc would fall back to its own default), an absolute
// path or one that climbs out of the component, an entry that TypeDoc would read
// as a flag, and a duplicate.
func ValidateTypeDocEntryPoints(entries []string) error {
	if entries == nil {
		return nil
	}
	if len(entries) == 0 {
		return fmt.Errorf("[web] typedoc_entry_points is empty; omit it to keep the default, or list the package's public entry points")
	}
	seen := map[string]bool{}
	for _, e := range entries {
		switch {
		case strings.TrimSpace(e) == "" || strings.ContainsAny(e, "\r\n\\"):
			return fmt.Errorf("[web] typedoc_entry_points has %q; each entry is a nonempty, one-line, forward-slash path", e)
		case strings.HasPrefix(e, "/"):
			return fmt.Errorf("[web] typedoc_entry_points has %q; entries are relative to the component", e)
		case strings.HasPrefix(e, "-"):
			return fmt.Errorf("[web] typedoc_entry_points has %q, which TypeDoc would read as a flag", e)
		case path.Clean(e) == ".." || strings.HasPrefix(path.Clean(e), "../"):
			return fmt.Errorf("[web] typedoc_entry_points has %q, which leaves the component", e)
		case seen[e]:
			return fmt.Errorf("[web] typedoc_entry_points lists %q twice", e)
		}
		seen[e] = true
	}
	return nil
}

// RenderTypeDoc returns the shipped typedoc.json with its entryPoints replaced
// by the project's. With none declared it returns body untouched, byte for byte.
func RenderTypeDoc(body []byte, entries []string) ([]byte, error) {
	if len(entries) == 0 {
		return body, nil
	}
	if err := ValidateTypeDocEntryPoints(entries); err != nil {
		return nil, err
	}
	if n := bytes.Count(body, []byte(typeDocDefault)); n != 1 {
		return nil, fmt.Errorf("the shipped typedoc.json carries its default entryPoints line %d times, want 1; "+
			"[web] typedoc_entry_points has nothing to replace", n)
	}
	items := make([]any, len(entries))
	for i, e := range entries {
		items[i] = e
	}
	const key = `  "entryPoints": `
	var b bytes.Buffer
	b.WriteString(key)
	writeJSON(&b, items, 2, len(key))
	b.WriteString(",")
	return bytes.Replace(body, []byte(typeDocDefault), b.Bytes(), 1), nil
}
