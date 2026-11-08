package config

import "testing"

func TestIOSDocumentationHookOption(t *testing.T) {
	for _, tc := range []struct {
		value string
		want  bool
		bad   bool
	}{
		{"", false, false},
		{"false", false, false},
		{"true", true, false},
		{"'true'", false, true},
		{"1", false, true},
	} {
		t.Run(tc.value, func(t *testing.T) {
			text := "[project]\nname='demo'\n"
			if tc.value != "" {
				text += "[ios]\ndocumentation_hook=" + tc.value + "\n"
			}
			cfg, err := loadString(t, text)
			if tc.bad {
				if err == nil {
					t.Fatal("non-boolean hook option was accepted")
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			if cfg.IOS.DocumentationHook != tc.want {
				t.Fatalf("documentation_hook=%t, want %t", cfg.IOS.DocumentationHook, tc.want)
			}
		})
	}
}
