package coupon_test

import (
	"bytes"
	"fmt"
	"github.com/Santosh3204/oolio-order-food-online/pkg/coupon"
	"github.com/klauspost/compress/gzip"
	"github.com/klauspost/compress/zstd"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"
)

// Server that supports Range and kills the first full response mid-body.
func TestResumeAfterDroppedConnection(t *testing.T) {
	data := strings.Repeat("ABCDEFGH\n", 5000)
	var drops atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		start := 0
		if rg := r.Header.Get("Range"); rg != "" {
			start, _ = strconv.Atoi(strings.TrimSuffix(strings.TrimPrefix(rg, "bytes="), "-"))
			w.Header().Set("Content-Range", fmt.Sprintf("bytes %d-%d/%d", start, len(data)-1, len(data)))
			w.WriteHeader(http.StatusPartialContent)
		}
		w.Header().Set("Content-Length", strconv.Itoa(len(data)-start))
		if start == 0 && drops.Add(1) == 1 {
			_, err := w.Write([]byte(data[:1000]))
			if err != nil {
				return
			}
			w.(http.Flusher).Flush()
			conn, _, _ := w.(http.Hijacker).Hijack()
			err = conn.Close()
			if err != nil {
				return
			} // abrupt drop mid-body
			return
		}
		_, err := w.Write([]byte(data[start:]))
		if err != nil {
			return
		}
	}))
	defer srv.Close()

	rc, err := coupon.FileSource(srv.URL)()
	if err != nil {
		t.Fatal(err)
	}
	defer func(rc io.ReadCloser) {
		err := rc.Close()
		if err != nil {

		}
	}(rc)
	got, err := io.ReadAll(rc)
	if err != nil || string(got) != data {
		t.Fatalf("len=%d want=%d err=%v", len(got), len(data), err)
	}
}

func readAll(t *testing.T, loc string) string {
	t.Helper()
	rc, err := coupon.FileSource(loc)()
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = rc.Close() }()
	b, err := io.ReadAll(rc)
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

// Compression is chosen by extension through the codec registry.
func TestCodecs(t *testing.T) {
	const data = "ABCDEFGH\nIJKLMNOP\n"
	dir := t.TempDir()

	var gz bytes.Buffer
	zw := gzip.NewWriter(&gz)
	_, _ = zw.Write([]byte(data))
	_ = zw.Close()

	var zs bytes.Buffer
	enc, _ := zstd.NewWriter(&zs)
	_, _ = enc.Write([]byte(data))
	_ = enc.Close()

	files := map[string][]byte{"a.txt": []byte(data), "a.gz": gz.Bytes(), "a.zst": zs.Bytes(), "a.GZ": gz.Bytes()}
	for name, content := range files {
		p := filepath.Join(dir, name)
		if err := os.WriteFile(p, content, 0o644); err != nil {
			t.Fatal(err)
		}
		if got := readAll(t, p); got != data {
			t.Errorf("%s: got %q", name, got)
		}
	}
}

// A new format plugs in without touching the indexer.
func TestRegisterCodec(t *testing.T) {
	coupon.RegisterCodec(".upper", func(r io.Reader) (io.ReadCloser, error) {
		b, err := io.ReadAll(r)
		return io.NopCloser(bytes.NewReader(bytes.ToUpper(b))), err
	})
	p := filepath.Join(t.TempDir(), "x.upper")
	if err := os.WriteFile(p, []byte("abcdefgh\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if got := readAll(t, p); got != "ABCDEFGH\n" {
		t.Fatalf("got %q", got)
	}
}

// Pre-signed URLs carry a query string after the extension.
func TestCodecIgnoresURLQuery(t *testing.T) {
	var gz bytes.Buffer
	zw := gzip.NewWriter(&gz)
	_, _ = zw.Write([]byte("ABCDEFGH\n"))
	_ = zw.Close()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { _, _ = w.Write(gz.Bytes()) }))
	defer srv.Close()
	if got := readAll(t, srv.URL+"/couponbase1.gz?X-Amz-Signature=abc"); got != "ABCDEFGH\n" {
		t.Fatalf("got %q", got)
	}
}
