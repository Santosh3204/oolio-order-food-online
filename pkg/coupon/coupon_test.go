package coupon

import (
	"io"
	"slices"
	"strings"
	"testing"
)

func src(s string) Source {
	return func() (io.ReadCloser, error) { return io.NopCloser(strings.NewReader(s)), nil }
}

func TestBuildIndex(t *testing.T) {
	for _, parts := range []int{1, 7, 64} { // result must not depend on partitioning
		got, err := BuildIndex([]Source{
			src("HAPPYHRS\nFIFTYOFF\nSUPER100\nSHORT\nTOOLONGCODE1\nONLYONE12\nDUPLICATE\nDUPLICATE\n"),
			src("HAPPYHRS\nFIFTYOFF\nHAPPYHRS\n"),
			src("SUPER100\nSUPER100\nFIFTYOFF\n"),
		}, t.TempDir(), parts)
		if err != nil {
			t.Fatal(err)
		}
		want := []string{"FIFTYOFF", "HAPPYHRS", "SUPER100"}
		if !slices.Equal(got, want) {
			t.Fatalf("partitions=%d: got %v want %v", parts, got, want)
		}
	}
}

func TestSameFileTwiceDoesNotCount(t *testing.T) {
	got, _ := BuildIndex([]Source{src("DUPLICATE\nDUPLICATE\n"), src("OTHERCODE\n")}, t.TempDir(), 4)
	if len(got) != 0 {
		t.Fatalf("got %v", got)
	}
}

func TestIndexFileRoundTripAndService(t *testing.T) {
	path := t.TempDir() + "/idx/coupons.txt"
	if err := SaveIndexFile(path, []string{"HAPPYHRS", "FIFTYOFF"}); err != nil {
		t.Fatal(err)
	}
	idx, err := LoadIndexFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if !idx.IsValid("HAPPYHRS") || idx.IsValid("SUPER100") || idx.Size() != 2 {
		t.Fatal("bad round trip")
	}
	s := &Service{}
	if _, err := s.Check("HAPPYHRS"); err != ErrNotReady {
		t.Fatalf("want ErrNotReady, got %v", err)
	}
	if ok, err := s.Check("SHORT"); ok || err != nil { // length pre-filter needs no index
		t.Fatal("short code must be invalid without index")
	}
	var i Index = idx
	s.idx.Store(&i)
	if ok, _ := s.Check("FIFTYOFF"); !ok {
		t.Fatal("expected valid")
	}
}

func TestBuildAutoPublishesFile(t *testing.T) {
	dir := t.TempDir()
	a, b := dir+"/a.txt", dir+"/b.txt"
	if err := SaveIndexFile(a, []string{"HAPPYHRS", "ONLYINAAA"}); err != nil {
		t.Fatal(err)
	}
	if err := SaveIndexFile(b, []string{"HAPPYHRS", "ONLYINBBB"}); err != nil {
		t.Fatal(err)
	}
	cfg := Config{Store: "file", IndexPath: dir + "/out.txt", Sources: []string{a, b}, TmpDir: dir + "/tmp", Partitions: 8}
	if n, err := Build(cfg); err != nil || n != 1 {
		t.Fatalf("n=%d err=%v", n, err)
	}
	idx, err := Open(cfg)
	if err != nil {
		t.Fatal(err)
	}
	if ok, _ := idx.Contains("HAPPYHRS"); !ok {
		t.Fatal("expected HAPPYHRS valid")
	}
}
