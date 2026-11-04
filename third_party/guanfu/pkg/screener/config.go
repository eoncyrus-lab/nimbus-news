// Strategy profile config — YAML file loading.
//
// Profiles can be defined inline (model.BuiltinStrategyProfiles()) or
// loaded from guanfu/pkg/screener/strategies.yaml for customization.

package screener

import (
	"os"

	"gopkg.in/yaml.v3"

	"github.com/Ricaardo/nimbus-os/guanfu/pkg/model"
)

// profilesDoc is the top-level YAML structure.
type profilesDoc struct {
	Profiles []model.StrategyProfile `yaml:"profiles"`
}

// LoadProfiles reads strategy profiles from a YAML file.
// Falls back to BuiltinStrategyProfiles() if the file doesn't exist.
func LoadProfiles(path string) ([]model.StrategyProfile, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var doc profilesDoc
	if err := yaml.Unmarshal(data, &doc); err != nil {
		return nil, err
	}
	return doc.Profiles, nil
}

// LoadProfilesOrDefault reads from path, returns builtins on any error.
func LoadProfilesOrDefault(path string) []model.StrategyProfile {
	profiles, err := LoadProfiles(path)
	if err != nil || len(profiles) == 0 {
		return model.BuiltinStrategyProfiles()
	}
	return profiles
}

// FindProfile finds a profile by market+style, case-insensitive.
func FindProfile(profiles []model.StrategyProfile, market, style string) *model.StrategyProfile {
	for i, p := range profiles {
		if equalFold(p.Market, market) && equalFold(p.Style, style) {
			return &profiles[i]
		}
	}
	return nil
}

func equalFold(a, b string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := 0; i < len(a); i++ {
		ca, cb := a[i], b[i]
		if ca >= 'A' && ca <= 'Z' {
			ca += 32
		}
		if cb >= 'A' && cb <= 'Z' {
			cb += 32
		}
		if ca != cb {
			return false
		}
	}
	return true
}
