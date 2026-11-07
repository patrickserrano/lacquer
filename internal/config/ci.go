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
	if c.IOSTestStepTimeoutMinutes() > c.IOSTestJobTimeoutMinutes() {
		return fmt.Errorf("[ci].ios_test_step_timeout_minutes (%d) must not exceed ios_test_job_timeout_minutes (%d), including defaults", c.IOSTestStepTimeoutMinutes(), c.IOSTestJobTimeoutMinutes())
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
