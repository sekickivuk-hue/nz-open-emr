package nhi

import (
	"strings"
	"testing"
)

func TestValidateKnownGood(t *testing.T) {
	cases := []struct {
		in   string
		want Format
	}{
		{"ZZZ0016", FormatLegacy},
		{"ZZZ0024", FormatLegacy},
		{"ZZZ00AX", FormatNew},
		{"ALU18KZ", FormatNew},
		{"zzz0016", FormatLegacy}, // case-insensitive
	}
	for _, c := range cases {
		got, err := Validate(c.in)
		if err != nil || got != c.want {
			t.Errorf("Validate(%q) = %v, %v; want %v, nil", c.in, got, err, c.want)
		}
	}
}

func TestValidateRejects(t *testing.T) {
	bad := []string{"", "ZZZ0044", "ZZZZ000", "ZZZ?000", "ZZZ0017", "ZZZ00AY", "IIIX000", "ZZZ001", "ZZZ00165"}
	for _, s := range bad {
		if _, err := Validate(s); err == nil {
			t.Errorf("Validate(%q) accepted, want reject", s)
		}
	}
}

func TestValidateRejectsAllOtherChecksums(t *testing.T) {
	// For a known-valid NHI, every other checksum char must fail.
	for _, base := range []string{"ZZZ001", "ZZZ00A"} {
		valid := 0
		for _, c := range "0123456789ABCDEFGHIJKLMNOPQRSTUVWXYZ" {
			if _, err := Validate(base + string(c)); err == nil {
				valid++
			}
		}
		if valid != 1 {
			t.Errorf("base %q: %d checksums accepted, want exactly 1", base, valid)
		}
	}
}

func TestGenerateSyntheticRoundTrip(t *testing.T) {
	for _, f := range []Format{FormatLegacy, FormatNew} {
		for i := 0; i < 500; i++ {
			s, err := GenerateSynthetic(f)
			if err != nil {
				t.Fatal(err)
			}
			if !strings.HasPrefix(s, "Z") {
				t.Fatalf("synthetic NHI %q must start with reserved test prefix Z", s)
			}
			got, err := Validate(s)
			if err != nil || got != f {
				t.Fatalf("generated %q: Validate = %v, %v; want %v", s, got, err, f)
			}
		}
	}
}
