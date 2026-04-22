package main

import (
	"testing"
)

func TestParseHexRGB(t *testing.T) {
	tests := []struct {
		in       string
		wantR    uint8
		wantG    uint8
		wantB    uint8
		wantOK   bool
	}{
		{"#8b7aff", 0x8b, 0x7a, 0xff, true},
		{"8B7AFF", 0x8b, 0x7a, 0xff, true},
		{"#abc", 0xaa, 0xbb, 0xcc, true},
		{"", 0, 0, 0, false},
		{"#gg0000", 0, 0, 0, false},
	}
	for _, tc := range tests {
		c, ok := ParseHexRGB(tc.in)
		if ok != tc.wantOK {
			t.Fatalf("ParseHexRGB(%q) ok=%v want %v", tc.in, ok, tc.wantOK)
		}
		if !ok {
			continue
		}
		if c.R != tc.wantR || c.G != tc.wantG || c.B != tc.wantB || c.A != 0xff {
			t.Fatalf("ParseHexRGB(%q) = %+v want R=%d G=%d B=%d A=255", tc.in, c, tc.wantR, tc.wantG, tc.wantB)
		}
	}
}
