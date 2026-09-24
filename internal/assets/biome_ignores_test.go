package assets

import (
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"testing"

	"github.com/patrickserrano/lacquer/internal/config"
	"github.com/patrickserrano/lacquer/internal/tokens"
)

func TestBiomeManifestIgnoresReachRenderedConfig(t *testing.T) {
	for _, prefix := range []string{"", "apps/admin/"} {
		for _, extra := range []string{"", "[web]\nbiome_ignores = ['!**/payload-types.ts', '!**/quote\"name.ts']\n"} {
			t.Run(prefix+extra, func(t *testing.T) {
				dir := t.TempDir()
				manifest := filepath.Join(dir, ".lacquer.toml")
				write(t, manifest, "[project]\nname='demo'\n"+extra)
				cfg, err := config.Load(manifest)
				if err != nil {
					t.Fatal(err)
				}
				a := Asset{Src: "../../profiles/web/config/biome.json", Dest: prefix + "biome.json", Prefix: prefix}
				got, missing, err := Render(a, cfg)
				if err != nil || len(missing) > 0 {
					t.Fatalf("render: %v %v", err, missing)
				}
				original, err := os.ReadFile(a.Src)
				if err != nil {
					t.Fatal(err)
				}
				want, _ := tokens.Substitute(string(original), tokens.Values(cfg, prefix))
				if extra == "" {
					if string(got) != want {
						t.Fatal("no opt-in must be byte-identical")
					}
					return
				}
				var doc, base struct {
					Files struct {
						Includes []string `json:"includes"`
					} `json:"files"`
				}
				if err := json.Unmarshal(got, &doc); err != nil {
					t.Fatal(err)
				}
				if err := json.Unmarshal([]byte(want), &base); err != nil {
					t.Fatal(err)
				}
				includes := append(base.Files.Includes, "!**/payload-types.ts", "!**/quote\"name.ts")
				if !reflect.DeepEqual(doc.Files.Includes, includes) {
					t.Fatalf("includes=%q want %q", doc.Files.Includes, includes)
				}
			})
		}
	}
}
