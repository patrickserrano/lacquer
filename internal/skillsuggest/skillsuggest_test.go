package skillsuggest

import (
	"os"
	"path/filepath"
	"testing"
)

func write(t *testing.T, path, body string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
}

func TestSuggestFindsKnownFrameworks(t *testing.T) {
	root := t.TempDir()
	write(t, filepath.Join(root, "Model.swift"), "import Foundation\nimport HealthKit\n\nstruct M {}\n")
	write(t, filepath.Join(root, "Purchases.swift"), "import StoreKit\nimport SwiftUI\n")

	got, err := Suggest(root)
	if err != nil {
		t.Fatalf("Suggest: %v", err)
	}
	want := []string{
		"dpearson2699/swift-ios-skills@healthkit",
		"dpearson2699/swift-ios-skills@storekit",
	}
	if len(got) != len(want) {
		t.Fatalf("got %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("got[%d] = %q, want %q", i, got[i], want[i])
		}
	}
}

func TestSuggestDeduplicates(t *testing.T) {
	root := t.TempDir()
	write(t, filepath.Join(root, "A.swift"), "import HealthKit\n")
	write(t, filepath.Join(root, "B.swift"), "import HealthKit\n")

	got, err := Suggest(root)
	if err != nil {
		t.Fatalf("Suggest: %v", err)
	}
	if len(got) != 1 {
		t.Errorf("got %v, want exactly one deduplicated entry", got)
	}
}

func TestSuggestIgnoresUnmappedAndUnrelatedImports(t *testing.T) {
	root := t.TempDir()
	write(t, filepath.Join(root, "A.swift"), "import Foundation\nimport SwiftUI\nimport SomeRandomThirdPartyPackage\n")

	got, err := Suggest(root)
	if err != nil {
		t.Fatalf("Suggest: %v", err)
	}
	if len(got) != 0 {
		t.Errorf("got %v, want no suggestions (no known framework imported)", got)
	}
}

func TestSuggestSkipsDependencyDirs(t *testing.T) {
	root := t.TempDir()
	// A HealthKit import inside Pods/ must not surface a suggestion — it's
	// vendored dependency code, not this project's own usage.
	write(t, filepath.Join(root, "Pods", "SomeDep", "File.swift"), "import HealthKit\n")

	got, err := Suggest(root)
	if err != nil {
		t.Fatalf("Suggest: %v", err)
	}
	if len(got) != 0 {
		t.Errorf("got %v, want no suggestions from vendored Pods/ source", got)
	}
}

func TestSuggestSkipsSuffixedDerivedDataDirs(t *testing.T) {
	root := t.TempDir()
	// This fleet's own convention is a unique per-worktree derived-data path
	// like DerivedData-<feature> (see profiles/ios's "Working in worktrees"
	// guidance), never bare "DerivedData". A StoreKit import inside Xcode's
	// cached SPM checkout under a suffixed DerivedData dir must not surface —
	// it's third-party dependency source, not this project's own usage.
	write(t, filepath.Join(root, "DerivedData-shots", "SourcePackages", "checkouts", "purchases-ios", "File.swift"), "import StoreKit\n")
	write(t, filepath.Join(root, "Model.swift"), "import Foundation\n")

	got, err := Suggest(root)
	if err != nil {
		t.Fatalf("Suggest: %v", err)
	}
	if len(got) != 0 {
		t.Errorf("got %v, want no suggestions from a suffixed DerivedData-* dependency checkout", got)
	}
}

func TestSuggestHandlesTestableImport(t *testing.T) {
	root := t.TempDir()
	write(t, filepath.Join(root, "Tests.swift"), "@testable import StoreKit\n")

	got, err := Suggest(root)
	if err != nil {
		t.Fatalf("Suggest: %v", err)
	}
	if len(got) != 1 || got[0] != "dpearson2699/swift-ios-skills@storekit" {
		t.Errorf("got %v", got)
	}
}

func TestSuggestOnEmptyDirReturnsEmpty(t *testing.T) {
	root := t.TempDir()
	got, err := Suggest(root)
	if err != nil {
		t.Fatalf("Suggest: %v", err)
	}
	if len(got) != 0 {
		t.Errorf("got %v, want empty", got)
	}
}
