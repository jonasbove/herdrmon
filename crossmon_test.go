package main

import "testing"

// Pinned against sidebar/lib/species.js defaultMon: all parts must agree.
func TestDefaultMonCrossLanguage(t *testing.T) {
	for name, want := range map[string]string{
		"workstation": "onix", "printer": "claydol", "nas-01": "magneton", "My Laptop": "electrode",
	} {
		if got := defaultMon(name); got != want {
			t.Errorf("defaultMon(%q) = %q, want %q", name, got, want)
		}
	}
}
