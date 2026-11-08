package assets

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/patrickserrano/lacquer/internal/config"
)

func TestDocumentationHookRegion(t *testing.T) {
	for _, enabled := range []bool{false, true} {
		cfg := &config.Config{IOS: config.IOS{DocumentationHook: enabled}}
		src := filepath.Join(t.TempDir(), ".pre-commit-config.yaml")
		body := "before\n# lacquer:documentation-hook:begin\ndocs\n# lacquer:documentation-hook:end\nafter\n"
		if err := os.WriteFile(src, []byte(body), 0600); err != nil {
			t.Fatal(err)
		}
		got, _, err := Render(Asset{Src: src, Dest: ".pre-commit-config.yaml"}, cfg)
		want := "before\nafter\n"
		if enabled {
			want = "before\ndocs\nafter\n"
		}
		if err != nil || string(got) != want {
			t.Fatalf("enabled=%t: %q, %v; want %q", enabled, got, err, want)
		}
	}
}

func TestDocumentationHookMalformedRegion(t *testing.T) {
	for _, body := range []string{
		"# lacquer:documentation-hook:begin\n",
		"# lacquer:documentation-hook:end\n",
		"# lacquer:documentation-hook:end\n# lacquer:documentation-hook:begin\n",
		"# lacquer:documentation-hook:begin\n# lacquer:documentation-hook:begin\n# lacquer:documentation-hook:end\n",
	} {
		src := filepath.Join(t.TempDir(), ".pre-commit-config.yaml")
		if err := os.WriteFile(src, []byte(body), 0600); err != nil {
			t.Fatal(err)
		}
		if _, _, err := Render(Asset{Src: src, Dest: ".pre-commit-config.yaml"}, &config.Config{}); err == nil {
			t.Fatalf("malformed optional hook region accepted: %q", body)
		}
	}
}
