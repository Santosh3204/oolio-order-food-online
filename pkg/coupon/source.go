package coupon

import (
	"fmt"
	"io"
	"log"
	"net"
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
	resp, err := httpClient.Do(req)
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

// maxResumes with exponential backoff (1,2,4,...,30s cap) tolerates a network/DNS
// outage of roughly 15 minutes before giving up. The counter resets whenever bytes flow again.
const maxResumes = 40

// httpClient bounds connection setup so a dead network fails fast (and is retried)
// instead of hanging; there is deliberately no overall timeout (downloads are GBs).
var httpClient = &http.Client{Transport: &http.Transport{
	Proxy:                 http.ProxyFromEnvironment,
	DialContext:           (&net.Dialer{Timeout: 15 * time.Second, KeepAlive: 30 * time.Second}).DialContext,
	TLSHandshakeTimeout:   15 * time.Second,
	ResponseHeaderTimeout: 30 * time.Second,
}}

func backoff(attempt int) time.Duration {
	return min(time.Second<<min(attempt, 5), 30*time.Second)
}

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
		time.Sleep(backoff(attempt))
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
