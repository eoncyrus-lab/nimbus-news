package controlstore

import (
	"context"
	"fmt"
)

// Probe verifies that the control database can execute a query. Application
// readiness uses this instead of treating an open process as database-ready.
func (s *Store) Probe(ctx context.Context) error {
	var value int
	if err := s.db.QueryRowContext(ctx, `SELECT 1`).Scan(&value); err != nil {
		return fmt.Errorf("controlstore: probe: %w", err)
	}
	if value != 1 {
		return fmt.Errorf("controlstore: probe: got %d, want 1", value)
	}
	return nil
}
