package config

import (
	"fmt"
	"path/filepath"
	"strings"
)

// CI holds optional limits for the iOS Test job. Pointers distinguish an absent
// setting (use the existing default) from an explicit zero (invalid).
type CI struct {
	IOSTestJobTimeoutMinutesSetting  *int `toml:"ios_test_job_timeout_minutes"`
	IOSTestStepTimeoutMinutesSetting *int `toml:"ios_test_step_timeout_minutes"`

	// IOSFailOnOwnSourceWarnings opts the Build and Test jobs into failing when
	// xcodebuild prints a Swift `warning:` whose file is inside this repository's
	// own sources. Off unless the manifest says so: a project that does not ask
	// keeps the workflow it has, byte for byte.
	IOSFailOnOwnSourceWarnings bool `toml:"ios_fail_on_own_source_warnings"`
	// IOSOwnSourceWarningsAllowlistSetting names the project's file of regexes
	// for warnings that are known and accepted. A pointer so an explicit empty
	// string (invalid) is told apart from an absent key (the default path).
	IOSOwnSourceWarningsAllowlistSetting *string `toml:"ios_own_source_warnings_allowlist"`
}

// DefaultOwnSourceWarningsAllowlist is where the gate looks for allowlisted
// warnings when the manifest does not name a file. It sits beside
// .lacquer.toml and .lacquer.lock. A missing file is an empty allowlist.
const DefaultOwnSourceWarningsAllowlist = ".lacquer-warning-allowlist"

// IOSOwnSourceWarningsAllowlist returns the configured allowlist path or the
// default.
func (c CI) IOSOwnSourceWarningsAllowlist() string {
	if c.IOSOwnSourceWarningsAllowlistSetting != nil {
		return *c.IOSOwnSourceWarningsAllowlistSetting
	}
	return DefaultOwnSourceWarningsAllowlist
}

// IOSTestJobTimeoutMinutes returns the configured job limit or 20 minutes.
func (c CI) IOSTestJobTimeoutMinutes() int {
	if c.IOSTestJobTimeoutMinutesSetting != nil {
		return *c.IOSTestJobTimeoutMinutesSetting
	}
	return 20
}

// IOSTestStepTimeoutMinutes returns the configured test-step limit or 10 minutes.
func (c CI) IOSTestStepTimeoutMinutes() int {
	if c.IOSTestStepTimeoutMinutesSetting != nil {
		return *c.IOSTestStepTimeoutMinutesSetting
	}
	return 10
}

func validateCI(c CI) error {
	for _, f := range []struct {
		key   string
		value *int
	}{
		{"ios_test_job_timeout_minutes", c.IOSTestJobTimeoutMinutesSetting},
		{"ios_test_step_timeout_minutes", c.IOSTestStepTimeoutMinutesSetting},
	} {
		if f.value != nil && (*f.value < 1 || *f.value > 360) {
			return fmt.Errorf("[ci].%s must be a positive integer from 1 to 360 minutes", f.key)
		}
	}
	if err := validateOwnSourceWarnings(c); err != nil {
		return err
	}
	if c.IOSTestStepTimeoutMinutes() > c.IOSTestJobTimeoutMinutes() {
		return fmt.Errorf("[ci].ios_test_step_timeout_minutes (%d) must not exceed ios_test_job_timeout_minutes (%d), including defaults", c.IOSTestStepTimeoutMinutes(), c.IOSTestJobTimeoutMinutes())
	}
	return nil
}

func validateOwnSourceWarnings(c CI) error {
	if c.IOSOwnSourceWarningsAllowlistSetting == nil {
		return nil
	}
	// An allowlist with the gate off is read by nothing. Say so rather than
	// let a setting that looks effective do nothing.
	if !c.IOSFailOnOwnSourceWarnings {
		return fmt.Errorf("[ci].ios_own_source_warnings_allowlist is set, but ios_fail_on_own_source_warnings is not true, so nothing would read it")
	}
	path := *c.IOSOwnSourceWarningsAllowlistSetting
	if path == "" {
		return fmt.Errorf("[ci].ios_own_source_warnings_allowlist must not be empty; omit the key to use %s", DefaultOwnSourceWarningsAllowlist)
	}
	if !filepath.IsLocal(path) {
		return fmt.Errorf("[ci].ios_own_source_warnings_allowlist must be a relative path inside the repository")
	}
	// Quoted in shell like xcodeproj and package_resolved, so the same charset.
	if !xcodeprojVal.MatchString(filepath.ToSlash(path)) {
		return fmt.Errorf("[ci].ios_own_source_warnings_allowlist contains unsafe path characters")
	}
	return nil
}

func validatePackageResolved(cfg *Config) error {
	source := cfg.IOS.PackageResolved
	if source == "" {
		return nil
	}
	if !filepath.IsLocal(source) {
		return fmt.Errorf("[ios].package_resolved must be a relative path inside the repository")
	}
	// Every use is quoted in shell, like xcodeproj and secrets_file, so the
	// shared quoted-path charset safely admits spaces in an existing path.
	if !xcodeprojVal.MatchString(filepath.ToSlash(source)) {
		return fmt.Errorf("[ios].package_resolved contains unsafe path characters")
	}
	source = filepath.ToSlash(filepath.Clean(source))
	project := filepath.ToSlash(filepath.Clean(cfg.Project.Xcodeproj))
	if source == "." || cfg.Project.Xcodeproj == "" || source == project || strings.HasPrefix(source, project+"/") {
		return fmt.Errorf("[ios].package_resolved must name a repository-relative source file outside [project].xcodeproj, which must be set")
	}
	return nil
}
