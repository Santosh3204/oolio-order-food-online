package coupon

import (
	"io"
	"strings"
	"testing"
)

func src(s string) Source {
	return func() (io.ReadCloser, error) { return io.NopCloser(strings.NewReader(s)), nil }
}

func TestSetValidator(t *testing.T) {
	v, err := NewSetValidator(
		src("HAPPYHRS\nFIFTYOFF\nSUPER100\nSHORT\nTOOLONGCODE1\nONLYONE12\n"),
		src("HAPPYHRS\nFIFTYOFF\nHAPPYHRS\n"), // duplicate within one file must not count twice
		src("SUPER100\nSUPER100\nFIFTYOFF\n"),
	)
	if err != nil {
		t.Fatal(err)
	}
	for code, want := range map[string]bool{
		"HAPPYHRS": true, "FIFTYOFF": true, "SUPER100": true,
		"ONLYONE12": false, "SHORT": false, "TOOLONGCODE1": false, "NOPE1234": false,
	} {
		if got := v.IsValid(code); got != want {
			t.Errorf("%s: got %v want %v", code, got, want)
		}
	}
}

func TestDuplicateInOneFileNotEnough(t *testing.T) {
	v, _ := NewSetValidator(src("DUPLICATE\nDUPLICATE\n"), src("OTHERCODE\n"))
	if v.IsValid("DUPLICATE") {
		t.Fatal("code in only one file must be invalid")
	}
}
