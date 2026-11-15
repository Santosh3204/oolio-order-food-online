package coupon

import (
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
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

	rc, err := open(srv.URL)
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
