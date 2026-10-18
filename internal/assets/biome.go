package assets

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"

	"github.com/patrickserrano/lacquer/internal/config"
	projfrag "github.com/patrickserrano/lacquer/internal/fragments"
)

// renderBiomeIgnores leaves the default bytes untouched. With extra ignores it
// edits only files.includes, retaining the shipped formatting and key order.
func renderBiomeIgnores(body []byte, ignores []string) ([]byte, error) {
	if len(ignores) == 0 {
		return body, nil
	}
	var doc map[string]json.RawMessage
	if err := json.Unmarshal(body, &doc); err != nil {
		return nil, err
	}
	var files map[string]json.RawMessage
	if err := json.Unmarshal(doc["files"], &files); err != nil {
		return nil, err
	}
	raw, ok := files["includes"]
	if !ok {
		return nil, fmt.Errorf("biome.json has no files.includes")
	}
	var includes []string
	if err := json.Unmarshal(raw, &includes); err != nil {
		return nil, err
	}
	if len(includes) == 0 {
		return nil, fmt.Errorf("biome.json files.includes must contain the shared defaults")
	}
	var additions bytes.Buffer
	for _, ignore := range ignores {
		encoded, err := json.Marshal(ignore)
		if err != nil {
			return nil, err
		}
		additions.WriteString(",\n      ")
		additions.Write(encoded)
	}
	end := bytes.LastIndexByte(raw, ']')
	updated := append([]byte{}, bytes.TrimRight(raw[:end], " \r\n\t")...)
	updated = append(updated, additions.Bytes()...)
	updated = append(updated, []byte("\n    ]")...)
	newFiles := bytes.Replace(doc["files"], raw, updated, 1)
	return bytes.Replace(body, doc["files"], newFiles, 1), nil
}

// renderFragments applies the manifest's project-owned fragments to the three
// configs that take them (see internal/fragments). A project that declares none
// gets the shipped bytes untouched, and its profile's doctor.toml is not even
// read.
func renderFragments(a Asset, body []byte, cfg *config.Config) ([]byte, error) {
	probed := func(read func([]byte) ([]string, error)) ([]string, error) {
		// <root>/profiles/<profile>/config/<file> -> <root>/profiles/<profile>/doctor.toml
		data, err := os.ReadFile(filepath.Join(filepath.Dir(filepath.Dir(a.Src)), "doctor.toml"))
		if err != nil {
			return nil, fmt.Errorf("read the doctor probes a fragment of %s is checked against: %w", a.Dest, err)
		}
		return read(data)
	}
	switch filepath.Base(a.Dest) {
	case "biome.json":
		body, err := renderBiomeIgnores(body, cfg.Web.BiomeIgnores)
		if err != nil || len(cfg.Web.BiomeOverrides) == 0 {
			return body, err
		}
		rules, err := probed(projfrag.BiomeProbedRules)
		if err != nil {
			return nil, err
		}
		return projfrag.RenderBiomeOverrides(body, rules, cfg.Web.BiomeOverrides)
	case "typedoc.json":
		return projfrag.RenderTypeDoc(body, cfg.Web.TypeDocEntryPoints, cfg.Web.TypeDocEntryPointStrategy)
	case ".swiftlint.yml":
		if len(cfg.IOS.SwiftLintCustomRules) == 0 {
			return body, nil
		}
		rules, err := probed(projfrag.SwiftLintProbedRules)
		if err != nil {
			return nil, err
		}
		return projfrag.RenderSwiftLint(body, rules, cfg.IOS.SwiftLintCustomRules)
	}
	return body, nil
}
