package handlers

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestAuthMiddlewareRejectsMissingAndInvalid(t *testing.T) {
	next := func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(http.StatusNoContent) }
	h := AuthMiddleware([]string{"pilot-key"}, next)

	for _, auth := range []string{"", "Bearer wrong"} {
		req := httptest.NewRequest(http.MethodGet, "/", nil)
		if auth != "" { req.Header.Set("Authorization", auth) }
		rr := httptest.NewRecorder()
		h(rr, req)
		if rr.Code != http.StatusUnauthorized { t.Fatalf("auth %q status=%d", auth, rr.Code) }
		if rr.Header().Get("Content-Type") != "application/json" { t.Fatalf("auth %q content-type=%q", auth, rr.Header().Get("Content-Type")) }
	}
}

func TestAuthMiddlewareAcceptsValid(t *testing.T) {
	h := AuthMiddleware([]string{"pilot-key"}, func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(http.StatusNoContent) })
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.Header.Set("Authorization", "Bearer pilot-key")
	rr := httptest.NewRecorder()
	h(rr, req)
	if rr.Code != http.StatusNoContent { t.Fatalf("status=%d", rr.Code) }
}

func TestRateLimitIsPerDeviceAndBounded(t *testing.T) {
	p := NewPipeline(PipelineConfig{RateLimitPerMinute: 2, QueueSize: 1, Workers: 1})
	now := p.ctx.Value(struct{}{})
	_ = now
	if !p.allowDevice("a", testNow()) || !p.allowDevice("a", testNow()) { t.Fatal("first two should pass") }
	if p.allowDevice("a", testNow()) { t.Fatal("third should be rate limited") }
	if !p.allowDevice("b", testNow()) { t.Fatal("second device should have independent limit") }
}

func testNow() (t interface{ IsZero() bool }) { return nil }
