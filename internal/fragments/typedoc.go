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

// TypeDocExpand is the one entryPointStrategy [web] typedoc_entry_point_strategy
// accepts. Under it a directory entry is walked recursively, so a file added
// under it is documented (and checked) without anyone listing it.
const TypeDocExpand = "expand"

// TypeDocTestExcludes is the exclude rendered beside an entry list that can grow
// on its own: a strategy, or a glob. Both pull every matching file in, and a
// test file's exports (fixtures, helpers) are not the package's public surface,
// so without this they would either need TSDoc nobody reads or turn the docs
// gate red for code that ships nowhere.
//
// The patterns are the shapes vitest and jest collect by default (*.test.*,
// *.spec.*, __tests__/), in the two extensions the web profile compiles. Each
// is a test-only shape: none matches a module a package would export.
var TypeDocTestExcludes = []string{
	"**/*.test.ts", "**/*.test.tsx", "**/*.spec.ts", "**/*.spec.tsx", "**/__tests__/**",
}

// typeDocGlob reports whether TypeDoc expands an entry as a glob. Measured on
// TypeDoc 0.28.20: it does so under every strategy, the default included.
func typeDocGlob(e string) bool { return strings.ContainsAny(e, "*?[{(") }

// TypeDocExcludes is the exclude list the rendered typedoc.json carries for
// these keys, or nil when it carries none (the shipped file has no exclude).
func TypeDocExcludes(entries []string, strategy string) []string {
	if strategy != "" {
		return TypeDocTestExcludes
	}
	for _, e := range entries {
		if typeDocGlob(e) {
			return TypeDocTestExcludes
		}
	}
	return nil
}

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

// ValidateTypeDoc checks the entry points together with
// [web] typedoc_entry_point_strategy.
//
// Only "expand" is accepted. "packages" is refused because, measured on TypeDoc
// 0.28.20 with the shipped typedoc.json, it does not apply the root's
// `validation` to each package: an undocumented export in a package's own entry
// point exited 0 and wrote a site (exit 4 once packageOptions repeated the
// validation keys). A strategy that turns the gate off while it reads green is
// worse than no strategy. "resolve" is TypeDoc's default and is spelled by
// omitting the key.
//
// Without a strategy, TypeDoc's default resolves a directory to its index file
// and fails when there is none (exit 3, "Unable to find any entry points"), so a
// plain directory entry is refused here with the key that makes it mean what it
// says. A glob is fine either way: TypeDoc expands globs under every strategy.
func ValidateTypeDoc(entries []string, strategy string) error {
	if err := ValidateTypeDocEntryPoints(entries); err != nil {
		return err
	}
	switch strategy {
	case "", TypeDocExpand:
	case "packages":
		return fmt.Errorf("[web] typedoc_entry_point_strategy = %q is not supported: TypeDoc's packages mode does not apply the root "+
			"validation to each package, so undocumented exports pass silently. Use %q", strategy, TypeDocExpand)
	default:
		return fmt.Errorf("[web] typedoc_entry_point_strategy is %q; the only value is %q (omit the key for TypeDoc's default)", strategy, TypeDocExpand)
	}
	if strategy == "" {
		for _, e := range entries {
			if !typeDocGlob(e) && path.Ext(e) == "" {
				return fmt.Errorf("[web] typedoc_entry_points has %q, a directory; TypeDoc reads one only under "+
					"typedoc_entry_point_strategy = %q, which covers every file under it", e, TypeDocExpand)
			}
		}
	}
	return nil
}

// RenderTypeDoc returns the shipped typedoc.json with the project's entry points
// in place of the default and, when a strategy or a glob is declared, the
// strategy and the test exclude after them. With nothing declared it returns
// body untouched, byte for byte: the exclude exists only for lists that can
// grow on their own, so a project that declares neither renders as shipped.
func RenderTypeDoc(body []byte, entries []string, strategy string) ([]byte, error) {
	if len(entries) == 0 && strategy == "" {
		return body, nil
	}
	if err := ValidateTypeDoc(entries, strategy); err != nil {
		return nil, err
	}
	if n := bytes.Count(body, []byte(typeDocDefault)); n != 1 {
		return nil, fmt.Errorf("the shipped typedoc.json carries its default entryPoints line %d times, want 1; "+
			"[web] typedoc_entry_points has nothing to replace", n)
	}
	var b bytes.Buffer
	if len(entries) == 0 {
		b.WriteString(typeDocDefault)
	} else {
		writeTypeDocKey(&b, `  "entryPoints": `, entries)
	}
	if strategy != "" {
		b.WriteString("\n  \"entryPointStrategy\": " + quote(strategy) + ",")
	}
	if excl := TypeDocExcludes(entries, strategy); excl != nil {
		b.WriteString("\n")
		writeTypeDocKey(&b, `  "exclude": `, excl)
	}
	return bytes.Replace(body, []byte(typeDocDefault), b.Bytes(), 1), nil
}

// writeTypeDocKey writes one top-level string-list key, laid out as biome
// format lays it out, with its trailing comma.
func writeTypeDocKey(b *bytes.Buffer, key string, list []string) {
	items := make([]any, len(list))
	for i, e := range list {
		items[i] = e
	}
	b.WriteString(key)
	writeJSON(b, items, 2, len(key))
	b.WriteString(",")
}
