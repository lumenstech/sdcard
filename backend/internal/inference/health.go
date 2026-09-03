package inference

import (
	"context"
	"fmt"
	"net/http"
	"strings"
	"time"
)

// Health verifies Ollama is reachable without running inference.
func (s *Service) Health(ctx context.Context) error {
	healthCtx, cancel := context.WithTimeout(ctx, 3*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(healthCtx, http.MethodGet, strings.TrimRight(s.cfg.OllamaURL, "/")+"/api/tags", nil)
	if err != nil {
		return err
	}
	resp, err := s.client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return fmt.Errorf("ollama health status %d", resp.StatusCode)
	}
	return nil
}
