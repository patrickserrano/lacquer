package assets

import (
	"bytes"
	"encoding/json"
	"fmt"
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
