package engine

import (
	"github.com/Ricaardo/nimbus-os/guanfu/pkg/assetprofile"
	"github.com/Ricaardo/nimbus-os/guanfu/pkg/model"
)

// AnnotatePanelProfile attaches asset-profile metadata to a panel without
// changing the stable indicator maps. This lets clients route reading behavior
// by profile while old JSON consumers continue to read cycle/valuation/etc.
func AnnotatePanelProfile(p *model.IndicatorPanel, asset string) {
	if p == nil {
		return
	}
	profile, ok := assetprofile.For(asset)
	if !ok {
		return
	}
	p.ProfileKey = profile.Key
	p.ProfileVersion = profile.Version
	p.AssetClass = string(profile.Class)
	p.SkillProfileURI = profile.SkillProfileURI
	p.DomainMeta = make([]model.PanelDomainMeta, 0, len(profile.ReadingDomains))
	for _, d := range profile.ReadingDomains {
		p.DomainMeta = append(p.DomainMeta, model.PanelDomainMeta{
			Key:     d.Key,
			Title:   d.Title,
			Icon:    d.Icon,
			Purpose: d.Purpose,
		})
	}
}
