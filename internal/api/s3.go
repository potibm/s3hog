package api

import (
	"bytes"
	"io"
	"net/http"
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
		if err := auth.Verify(r, cfg.AccessKeyID, cfg.SecretKey); err != nil {
			writeS3Error(w, err.Error())
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
	b, err := io.ReadAll(r.Body)
	r.Body.Close()
	if err != nil {
		r.Body = io.NopCloser(bytes.NewReader(b))
		return
	}
	decoded, err := decodeAwsChunked(b)
	if err != nil {
		// fall back to original
		r.Body = io.NopCloser(bytes.NewReader(b))
		return
	}
	r.Body = io.NopCloser(bytes.NewReader(decoded))
	r.Header.Del("Content-Encoding")
	r.Header.Set("Content-Length", strconv.Itoa(len(decoded)))
	r.ContentLength = int64(len(decoded))
}

func decodeAwsChunked(b []byte) ([]byte, error) {
	var out bytes.Buffer
	i := 0
	for i < len(b) {
		// find CRLF
		crlf := bytes.Index(b[i:], []byte("\r\n"))
		if crlf < 0 {
			return nil, io.ErrUnexpectedEOF
		}
		line := string(b[i : i+crlf])
		i += crlf + 2
		sizeHex := line
		if idx := strings.Index(sizeHex, ";"); idx >= 0 {
			sizeHex = sizeHex[:idx]
		}
		sizeHex = strings.TrimSpace(sizeHex)
		size, err := strconv.ParseInt(sizeHex, 16, 64)
		if err != nil {
			return nil, err
		}
		if size == 0 {
			// consume trailers until blank line
			break
		}
		if int64(len(b)-i) < size {
			return nil, io.ErrUnexpectedEOF
		}
		out.Write(b[i : i+int(size)])
		i += int(size)
		if i+2 <= len(b) && b[i] == '\r' && b[i+1] == '\n' {
			i += 2
		}
	}
	return out.Bytes(), nil
}

func writeS3Error(w http.ResponseWriter, msg string) {
	w.Header().Set("Content-Type", "application/xml")
	w.WriteHeader(http.StatusForbidden)
	w.Write([]byte(`<?xml version="1.0" encoding="UTF-8"?><Error><Code>AccessDenied</Code><Message>` + xmlEscape(msg) + `</Message></Error>`))
}

func xmlEscape(s string) string {
	s = strings.ReplaceAll(s, "&", "&amp;")
	s = strings.ReplaceAll(s, "<", "&lt;")
	s = strings.ReplaceAll(s, ">", "&gt;")
	return s
}
