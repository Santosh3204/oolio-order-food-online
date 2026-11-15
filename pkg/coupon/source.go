package coupon

import (
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"strings"
	"time"

	"github.com/klauspost/compress/gzip" // drop-in, noticeably faster than compress/gzip
)

// FileSource reads a local path or an http(s) URL; ".gz" is transparently decompressed.
func FileSource(location string) Source {
	return func() (io.ReadCloser, error) {
		rc, err := open(location)
		if err != nil {
			return nil, err
		}
		if !strings.HasSuffix(location, ".gz") {
			return rc, nil
		}
		gz, err := gzip.NewReader(rc)
		if err != nil {
			err := rc.Close()
			if err != nil {
				return nil, err
			}
			return nil, err
		}
		return &gzReadCloser{gz: gz, under: rc}, nil
	}
}

func open(location string) (io.ReadCloser, error) {
	if isURL(location) {
		body, err := get(location, 0)
		if err != nil {
			return nil, err
		}
		return &resumeReader{url: location, body: body}, nil
	}
	return os.Open(location)
}

func get(url string, offset int64) (io.ReadCloser, error) {
	req, err := http.NewRequest(http.MethodGet, url, nil)
	if err != nil {
		return nil, err
	}
	if offset > 0 {
		req.Header.Set("Range", fmt.Sprintf("bytes=%d-", offset))
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return nil, err
	}
	want := http.StatusOK
	if offset > 0 {
		want = http.StatusPartialContent
	}
	if resp.StatusCode != want {
		err := resp.Body.Close()
		if err != nil {
			return nil, err
		}
		return nil, fmt.Errorf("fetch %s (offset %d): status %d", url, offset, resp.StatusCode)
	}
	return resp.Body, nil
}

// resumeReader survives dropped connections during multi-GB downloads: on a
// read error it reconnects with a Range request from the last byte received.
type resumeReader struct {
	url    string
	body   io.ReadCloser
	offset int64
}

const maxResumes = 20

func (r *resumeReader) Read(p []byte) (int, error) {
	for attempt := 0; ; attempt++ {
		n, err := r.body.Read(p)
		r.offset += int64(n)
		if err == nil || err == io.EOF || attempt >= maxResumes {
			return n, err
		}
		log.Printf("download %s interrupted at byte %d (%v); resuming (%d/%d)", r.url, r.offset, err, attempt+1, maxResumes)
		err = r.body.Close()
		if err != nil {
			return 0, err
		}
		time.Sleep(time.Duration(min(attempt+1, 10)) * time.Second)
		body, gerr := get(r.url, r.offset)
		if gerr != nil {
			if attempt+1 >= maxResumes {
				return n, gerr
			}
			r.body = io.NopCloser(errReader{gerr}) // next loop iteration retries the reconnect
			continue
		}
		r.body = body
		if n > 0 {
			return n, nil
		}
	}
}

func (r *resumeReader) Close() error { return r.body.Close() }

type errReader struct{ err error }

func (e errReader) Read([]byte) (int, error) { return 0, e.err }

type gzReadCloser struct {
	gz    *gzip.Reader
	under io.Closer
}

func (g *gzReadCloser) Read(p []byte) (int, error) { return g.gz.Read(p) }
func (g *gzReadCloser) Close() error {
	err := g.gz.Close()
	if err != nil {
		return err
	}
	return g.under.Close()
}
