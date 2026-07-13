// Package skillsuggest scans a component's Swift imports and suggests
// third-party skill packages (installable via the `skills` CLI,
// https://github.com/vercel-labs/skills) worth adding to [project].skills.
// It is advisory only: `lacquer init` writes its output as a starting point,
// never a silent decision — an unmapped or unused import suggests nothing.
package skillsuggest

import (
	"bufio"
	"os"
	"path/filepath"
	"regexp"
	"sort"
)

// skipDirs mirrors internal/detect's skip list: these never hold project
// source worth scanning for imports.
var skipDirs = map[string]bool{
	".git": true, ".worktrees": true, "node_modules": true,
	"DerivedData": true, ".build": true, "vendor": true, ".agents": true,
	"Pods": true, "Carthage": true,
}

// frameworkSkills maps a Swift import's module name to the third-party skill
// package that documents it, sourced from dpearson2699/swift-ios-skills (the
// Apple-framework skill collection already in use across this fleet — see
// ~/.agents/.skill-lock.json). Only frameworks with a real, findable skill in
// that collection are listed; an import with no entry here suggests nothing.
var frameworkSkills = map[string]string{
	"ActivityKit":            "dpearson2699/swift-ios-skills@activitykit",
	"AppIntents":             "dpearson2699/swift-ios-skills@app-intents",
	"AuthenticationServices": "dpearson2699/swift-ios-skills@authentication",
	"AVKit":                  "dpearson2699/swift-ios-skills@avkit",
	"BackgroundTasks":        "dpearson2699/swift-ios-skills@background-processing",
	"Charts":                 "dpearson2699/swift-ios-skills@swift-charts",
	"CloudKit":               "dpearson2699/swift-ios-skills@cloudkit",
	"CryptoKit":              "dpearson2699/swift-ios-skills@cryptokit",
	"DeviceCheck":            "dpearson2699/swift-ios-skills@device-integrity",
	"FoundationModels":       "dpearson2699/swift-ios-skills@apple-on-device-ai",
	"HealthKit":              "dpearson2699/swift-ios-skills@healthkit",
	"LocalAuthentication":    "dpearson2699/swift-ios-skills@authentication",
	"MapKit":                 "dpearson2699/swift-ios-skills@mapkit",
	"MusicKit":               "dpearson2699/swift-ios-skills@musickit",
	"PDFKit":                 "dpearson2699/swift-ios-skills@pdfkit",
	"PhotosUI":               "dpearson2699/swift-ios-skills@photokit",
	"RealityKit":             "dpearson2699/swift-ios-skills@realitykit",
	"StoreKit":               "dpearson2699/swift-ios-skills@storekit",
	"SwiftData":              "dpearson2699/swift-ios-skills@swiftdata",
	"Testing":                "dpearson2699/swift-ios-skills@swift-testing",
	"UserNotifications":      "dpearson2699/swift-ios-skills@push-notifications",
	"Vision":                 "dpearson2699/swift-ios-skills@vision-framework",
	"VisionKit":              "dpearson2699/swift-ios-skills@vision-framework",
	"WidgetKit":              "dpearson2699/swift-ios-skills@widgetkit",
}

// importRe matches a top-of-file `import Foo` / `import Foo.Bar` line and
// captures the leading module name. `@testable import` and submodule imports
// (Foundation.Bundle) both still yield a usable module name from group 1.
var importRe = regexp.MustCompile(`^\s*(?:@testable\s+)?import\s+([A-Za-z_][A-Za-z0-9_]*)`)

// Suggest walks componentRoot for .swift files, collects every imported
// module with a known skill mapping, and returns deduplicated, sorted
// "<owner>/<repo>@<skill-name>" entries ready to paste into
// [project].skills. It never fails on an unreadable individual file — a
// suggestion is best-effort, not a gate.
func Suggest(componentRoot string) ([]string, error) {
	found := map[string]bool{}

	err := filepath.Walk(componentRoot, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return nil // best-effort: skip what we can't stat
		}
		if info.IsDir() {
			if skipDirs[info.Name()] {
				return filepath.SkipDir
			}
			return nil
		}
		if filepath.Ext(path) != ".swift" {
			return nil
		}
		scanFile(path, found)
		return nil
	})
	if err != nil {
		return nil, err
	}

	entries := make([]string, 0, len(found))
	for skill := range found {
		entries = append(entries, skill)
	}
	sort.Strings(entries)
	return entries, nil
}

func scanFile(path string, found map[string]bool) {
	f, err := os.Open(path)
	if err != nil {
		return
	}
	defer f.Close()

	scanner := bufio.NewScanner(f)
	for scanner.Scan() {
		m := importRe.FindStringSubmatch(scanner.Text())
		if m == nil {
			continue
		}
		if skill, ok := frameworkSkills[m[1]]; ok {
			found[skill] = true
		}
	}
}
