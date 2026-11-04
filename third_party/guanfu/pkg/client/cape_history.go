// Shiller CAPE refresh via the existing scripts/import_cape.py wrapper.
//
// import_cape.py downloads the Yale/Shiller XLS, parses the CAPE column,
// and writes ~/.guanfu/prices/spx_cape.json directly. We invoke it and
// then re-Save through PriceStore so meta.json is updated too — the script
// bypasses meta and we want the unified status table to see fresh entries.
//
// CAPE is monthly, so a 1-day "fresh" threshold is overly aggressive:
// instead we only re-run when last_date is older than 28 days.

package client

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"time"

	"github.com/Ricaardo/nimbus-os/guanfu/pkg/store"
)

// CAPESource refreshes spx_cape via the Python script.
type CAPESource struct{}

func (CAPESource) Key() string         { return "spx_cape" }
func (CAPESource) DisplayName() string { return "spx_cape (Shiller CAPE, monthly)" }

const capeMonthlyTTL = 28 * 24 * time.Hour

func (CAPESource) Refresh(ctx context.Context, s *store.PriceStore) (*RefreshResult, error) {
	// Custom freshness check (monthly cadence).
	last, _ := s.LastDate("spx_cape")
	if last != "" {
		if t, err := time.Parse("2006-01-02", last); err == nil {
			if time.Since(t) < capeMonthlyTTL {
				// The script writes the JSON directly and bypasses meta; a manual
				// script run followed by fresh-skips here leaves meta.json stale
				// forever (status then shows a bogus "! Nd ago"). Reconcile.
				if m, ok := s.GetMeta("spx_cape"); !ok || m.LastDate != last {
					if pts, err := s.Load("spx_cape"); err == nil && len(pts) > 0 {
						for i := range pts {
							if pts[i].Source == "" {
								pts[i].Source = "shiller:cape"
							}
						}
						_ = s.Save("spx_cape", pts) // best-effort meta sync
					}
				}
				return freshSkipResult("spx_cape", "spx_cape (Shiller CAPE, monthly)", last, s), nil
			}
		}
	}

	script, err := findCAPEScriptPath()
	if err != nil {
		count, _ := s.Count("spx_cape")
		return &RefreshResult{
			Key: "spx_cape", DisplayName: "spx_cape (Shiller CAPE, monthly)",
			Mode: "skip", SkipReason: "config", Stale: last != "", Action: "configure",
			Total: count, LastDate: last, Error: err.Error(),
		}, nil
	}

	// Script writes directly to ~/.guanfu/prices/spx_cape.json.
	cmd := exec.CommandContext(ctx, "python3", script)
	cmd.Env = append(os.Environ(), "PYTHONIOENCODING=utf-8")
	if out, err := cmd.CombinedOutput(); err != nil {
		return nil, fmt.Errorf("import_cape.py: %w (output: %s)", err, string(out))
	}

	// Re-Save through PriceStore so meta.json reflects the new last_date.
	pts, err := s.Load("spx_cape")
	if err != nil {
		return nil, fmt.Errorf("post-script load spx_cape: %w", err)
	}
	if len(pts) == 0 {
		return nil, fmt.Errorf("spx_cape empty after script run")
	}
	// Tag source if missing (legacy data may lack it).
	for i := range pts {
		if pts[i].Source == "" {
			pts[i].Source = "shiller:cape"
		}
	}
	if err := s.Save("spx_cape", pts); err != nil {
		return nil, fmt.Errorf("re-save spx_cape: %w", err)
	}

	count, _ := s.Count("spx_cape")
	newLast, _ := s.LastDate("spx_cape")
	mode := "full"
	if last != "" {
		mode = "incremental"
	}
	return &RefreshResult{
		Key: "spx_cape", DisplayName: "spx_cape (Shiller CAPE, monthly)",
		Mode: mode, Added: count, Total: count, LastDate: newLast,
	}, nil
}

func findCAPEScriptPath() (string, error) {
	candidates := []string{
		"scripts/import_cape.py",
		filepath.Join("..", "scripts", "import_cape.py"),
	}
	if home, err := os.UserHomeDir(); err == nil {
		candidates = append(candidates,
			filepath.Join(home, "guanfu", "scripts", "import_cape.py"),
		)
	}
	for _, p := range candidates {
		if _, err := os.Stat(p); err == nil {
			return p, nil
		}
	}
	return "", fmt.Errorf("import_cape.py not found in any of %v", candidates)
}
