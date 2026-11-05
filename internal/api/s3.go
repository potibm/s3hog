package api

import (
	"bufio"
	"io"
	"log"
	"net/http"
	"os"
	"strconv"
	"strings"

	"github.com/johannesboyne/gofakes3"
	"github.com/potibm/s3hog/internal/auth"
	"github.com/potibm/s3hog/internal/config"
	"github.com/potibm/s3hog/internal/storage"
)

// NewS3Handler builds the S3-compatible HTTP handler.
func NewS3Handler(cfg config.Config, backend *storage.Backend) http.Handler {
	faker := gofakes3.New(backend, gofakes3.WithAutoBucket(cfg.AutoBucket == true))
	handler := faker.Server()
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("X-Content-Type-Options", "nosniff")
		w.Header().Set("X-Frame-Options", "DENY")
		if err := auth.Verify(r, cfg.AccessKeyID, cfg.SecretKey); err != nil {
			log.Printf("auth failed from %s: %v", r.RemoteAddr, err)
			writeS3Error(w, "AccessDenied")
			return
		}
		decodeAwsChunkedBody(r)
		handler.ServeHTTP(w, r)
	})
}

func decodeAwsChunkedBody(r *http.Request) {
	ce := r.Header.Get("Content-Encoding")
	if !strings.Contains(ce, "aws-chunked") {
		return
	}
	// Stream the (possibly large) request body into a temp file first so we
	// never hold the whole upload in memory, then stream-decode from it.
	tmpIn, err := os.CreateTemp("", "s3hog-chunked-in-*")
	if err != nil {
		return
	}
	if _, err := io.Copy(tmpIn, r.Body); err != nil {
		r.Body.Close()
		tmpIn.Close()
		os.Remove(tmpIn.Name())
		return
	}
	r.Body.Close()
	tmpIn.Close()

	fIn, err := os.Open(tmpIn.Name())
	if err != nil {
		os.Remove(tmpIn.Name())
		return
	}

	tmpOut, err := os.CreateTemp("", "s3hog-chunked-out-*")
	if err != nil {
		fIn.Close()
		os.Remove(tmpIn.Name())
		return
	}

	if err := decodeAwsChunkedStream(fIn, tmpOut); err != nil {
		fIn.Close()
		tmpOut.Close()
		os.Remove(tmpIn.Name())
		os.Remove(tmpOut.Name())
		// fall back to original (re-open input)
		fb, _ := os.Open(tmpIn.Name())
		r.Body = &tempFileBody{File: fb, path: tmpIn.Name()}
		return
	}
	fIn.Close()
	tmpOut.Close()
	os.Remove(tmpIn.Name())

	fOut, err := os.Open(tmpOut.Name())
	if err != nil {
		os.Remove(tmpOut.Name())
		return
	}
	r.Body = &tempFileBody{File: fOut, path: tmpOut.Name()}
	r.Header.Del("Content-Encoding")
	// Content-Length unknown without stat; stat the file.
	if st, err := fOut.Stat(); err == nil {
		r.Header.Set("Content-Length", strconv.FormatInt(st.Size(), 10))
		r.ContentLength = st.Size()
	}
}

// decodeAwsChunkedStream decodes an aws-chunked stream from in into out.
func decodeAwsChunkedStream(in io.Reader, out io.Writer) error {
	br := bufio.NewReader(in)
	for {
		line, err := br.ReadString('\n')
		if err != nil && line == "" {
			return err
		}
		line = strings.TrimRight(line, "\r\n")
		sizeHex := line
		if idx := strings.Index(sizeHex, ";"); idx >= 0 {
			sizeHex = sizeHex[:idx]
		}
		sizeHex = strings.TrimSpace(sizeHex)
		size, err := strconv.ParseInt(sizeHex, 16, 64)
		if err != nil {
			return err
		}
		if size == 0 {
			// consume trailers until blank line
			for {
				tl, err := br.ReadString('\n')
				if err != nil {
					return nil
				}
				if strings.TrimSpace(tl) == "" {
					break
				}
			}
			return nil
		}
		if _, err := io.CopyN(out, br, size); err != nil {
			return err
		}
		// consume trailing CRLF
		if _, err := br.Discard(2); err != nil {
			// EOF is acceptable if no trailing CRLF
		}
	}
}

func writeS3Error(w http.ResponseWriter, msg string) {
	w.Header().Set("Content-Type", "application/xml")
	w.WriteHeader(http.StatusForbidden)
	w.Write([]byte(`<?xml version="1.0" encoding="UTF-8"?><Error><Code>AccessDenied</Code><Message>` + xmlEscape(msg) + `</Message></Error>`))
}

// tempFileBody wraps an *os.File and removes the backing temp file on Close.
type tempFileBody struct {
	*os.File
	path string
}

func (t *tempFileBody) Close() error {
	err := t.File.Close()
	os.Remove(t.path)
	return err
}

func xmlEscape(s string) string {
	s = strings.ReplaceAll(s, "&", "&amp;")
	s = strings.ReplaceAll(s, "<", "&lt;")
	s = strings.ReplaceAll(s, ">", "&gt;")
	return s
}
