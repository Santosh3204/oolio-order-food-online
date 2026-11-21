package coupon_test

import (
	"github.com/Santosh3204/oolio-order-food-online/pkg/coupon"
	"io"
	"os"
	"slices"
	"strings"
	"testing"
	"time"
)

func src(s string) coupon.Source {
	return func() (io.ReadCloser, error) { return io.NopCloser(strings.NewReader(s)), nil }
}

func TestBuildIndex(t *testing.T) {
	for _, parts := range []int{1, 7, 64} { // result must not depend on partitioning
		got, err := coupon.BuildIndex([]coupon.Source{
			src("HAPPYHRS\nFIFTYOFF\nSUPER100\nSHORT\nTOOLONGCODE1\nONLYONE12\nDUPLICATE\nDUPLICATE\n"),
			src("HAPPYHRS\nFIFTYOFF\nHAPPYHRS\n"),
			src("SUPER100\nSUPER100\nFIFTYOFF\n"),
		}, t.TempDir(), parts, 3)
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
	got, _ := coupon.BuildIndex([]coupon.Source{src("DUPLICATE\nDUPLICATE\n"), src("OTHERCODE\n")}, t.TempDir(), 4, 2)
	if len(got) != 0 {
		t.Fatalf("got %v", got)
	}
}

func TestIndexFileRoundTripAndService(t *testing.T) {
	path := t.TempDir() + "/idx/coupons.txt"
	if err := coupon.SaveIndexFile(path, []string{"HAPPYHRS", "FIFTYOFF"}); err != nil {
		t.Fatal(err)
	}
	idx, err := coupon.LoadIndexFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if !idx.IsValid("HAPPYHRS") || idx.IsValid("SUPER100") || idx.Size() != 2 {
		t.Fatal("bad round trip")
	}
	s := &coupon.Service{}
	if _, err := s.Check("HAPPYHRS"); err != coupon.ErrNotReady {
		t.Fatalf("want coupon.ErrNotReady, got %v", err)
	}
	if ok, err := s.Check("SHORT"); ok || err != nil { // length pre-filter needs no index
		t.Fatal("short code must be invalid without index")
	}
	s.SetIndex(idx)
	if ok, _ := s.Check("FIFTYOFF"); !ok {
		t.Fatal("expected valid")
	}
}

func TestBuildAutoPublishesFile(t *testing.T) {
	dir := t.TempDir()
	a, b := dir+"/a.txt", dir+"/b.txt"
	if err := coupon.SaveIndexFile(a, []string{"HAPPYHRS", "ONLYINAAA"}); err != nil {
		t.Fatal(err)
	}
	if err := coupon.SaveIndexFile(b, []string{"HAPPYHRS", "ONLYINBBB"}); err != nil {
		t.Fatal(err)
	}
	cfg := coupon.Config{Store: "file", IndexPath: dir + "/out.txt", Sources: []string{a, b}, TmpDir: dir + "/tmp", Partitions: 8, Workers: 2}
	if n, err := coupon.Build(cfg); err != nil || n != 1 {
		t.Fatalf("n=%d err=%v", n, err)
	}
	idx, err := coupon.Open(cfg)
	if err != nil {
		t.Fatal(err)
	}
	if ok, _ := idx.Contains("HAPPYHRS"); !ok {
		t.Fatal("expected HAPPYHRS valid")
	}
}

func TestServiceRefreshPicksUpNewIndexAndKeepsLastGoodOnFailure(t *testing.T) {
	path := t.TempDir() + "/idx.txt"
	if err := coupon.SaveIndexFile(path, []string{"HAPPYHRS"}); err != nil {
		t.Fatal(err)
	}
	s := coupon.NewService(coupon.Config{Store: "file", IndexPath: path, Refresh: 20 * time.Millisecond})
	waitFor(t, func() bool { ok, err := s.Check("HAPPYHRS"); return err == nil && ok })

	if err := coupon.SaveIndexFile(path, []string{"HAPPYHRS", "FIFTYOFF"}); err != nil {
		t.Fatal(err)
	}
	waitFor(t, func() bool { ok, _ := s.Check("FIFTYOFF"); return ok })

	os.Remove(path) // simulate the store going away: last good index must keep serving
	time.Sleep(100 * time.Millisecond)
	if ok, err := s.Check("FIFTYOFF"); !ok || err != nil {
		t.Fatalf("lost last-good index: ok=%v err=%v", ok, err)
	}
}

func waitFor(t *testing.T, cond func() bool) {
	t.Helper()
	for i := 0; i < 200; i++ {
		if cond() {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatal("condition not met in time")
}
