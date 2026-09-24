package main

import (
	"bytes"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/patrickserrano/lacquer/internal/baseline"
)

const staticSettingsLabel = "static resolution by lacquer: not Xcode's evaluation; defaults and conditionals are not applied"

type settingRow struct {
	Target        string            `json:"target"`
	Configuration string            `json:"configuration"`
	Setting       string            `json:"setting"`
	Static        *baseline.Setting `json:"static,omitempty"`
	Xcode         *baseline.Setting `json:"xcodebuild,omitempty"`
	Comparison    string            `json:"comparison,omitempty"`
}

func runSettings(args []string, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet("settings", flag.ContinueOnError)
	fs.SetOutput(stderr)
	project := fs.String("project", "", ".xcodeproj (default: the single bundle in the current directory)")
	target := fs.String("target", "", "target name (default: all targets)")
	scheme := fs.String("scheme", "", "scheme name (required with --xcode)")
	configuration := fs.String("configuration", "", "configuration name (default: all configurations)")
	jsonOutput := fs.Bool("json", false, "print structured output")
	xcode := fs.Bool("xcode", false, "ask xcodebuild for authoritative values without building")
	compare := fs.Bool("compare", false, "compare static values with --xcode")
	if err := fs.Parse(args); err != nil {
		return 2
	}
	if *xcode && *scheme == "" {
		fmt.Fprintln(stderr, "error: --xcode requires --scheme")
		return 2
	}
	if *compare && !*xcode {
		return fail(stderr, fmt.Errorf("--compare requires --xcode"))
	}
	if *project == "" {
		projects, err := filepath.Glob("*.xcodeproj")
		if err != nil {
			return fail(stderr, err)
		}
		if len(projects) != 1 {
			return fail(stderr, fmt.Errorf("specify --project: found %d .xcodeproj bundles", len(projects)))
		}
		*project = projects[0]
	}
	path, err := filepath.Abs(*project)
	if err != nil {
		return fail(stderr, err)
	}
	if filepath.Base(path) == "project.pbxproj" {
		path = filepath.Dir(path)
	}
	d, err := baseline.ReadXcodeproj(path)
	if err != nil {
		return fail(stderr, fmt.Errorf("UNKNOWN — %w", err))
	}
	keys := fs.Args()
	if len(keys) == 0 {
		keys = []string{"SWIFT_VERSION", "SWIFT_TREAT_WARNINGS_AS_ERRORS", "SWIFT_STRICT_CONCURRENCY"}
	}
	var rows []settingRow
	for _, c := range d.Configs {
		if c.ProjectLevel || (*target != "" && c.Target != *target) || (*configuration != "" && c.Name != *configuration) {
			continue
		}
		// An unowned configuration cannot safely be associated with a target.
		if c.Target == "" || c.Name == "" {
			return fail(stderr, fmt.Errorf("UNKNOWN — configuration %s has no target/name in %s", c.ID, d.Source))
		}
		var authoritative map[string]string
		if *xcode {
			authoritative, err = xcodeSettings(path, *scheme, c.Target, c.Name)
			if err != nil {
				return fail(stderr, err)
			}
		}
		for _, key := range keys {
			row := settingRow{Target: c.Target, Configuration: c.Name, Setting: key}
			if !*xcode || *compare {
				value := d.Setting(c, key)
				row.Static = &value
			}
			if *xcode {
				value := baseline.Setting{State: "unset", Source: "xcodebuild"}
				if v, ok := authoritative[key]; ok {
					value.State = "set"
					value.Value = v
				}
				row.Xcode = &value
			}
			if *compare {
				row.Comparison = "match"
				if row.Static.State == "unknown" {
					row.Comparison = "unknown"
				} else if row.Static.State != row.Xcode.State || row.Static.Value != row.Xcode.Value {
					row.Comparison = "different"
				}
			}
			rows = append(rows, row)
		}
	}
	if len(rows) == 0 {
		return fail(stderr, fmt.Errorf("UNKNOWN — no matching target/configuration in %s", path))
	}
	label := staticSettingsLabel
	if *xcode {
		label = "xcodebuild"
		if *compare {
			label = staticSettingsLabel + "; compared with xcodebuild"
		}
	}
	if *jsonOutput {
		encoder := json.NewEncoder(stdout)
		encoder.SetIndent("", "  ")
		if err := encoder.Encode(struct {
			Label    string       `json:"label"`
			Project  string       `json:"project"`
			Settings []settingRow `json:"settings"`
		}{label, path, rows}); err != nil {
			return fail(stderr, err)
		}
	} else {
		fmt.Fprintln(stdout, label)
		for _, row := range rows {
			fmt.Fprintf(stdout, "%s/%s: %s = ", row.Target, row.Configuration, row.Setting)
			if row.Static != nil {
				fmt.Fprint(stdout, formatSetting(*row.Static))
			}
			if row.Xcode != nil {
				if row.Static != nil {
					fmt.Fprint(stdout, " | xcodebuild: ")
				}
				fmt.Fprint(stdout, formatSetting(*row.Xcode))
			}
			switch row.Comparison {
			case "different":
				fmt.Fprint(stdout, " [DIFF]")
			case "unknown":
				fmt.Fprint(stdout, " [UNKNOWN comparison]")
			case "match":
				fmt.Fprint(stdout, " [MATCH]")
			}
			fmt.Fprintln(stdout)
		}
	}
	return 0
}

func formatSetting(value baseline.Setting) string {
	switch value.State {
	case "unknown":
		return "UNKNOWN — " + value.Reason
	case "unset":
		return "UNSET"
	default:
		return value.Value + " — " + value.Source
	}
}

// xcodeSettings never builds and isolates any Xcode scratch output. PATH permits
// tests to inject a fake executable; stderr and failures are never swallowed.
func xcodeSettings(project, scheme, target, configuration string) (map[string]string, error) {
	dir, err := os.MkdirTemp("", "lacquer-settings-*")
	if err != nil {
		return nil, err
	}
	defer os.RemoveAll(dir)
	cmd := exec.Command("xcodebuild", "-showBuildSettings", "-json", "-project", project, "-scheme", scheme, "-configuration", configuration, "-derivedDataPath", dir, "-disableAutomaticPackageResolution", "-skipPackageUpdates")
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	output, err := cmd.Output()
	if err != nil {
		return nil, fmt.Errorf("xcodebuild: %w: %s", err, strings.TrimSpace(stderr.String()))
	}
	var results []struct {
		Target        string            `json:"target"`
		BuildSettings map[string]string `json:"buildSettings"`
	}
	if err := json.Unmarshal(output, &results); err != nil {
		return nil, fmt.Errorf("xcodebuild JSON: %w", err)
	}
	var settings map[string]string
	var targets []string
	targetFound := false
	for _, result := range results {
		targets = append(targets, result.Target)
		if result.Target != target {
			continue
		}
		targetFound = true
		if config, ok := result.BuildSettings["CONFIGURATION"]; ok && config != configuration {
			continue
		}
		if settings != nil {
			return nil, fmt.Errorf("xcodebuild: ambiguous results for %s/%s", target, configuration)
		}
		settings = result.BuildSettings
	}
	if !targetFound {
		built := strings.Join(targets, ", ")
		if built == "" {
			built = "(none)"
		}
		return nil, fmt.Errorf("xcodebuild: scheme %q does not build target %q; builds targets: %s", scheme, target, built)
	}
	if len(settings) == 0 {
		return nil, fmt.Errorf("xcodebuild: no matching settings for %s/%s", target, configuration)
	}
	return settings, nil
}
