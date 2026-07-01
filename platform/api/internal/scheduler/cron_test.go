package scheduler

import (
	"testing"
	"time"
)

// TestNextFromSpec verifies that nextFromSpec returns a future time for valid
// specs and nil for invalid ones.
func TestNextFromSpec(t *testing.T) {
	now := time.Now()

	tests := []struct {
		name      string
		spec      string
		wantNil   bool
		wantAfter time.Time
	}{
		{
			name:      "every minute (6-field)",
			spec:      "0 * * * * *",
			wantAfter: now,
		},
		{
			name:      "@every 1m descriptor",
			spec:      "@every 1m",
			wantAfter: now,
		},
		{
			name:      "@hourly descriptor",
			spec:      "@hourly",
			wantAfter: now,
		},
		{
			name:    "invalid spec returns nil",
			spec:    "not-a-spec",
			wantNil: true,
		},
		{
			name:    "empty spec returns nil",
			spec:    "",
			wantNil: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := nextFromSpec(tt.spec)
			if tt.wantNil {
				if got != nil {
					t.Errorf("nextFromSpec(%q) = %v, want nil", tt.spec, got)
				}
				return
			}
			if got == nil {
				t.Fatalf("nextFromSpec(%q) = nil, want a future time", tt.spec)
			}
			if !got.After(tt.wantAfter) {
				t.Errorf("nextFromSpec(%q) = %v, want time after %v", tt.spec, got, tt.wantAfter)
			}
		})
	}
}
