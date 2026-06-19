package config
import "testing"
func TestTrailingSlashExclude(t *testing.T){
  if err := validateComponentPath(".github/workflows/"); err != nil {
    t.Logf("trailing-slash rejected: %v", err)
  } else {
    t.Logf("trailing-slash accepted")
  }
}
