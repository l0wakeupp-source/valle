package config

import "testing"

func TestValidateCacheRetention(t *testing.T) {
	valid := []string{"", "long", "none"}
	for _, v := range valid {
		if err := ValidateCacheRetention(v); err != nil {
			t.Errorf("ValidateCacheRetention(%q) = %v, want nil", v, err)
		}
	}
	for _, v := range []string{"short", "LONG", "24h", "true"} {
		if err := ValidateCacheRetention(v); err == nil {
			t.Errorf("ValidateCacheRetention(%q) = nil, want error", v)
		}
	}
}
