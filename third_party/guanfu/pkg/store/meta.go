package store

// AssetMeta tracks metadata for a single asset's price archive.
type AssetMeta struct {
	LastDate  string `json:"last_date"`
	Count     int    `json:"count"`
	Source    string `json:"source,omitempty"`
	UpdatedAt string `json:"updated_at"`
}
