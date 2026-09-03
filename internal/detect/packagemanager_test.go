package detect

import (
	"os"
	"path/filepath"
	"testing"
)

// writeJSON writes package.json content at root/prefix/package.json.
func writeJSON(t *testing.T, root, prefix, content string) {
	t.Helper()
	dir := filepath.Join(root, filepath.FromSlash(prefix))
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "package.json"), []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

func TestWebPackageManagerPnpm(t *testing.T) {
	root := t.TempDir()
	writeJSON(t, root, "", `{"name":"acme","packageManager":"pnpm@10.20.0"}`)
	if got := WebPackageManager(root, ""); got != PackageManagerPnpm {
		t.Errorf("got %q, want %q", got, PackageManagerPnpm)
	}
}

// A missing package.json is the state of a project that has not adopted the
// web profile's Node stack at all yet — the historical default, and it must
// stay npm rather than silently assuming a lockfile nobody committed.
func TestWebPackageManagerMissingFileIsNpm(t *testing.T) {
	root := t.TempDir()
	if got := WebPackageManager(root, ""); got != PackageManagerNpm {
		t.Errorf("got %q, want %q", got, PackageManagerNpm)
	}
}

// A package.json with no packageManager field at all — an ordinary npm
// project that never opted in — must render npm, not silently inherit pnpm.
func TestWebPackageManagerNoFieldIsNpm(t *testing.T) {
	root := t.TempDir()
	writeJSON(t, root, "", `{"name":"acme"}`)
	if got := WebPackageManager(root, ""); got != PackageManagerNpm {
		t.Errorf("got %q, want %q", got, PackageManagerNpm)
	}
}

// An explicit non-pnpm packageManager (yarn, or an npm version pin) must not
// be misread as pnpm just because the field is present.
func TestWebPackageManagerYarnFieldIsNpm(t *testing.T) {
	root := t.TempDir()
	writeJSON(t, root, "", `{"name":"acme","packageManager":"yarn@4.0.0"}`)
	if got := WebPackageManager(root, ""); got != PackageManagerNpm {
		t.Errorf("got %q, want %q", got, PackageManagerNpm)
	}
}

// Malformed JSON must fail closed to npm, not panic or propagate an error —
// WebPackageManager has no error return, by design (see its doc comment).
func TestWebPackageManagerMalformedJSONIsNpm(t *testing.T) {
	root := t.TempDir()
	writeJSON(t, root, "", `{ this is not json`)
	if got := WebPackageManager(root, ""); got != PackageManagerNpm {
		t.Errorf("got %q, want %q", got, PackageManagerNpm)
	}
}

// The component's OWN package.json governs, not the repo root's. A pnpm root
// with an npm (or absent) nested component must not leak its choice down, and
// vice versa — pnpm/action-setup's package_json_file input makes exactly this
// same distinction, and getting it wrong silently applies one component's
// package manager to another's CI job.
func TestWebPackageManagerIsPerComponent(t *testing.T) {
	root := t.TempDir()
	writeJSON(t, root, "", `{"name":"acme","packageManager":"pnpm@10.20.0"}`)
	writeJSON(t, root, "admin", `{"name":"acme-admin"}`)

	if got := WebPackageManager(root, ""); got != PackageManagerPnpm {
		t.Errorf("root: got %q, want %q", got, PackageManagerPnpm)
	}
	if got := WebPackageManager(root, "admin/"); got != PackageManagerNpm {
		t.Errorf("admin/: got %q, want %q — must not inherit the root's pnpm choice", got, PackageManagerNpm)
	}
}

// An unknown root (a Config built in memory, as every unit test that
// constructs config.Config{} literally does) must not fall through to a
// RELATIVE package.json read against the calling process's own working
// directory — it must read as "no project at all", i.e. npm.
func TestWebPackageManagerEmptyRootIsNpm(t *testing.T) {
	if got := WebPackageManager("", ""); got != PackageManagerNpm {
		t.Errorf("got %q, want %q", got, PackageManagerNpm)
	}
}
