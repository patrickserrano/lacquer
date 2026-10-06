package doctor

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/patrickserrano/lacquer/internal/safepath"
)

// biomeLocalSchema is the `$schema` the shipped biome.json renders: the schema
// file inside the @biomejs/biome package the component installed. Biome's own
// configuration reference documents this form. It cannot name a different
// version from the binary beside it, because it IS that package's file.
const biomeLocalSchema = "./node_modules/@biomejs/biome/configuration_schema.json"

// biomeSchemaURL matches the hosted form, whose path carries a version.
var biomeSchemaURL = regexp.MustCompile(`^https://biomejs\.dev/schemas/([^/]+)/schema\.json$`)

// checkBiomeSchemaInstalled is the `biome-schema` configuration probe: the
// synced biome.json's `$schema` must describe the @biomejs/biome the component
// actually installed.
//
// Biome does not check this itself in any way a gate notices. A hosted URL
// naming another version is reported at INFO severity ("The configuration
// schema version does not match the CLI version") and `biome ci` exits 0; a
// local path that resolves to nothing is not reported at all. The shipped
// config said 2.5.0 while consumers ran 2.5.14 and 2.5.15, and nothing said a
// word, because both of those outcomes look exactly like a config that matches.
func checkBiomeSchemaInstalled(root, component string) error {
	// The negative controls first. A check that cannot fail on a mismatch is
	// indistinguishable from one that is not running.
	if checkBiomeSchema("https://biomejs.dev/schemas/2.5.0/schema.json", "2.5.15", "", "") == nil {
		return fmt.Errorf("schema check accepted a hosted $schema for 2.5.0 against an installed 2.5.15")
	}
	if checkBiomeSchema(biomeLocalSchema, "2.5.15", "/nowhere/configuration_schema.json", "/installed/configuration_schema.json") == nil {
		return fmt.Errorf("schema check accepted a local $schema that does not resolve to the installed package")
	}

	rel, err := filepath.Rel(root, filepath.Join(component, "biome.json"))
	if err != nil {
		return err
	}
	path, err := safepath.Resolve(root, rel)
	if err != nil {
		return err
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	var doc struct {
		Schema string `json:"$schema"`
	}
	if err := json.Unmarshal(data, &doc); err != nil {
		return fmt.Errorf("%s: %w", rel, err)
	}

	pkgDir := filepath.Join(component, "node_modules", "@biomejs", "biome")
	pkgData, err := os.ReadFile(filepath.Join(pkgDir, "package.json"))
	if err != nil {
		return fmt.Errorf("@biomejs/biome is not installed in this component (%v), so there is no installed version for biome.json's $schema to match", err)
	}
	var pkg struct {
		Version string `json:"version"`
	}
	if err := json.Unmarshal(pkgData, &pkg); err != nil || pkg.Version == "" {
		return fmt.Errorf("could not read a version from the installed @biomejs/biome package.json")
	}

	// Both sides through EvalSymlinks: pnpm's node_modules/@biomejs/biome is a
	// link into its store, and a path is the installed schema only if the two
	// land on the same file.
	resolved := ""
	if !biomeSchemaURL.MatchString(doc.Schema) && doc.Schema != "" && !strings.Contains(doc.Schema, "://") {
		if r, err := filepath.EvalSymlinks(filepath.Join(filepath.Dir(path), filepath.FromSlash(doc.Schema))); err == nil {
			resolved = r
		}
	}
	installed, err := filepath.EvalSymlinks(filepath.Join(pkgDir, "configuration_schema.json"))
	if err != nil {
		return fmt.Errorf("the installed @biomejs/biome %s ships no configuration_schema.json: %v", pkg.Version, err)
	}
	return checkBiomeSchema(doc.Schema, pkg.Version, resolved, installed)
}

// checkBiomeSchema decides whether a `$schema` value matches the installed
// biome. For a local path, resolved is where it landed ("" if nowhere) and
// installed is the installed package's schema file.
func checkBiomeSchema(schema, version, resolved, installed string) error {
	if m := biomeSchemaURL.FindStringSubmatch(schema); m != nil {
		if m[1] != version {
			return fmt.Errorf("biome.json's $schema is for biome %s but @biomejs/biome %s is installed; re-sync, or point $schema at %s", m[1], version, biomeLocalSchema)
		}
		return nil
	}
	if schema == "" {
		return fmt.Errorf("biome.json has no $schema")
	}
	if strings.Contains(schema, "://") {
		return fmt.Errorf("biome.json's $schema %q is neither a biomejs.dev schema URL nor a local path, so its version cannot be checked", schema)
	}
	if resolved == "" {
		return fmt.Errorf("biome.json's $schema %q does not resolve to a file; Biome does not report this, editors silently lose validation", schema)
	}
	if resolved != installed {
		return fmt.Errorf("biome.json's $schema %q resolves to %s, not to the installed @biomejs/biome %s (%s)", schema, resolved, version, installed)
	}
	return nil
}
