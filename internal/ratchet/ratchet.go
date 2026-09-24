// Package ratchet records project-owned ceilings and tightens them after gains.
// Reasons explain an explicit increase; they never exempt a later regression.
package ratchet

import (
	"bytes"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/BurntSushi/toml"
	"github.com/patrickserrano/lacquer/internal/config"
)

const Name = ".lacquer.ratchet.toml"
const ClaudeProjectLines = "claude_md_project_lines"
const Suppressions = "unjustified_suppressions"

var metrics = []string{ClaudeProjectLines, Suppressions}

type Baseline struct {
	Ratchet map[string]int    `toml:"ratchet"`
	Reasons map[string]string `toml:"reasons,omitempty"`
}

type Finding struct {
	Metric string `json:"metric"`
	Before int    `json:"before"`
	After  int    `json:"after"`
}

func (f Finding) Regressed() bool { return f.After > f.Before }

func Format(findings []Finding) string {
	var out strings.Builder
	for _, f := range findings {
		state := "improved"
		if f.Regressed() {
			state = "regressed"
		}
		fmt.Fprintf(&out, "ratchet: %s %s %d → %d\n", f.Metric, state, f.Before, f.After)
	}
	return out.String()
}

func Blocking(findings []Finding) int {
	n := 0
	for _, f := range findings {
		if f.Regressed() {
			n++
		}
	}
	return n
}

func Compare(b *Baseline, values map[string]int) []Finding {
	var out []Finding
	for _, key := range metrics {
		if values[key] != b.Ratchet[key] {
			out = append(out, Finding{key, b.Ratchet[key], values[key]})
		}
	}
	return out
}

func Read(root string) (*Baseline, error) {
	path := filepath.Join(root, Name)
	fi, err := os.Lstat(path)
	if os.IsNotExist(err) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	if !fi.Mode().IsRegular() {
		return nil, fmt.Errorf("%s must be a regular file", path)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var b Baseline
	md, err := toml.Decode(string(data), &b)
	if err != nil {
		return nil, fmt.Errorf("parse %s: %w", Name, err)
	}
	if len(md.Undecoded()) > 0 {
		return nil, fmt.Errorf("%s: unknown keys %v", Name, md.Undecoded())
	}
	for _, key := range metrics {
		value, ok := b.Ratchet[key]
		if !ok || value < 0 {
			return nil, fmt.Errorf("%s: %s needs a nonnegative baseline", Name, key)
		}
	}
	for key := range b.Ratchet {
		if !known(key) {
			return nil, fmt.Errorf("%s: unknown metric %q", Name, key)
		}
	}
	for key, reason := range b.Reasons {
		if !known(key) || strings.TrimSpace(reason) == "" {
			return nil, fmt.Errorf("%s: invalid reason for %q", Name, key)
		}
	}
	return &b, nil
}

func known(key string) bool { return key == ClaudeProjectLines || key == Suppressions }

func write(root string, b *Baseline) error {
	// Atomic replacement avoids a truncated baseline after an interrupted write.
	path := filepath.Join(root, Name)
	if fi, err := os.Lstat(path); err == nil && !fi.Mode().IsRegular() {
		return fmt.Errorf("refusing to replace non-regular %s", path)
	} else if err != nil && !os.IsNotExist(err) {
		return err
	}
	var data bytes.Buffer
	if err := toml.NewEncoder(&data).Encode(b); err != nil {
		return err
	}
	f, err := os.CreateTemp(root, ".lacquer-ratchet-*")
	if err != nil {
		return err
	}
	defer os.Remove(f.Name())
	if _, err := f.Write(data.Bytes()); err != nil {
		f.Close()
		return err
	}
	if err := f.Chmod(0o644); err != nil {
		f.Close()
		return err
	}
	if err := f.Close(); err != nil {
		return err
	}
	return os.Rename(f.Name(), path)
}

// Check is read-only. An absent baseline needs explicit enrollment via --write.
func Check(root string, cfg *config.Config) ([]Finding, error) {
	b, err := Read(root)
	if err != nil || b == nil {
		return nil, err
	}
	values, err := Measure(root, cfg)
	if err != nil {
		return nil, err
	}
	return Compare(b, values), nil
}

// Tighten initializes on request, or lowers an existing baseline. Regressions
// are reported but never written, including when another metric improved.
func Tighten(root string, cfg *config.Config, initialize bool) ([]Finding, error) {
	b, err := Read(root)
	if err != nil || (b == nil && !initialize) {
		return nil, err
	}
	values, err := Measure(root, cfg)
	if err != nil {
		return nil, err
	}
	if b == nil {
		return nil, write(root, &Baseline{Ratchet: values})
	}
	findings := Compare(b, values)
	changed := false
	for _, f := range findings {
		if !f.Regressed() {
			b.Ratchet[f.Metric] = f.After
			changed = true
		}
	}
	if changed {
		err = write(root, b)
	}
	return findings, err
}

// Loosen records exactly the current measurement and the reason for accepting it.
func Loosen(root string, cfg *config.Config, metric, reason string) error {
	if !known(metric) {
		return fmt.Errorf("unknown ratchet metric %q", metric)
	}
	if strings.TrimSpace(reason) == "" {
		return fmt.Errorf("loosening requires a nonempty --reason")
	}
	b, err := Read(root)
	if err != nil {
		return err
	}
	if b == nil {
		return fmt.Errorf("run lacquer ratchet --write to establish a baseline first")
	}
	values, err := Measure(root, cfg)
	if err != nil {
		return err
	}
	if values[metric] <= b.Ratchet[metric] {
		return fmt.Errorf("%s has not regressed; use --write to tighten", metric)
	}
	b.Ratchet[metric] = values[metric]
	if b.Reasons == nil {
		b.Reasons = map[string]string{}
	}
	b.Reasons[metric] = strings.TrimSpace(reason)
	return write(root, b)
}
