package device

import (
	"context"
	"fmt"
	"time"
)

// ReleaseFrame removes an ingest claim when admission fails before the frame is
// queued, allowing the sidecar to retry safely.
func (s *Store) ReleaseFrame(ctx context.Context, deviceID string, ts time.Time) error {
	_, err := s.pool.Exec(ctx, `DELETE FROM frame_receipts WHERE device_id=$1 AND timestamp=$2`, deviceID, ts)
	if err != nil {
		return fmt.Errorf("release frame claim: %w", err)
	}
	return nil
}
