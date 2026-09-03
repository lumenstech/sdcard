package inference

import (
	"context"
	"strings"
	"testing"
	"time"
)

func TestCleanJSONStripsFences(t *testing.T) {
	got := cleanJSON("```json\n[{\"class\":\"person\",\"confidence\":0.9,\"bbox\":[1,2,3,4]}]\n```")
	if !strings.HasPrefix(got, "[") || !strings.HasSuffix(got, "]") { t.Fatalf("unexpected cleaned json: %q", got) }
}

func TestShouldAlertThresholds(t *testing.T) {
	cases := []struct{ d Detection; want bool }{
		{Detection{Class:"person", Confidence:0.65}, true},
		{Detection{Class:"person", Confidence:0.64}, false},
		{Detection{Class:"vehicle", Confidence:0.70}, true},
		{Detection{Class:"vehicle", Confidence:0.69}, false},
		{Detection{Class:"animal", Confidence:0.99}, false},
	}
	for _, tc := range cases {
		if got := tc.d.ShouldAlert(); got != tc.want { t.Fatalf("%+v ShouldAlert=%v want %v", tc.d, got, tc.want) }
	}
}

func TestDetectFailsFastWhenOllamaUnavailable(t *testing.T) {
	svc := NewService(Config{OllamaURL:"http://127.0.0.1:1", Model:"llava", Confidence:0.5})
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	if _, err := svc.Detect(ctx, []byte("not-a-real-jpeg")); err == nil { t.Fatal("expected ollama error") }
}
