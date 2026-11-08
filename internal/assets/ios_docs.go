package assets

import (
	"bytes"
	"fmt"
)

// renderDocumentationHook removes only the marked optional entry, retaining
// every other byte. Comment markers keep the source template valid YAML too.
func renderDocumentationHook(body []byte, enabled bool) ([]byte, error) {
	begin := []byte("# lacquer:documentation-hook:begin\n")
	end := []byte("# lacquer:documentation-hook:end\n")
	first, last := bytes.Index(body, begin), bytes.Index(body, end)
	if first < 0 && last < 0 {
		return body, nil
	}
	if first < 0 || last < first || bytes.Count(body, begin) != 1 || bytes.Count(body, end) != 1 {
		return nil, fmt.Errorf("malformed optional documentation hook region in .pre-commit-config.yaml")
	}
	out := append([]byte{}, body[:first]...)
	if enabled {
		out = append(out, body[first+len(begin):last]...)
	}
	return append(out, body[last+len(end):]...), nil
}
