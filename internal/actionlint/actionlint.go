// Package actionlint owns the runner labels shared by rendered workflows and
// their lint configuration. Project labels and other configuration stay local.
package actionlint

import (
	"bytes"
	"fmt"
	"strings"

	"github.com/patrickserrano/lacquer/internal/region"
	"github.com/patrickserrano/lacquer/internal/version"
	"gopkg.in/yaml.v3"
)

const Name = ".github/actionlint.yaml"
const Key = "actionlint"

var Syntax = region.Hash

// Labels names only custom labels; actionlint already knows GitHub's OS,
// architecture and self-hosted labels. Keep this in step with shipped workflows.
const Labels = "dedicated\npi-gate\nblacksmith-4vcpu-ubuntu-2404\nblacksmith-2vcpu-ubuntu-2404-arm"

// Body is the managed portion of the self-hosted-runner.labels sequence.
func Body() string { return "    - " + strings.ReplaceAll(Labels, "\n", "\n    - ") }

// Merge inserts a region inside the labels sequence, avoiding duplicate YAML
// keys when a project already declares self-hosted runners. Initial adoption
// converts flow-style sequences to block style, retaining comments and values;
// subsequent syncs replace only the marked bytes.
func Merge(content string, v version.Version) (string, error) {
	block, err := Syntax.Merge("", Key, v, Body())
	if err != nil {
		return "", err
	}
	if strings.Contains(content, "lacquer:"+Key+":") {
		merged, err := Syntax.Merge(content, Key, v, Body())
		if err != nil {
			return "", err
		}
		if err := Check([]byte(merged)); err != nil {
			return "", err
		}
		return merged, nil
	}
	var doc yaml.Node
	if err := yaml.Unmarshal([]byte(content), &doc); err != nil {
		return "", err
	}
	if len(doc.Content) == 0 {
		if content != "" && !strings.HasSuffix(content, "\n") {
			content += "\n"
		}
		return content + "self-hosted-runner:\n  labels:\n" + block, nil
	}
	root := doc.Content[0]
	if root.Kind != yaml.MappingNode {
		return "", fmt.Errorf("actionlint config must be a mapping")
	}
	runner := mappingValue(root, "self-hosted-runner")
	if runner == nil && root.Style&yaml.FlowStyle == 0 {
		// No structural conversion is needed; preserve every existing byte.
		return strings.TrimRight(content, "\n") + "\n\nself-hosted-runner:\n  labels:\n" + block, nil
	}
	if runner == nil {
		runner = &yaml.Node{Kind: yaml.MappingNode, Tag: "!!map"}
		root.Content = append(root.Content, &yaml.Node{Kind: yaml.ScalarNode, Tag: "!!str", Value: "self-hosted-runner"}, runner)
	}
	if runner.Kind != yaml.MappingNode {
		return "", fmt.Errorf("self-hosted-runner must be a mapping")
	}
	labels := mappingValue(runner, "labels")
	if labels == nil {
		labels = &yaml.Node{Kind: yaml.SequenceNode, Tag: "!!seq"}
		runner.Content = append(runner.Content, &yaml.Node{Kind: yaml.ScalarNode, Tag: "!!str", Value: "labels"}, labels)
	}
	if labels.Kind != yaml.SequenceNode {
		return "", fmt.Errorf("self-hosted-runner.labels must be a sequence")
	}
	for _, label := range labels.Content {
		if label.Kind != yaml.ScalarNode || label.Tag != "!!str" {
			return "", fmt.Errorf("runner labels must be strings")
		}
	}
	// A fresh node's line uniquely identifies the insertion point after encoding,
	// so no sentinel string from project content can accidentally be replaced.
	placeholder := &yaml.Node{Kind: yaml.ScalarNode, Tag: "!!str", Value: "lacquer-managed-labels"}
	if labels.LineComment != "" {
		for i := 0; i < len(runner.Content); i += 2 {
			if runner.Content[i].Value == "labels" {
				runner.Content[i].LineComment = labels.LineComment
			}
		}
		labels.LineComment = ""
	}
	if runner.LineComment != "" {
		for i := 0; i < len(root.Content); i += 2 {
			if root.Content[i].Value == "self-hosted-runner" {
				root.Content[i].LineComment = runner.LineComment
			}
		}
		runner.LineComment = ""
	}
	labels.Style = 0
	runner.Style = 0
	root.Style = 0
	labels.Content = append(labels.Content, placeholder)
	var out bytes.Buffer
	enc := yaml.NewEncoder(&out)
	enc.SetIndent(2)
	if err := enc.Encode(&doc); err != nil {
		return "", err
	}
	var rendered yaml.Node
	if err := yaml.Unmarshal(out.Bytes(), &rendered); err != nil {
		return "", err
	}
	sequence := mappingValue(mappingValue(rendered.Content[0], "self-hosted-runner"), "labels")
	line := sequence.Content[len(sequence.Content)-1].Line - 1
	lines := strings.Split(out.String(), "\n")
	lines[line] = strings.TrimSuffix(block, "\n")
	merged := strings.Join(lines, "\n")
	if err := Check([]byte(merged)); err != nil {
		return "", err
	}
	return merged, nil
}

func mappingValue(node *yaml.Node, key string) *yaml.Node {
	for i := 0; i+1 < len(node.Content); i += 2 {
		if node.Content[i].Value == key {
			return node.Content[i+1]
		}
	}
	return nil
}

// Check fails on missing labels, including an empty but valid configuration.
func Check(data []byte) error {
	var cfg struct {
		Runner struct {
			Labels []string `yaml:"labels"`
		} `yaml:"self-hosted-runner"`
	}
	if err := yaml.Unmarshal(data, &cfg); err != nil {
		return err
	}
	have := map[string]bool{}
	for _, label := range cfg.Runner.Labels {
		have[label] = true
	}
	for _, label := range strings.Split(Labels, "\n") {
		if !have[label] {
			return fmt.Errorf("actionlint config is missing runner label %q", label)
		}
	}
	return nil
}
