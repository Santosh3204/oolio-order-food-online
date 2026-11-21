package coupon

import (
	"compress/bzip2"
	"fmt"
	"io"
	"log"
	"net"
	"net/http"
	"net/url"
	"os"
	"path"
	"strings"
	"sync"
	"time"

	"github.com/klauspost/compress/gzip" // drop-in, noticeably faster than compress/gzip
	"github.com/klauspost/compress/zstd"
)

// FileSource reads a local path or an http(s) URL. The compression format is
// chosen from the file extension via the codec registry (see RegisterCodec);
// unknown extensions are read as plain text, one code per line.
func FileSource(location string) Source {
	return func() (io.ReadCloser, error) {
		rc, err := open(location)
		if err != nil {
			return nil, err
		}
		codec := codecFor(location)
		if codec == nil {
			return rc, nil
		}
		dec, err := codec(rc)
		if err != nil {
			_ = rc.Close()
			return nil, fmt.Errorf("decode %s: %w", location, err)
		}
		return &decodedReadCloser{dec: dec, under: rc}, nil
	}
}

// Codec wraps a raw byte stream in a decompressor.
type Codec func(io.Reader) (io.ReadCloser, error)

var (
	codecMu sync.RWMutex
	codecs  = map[string]Codec{
		".gz": func(r io.Reader) (io.ReadCloser, error) { return gzip.NewReader(r) },
		".zst": func(r io.Reader) (io.ReadCloser, error) {
			d, err := zstd.NewReader(r)
			if err != nil {
				return nil, err
			}
			return d.IOReadCloser(), nil
		},
		".bz2": func(r io.Reader) (io.ReadCloser, error) { return io.NopCloser(bzip2.NewReader(r)), nil },
	}
)

// RegisterCodec plugs in support for another compression format (extension
// including the dot, e.g. ".xz") without touching the indexing code.
func RegisterCodec(ext string, c Codec) {
	codecMu.Lock()
	defer codecMu.Unlock()
	codecs[strings.ToLower(ext)] = c
}

// codecFor picks a codec by the extension of the path (query strings of
// pre-signed URLs are ignored). nil means plain text.
func codecFor(location string) Codec {
	p := location
	if isURL(location) {
		if u, err := url.Parse(location); err == nil {
			p = u.Path
		}
	}
	codecMu.RLock()
	defer codecMu.RUnlock()
	return codecs[strings.ToLower(path.Ext(p))]
}

// decodedReadCloser closes both the decoder and the underlying stream.
type decodedReadCloser struct {
	dec   io.ReadCloser
	under io.Closer
}

func (d *decodedReadCloser) Read(p []byte) (int, error) { return d.dec.Read(p) }
func (d *decodedReadCloser) Close() error {
	derr := d.dec.Close()
	if uerr := d.under.Close(); uerr != nil {
		return uerr
	}
	return derr
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

// Fingerprint identifies the current content of a source (best effort) so a
// resumed build never reuses spill files made from older data. Local files:
// size + mtime. URLs: HEAD length/ETag/Last-Modified. Unknown -> "".
func Fingerprint(loc string) string {
	if isURL(loc) {
		req, err := http.NewRequest(http.MethodHead, loc, nil)
		if err != nil {
			return ""
		}
		resp, err := httpClient.Do(req)
		if err != nil {
			return ""
		}
		_ = resp.Body.Close()
		return fmt.Sprintf("%d|%s|%s|%d", resp.ContentLength, resp.Header.Get("ETag"), resp.Header.Get("Last-Modified"), resp.StatusCode)
	}
	st, err := os.Stat(loc)
	if err != nil {
		return ""
	}
	return fmt.Sprintf("%d|%d", st.Size(), st.ModTime().UnixNano())
}
