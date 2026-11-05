package auth

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"sort"
	"strings"
	"time"
)

const (
	maxPresignedExpirySeconds = 604800
	splitParts                = 2
	decimalBase               = 10
)

// Verify checks a request against the configured credentials.
// It returns nil if the request is authenticated (or auth is disabled).
func Verify(r *http.Request, accessKey, secretKey string) error {
	if accessKey == "" || secretKey == "" {
		return nil
	}

	// Presigned URL?
	if r.URL.Query().Get("X-Amz-Algorithm") != "" {
		return verifyPresigned(r, accessKey, secretKey)
	}

	return verifyHeader(r, accessKey, secretKey)
}

func validateCredentialScope(creds credentials, amzDate string) (dateScope, region string, err error) {
	parts := strings.Split(creds.Scope, "/")
	if len(parts) != 4 || parts[3] != "aws4_request" {
		return "", "", fmt.Errorf("invalid credential scope")
	}

	dateScope = parts[0]
	region = parts[1]

	service := parts[2]
	if service != "s3" {
		return "", "", fmt.Errorf("invalid service %s", service)
	}

	if dateScope != amzDate[:8] {
		return "", "", fmt.Errorf("date mismatch")
	}

	return dateScope, region, nil
}

func resolvePayloadHash(r *http.Request, computedHash string) string {
	payloadHash := r.Header.Get("X-Amz-Content-Sha256")
	if payloadHash == "" {
		payloadHash = computedHash
	}

	return payloadHash
}

func checkPayloadHash(payloadHash, computedHash string) error {
	if payloadHash != "UNSIGNED-PAYLOAD" && !strings.HasPrefix(payloadHash, "STREAMING-") {
		if payloadHash != computedHash {
			return fmt.Errorf("payload hash mismatch")
		}
	}

	return nil
}

func verifyHeader(r *http.Request, accessKey, secretKey string) error {
	auth := r.Header.Get("Authorization")
	if auth == "" {
		return fmt.Errorf("missing Authorization header")
	}

	if !strings.HasPrefix(auth, "AWS4-HMAC-SHA256") {
		return fmt.Errorf("unsupported auth scheme")
	}

	// Parse authorization header
	creds, signedHeaders, signature, err := parseAuthHeader(auth)
	if err != nil {
		return err
	}

	if creds.AccessKey != accessKey {
		return fmt.Errorf("invalid access key")
	}

	amzDate := r.Header.Get("X-Amz-Date")
	if amzDate == "" {
		// fall back to Date header
		amzDate = r.Header.Get("Date")
	}

	if amzDate == "" {
		return fmt.Errorf("missing X-Amz-Date")
	}

	if err := checkSkew(amzDate); err != nil {
		return err
	}

	// region/service from credential scope
	dateScope, region, err := validateCredentialScope(creds, amzDate)
	if err != nil {
		return err
	}

	service := "s3"

	// payload hash
	// Stream the body to a temp file and compute its SHA256 while reading,
	// so we never hold the whole body in memory.
	computedHash, err := readBody(r)
	if err != nil {
		return err
	}

	payloadHash := resolvePayloadHash(r, computedHash)
	if err := checkPayloadHash(payloadHash, computedHash); err != nil {
		return err
	}

	canonicalReq := buildCanonicalRequest(r, signedHeaders, payloadHash)
	hashedCanonical := sha256Hex([]byte(canonicalReq))
	stringToSign := "AWS4-HMAC-SHA256\n" + amzDate + "\n" + creds.Scope + "\n" + hashedCanonical

	signingKey := makeSigningKey(secretKey, dateScope, region, service)

	computed := hex.EncodeToString(hmacSHA256(signingKey, []byte(stringToSign)))
	if !hmac.Equal([]byte(computed), []byte(signature)) {
		return fmt.Errorf("signature does not match")
	}

	return nil
}

func parseExpires(expiresStr string) (int, error) {
	expires, err := atoi(expiresStr)
	if err != nil || expires <= 0 {
		return 0, fmt.Errorf("invalid X-Amz-Expires")
	}

	if expires > maxPresignedExpirySeconds {
		return 0, fmt.Errorf("X-Amz-Expires exceeds maximum of 604800 seconds")
	}

	return expires, nil
}

func verifyPresigned(r *http.Request, accessKey, secretKey string) error {
	q := r.URL.Query()

	algorithm := q.Get("X-Amz-Algorithm")
	if algorithm != "AWS4-HMAC-SHA256" {
		return fmt.Errorf("unsupported algorithm")
	}

	credential := q.Get("X-Amz-Credential")
	signedHeadersStr := q.Get("X-Amz-SignedHeaders")
	signature := q.Get("X-Amz-Signature")
	amzDate := q.Get("X-Amz-Date")
	expiresStr := q.Get("X-Amz-Expires")

	if credential == "" || signedHeadersStr == "" || signature == "" || amzDate == "" || expiresStr == "" {
		return fmt.Errorf("missing presigned params")
	}

	creds, err := parseCredential(credential)
	if err != nil {
		return err
	}

	if creds.AccessKey != accessKey {
		return fmt.Errorf("invalid access key")
	}

	dateScope, region, err := validateCredentialScope(creds, amzDate)
	if err != nil {
		return err
	}

	service := "s3"

	// Presigned URLs: skew check is lenient; expiry is checked separately.
	expires, err := parseExpires(expiresStr)
	if err != nil {
		return err
	}

	t, err := time.Parse("20060102T150405Z", amzDate)
	if err != nil {
		return fmt.Errorf("invalid amz date")
	}

	if time.Since(t) > time.Duration(expires)*time.Second {
		return fmt.Errorf("presigned URL expired")
	}

	signedHeaders := strings.Split(signedHeadersStr, ";")

	// remove signature from query for canonical query string
	q2 := cloneValues(q)
	q2.Del("X-Amz-Signature")
	canonicalQS := canonicalQueryStringValues(q2)

	// payload hash for presigned: UNSIGNED-PAYLOAD
	payloadHash := "UNSIGNED-PAYLOAD"
	// build canonical request manually with the modified query
	canonicalReq := buildCanonicalRequestPresigned(r, signedHeaders, payloadHash, canonicalQS)

	hashedCanonical := sha256Hex([]byte(canonicalReq))
	stringToSign := "AWS4-HMAC-SHA256\n" + amzDate + "\n" + creds.Scope + "\n" + hashedCanonical

	signingKey := makeSigningKey(secretKey, dateScope, region, service)

	computed := hex.EncodeToString(hmacSHA256(signingKey, []byte(stringToSign)))
	if !hmac.Equal([]byte(computed), []byte(signature)) {
		return fmt.Errorf("signature does not match")
	}

	return nil
}

// ---- helpers ----

type credentials struct {
	AccessKey string
	Scope     string
}

func parseCredential(s string) (credentials, error) {
	// accessKey/scope
	idx := strings.Index(s, "/")
	if idx < 0 {
		return credentials{}, fmt.Errorf("invalid credential")
	}

	return credentials{AccessKey: s[:idx], Scope: s[idx+1:]}, nil
}

func parseAuthHeader(auth string) (c credentials, signed []string, sig string, err error) {
	// AWS4-HMAC-SHA256 Credential=..., SignedHeaders=..., Signature=...
	auth = strings.TrimPrefix(auth, "AWS4-HMAC-SHA256")
	auth = strings.TrimSpace(auth)
	parts := strings.Split(auth, ",")

	var credStr, signedHeadersStr, signature string

	for _, p := range parts {
		p = strings.TrimSpace(p)

		kv := strings.SplitN(p, "=", splitParts)
		if len(kv) != splitParts {
			continue
		}

		switch strings.TrimSpace(kv[0]) {
		case "Credential":
			credStr = strings.TrimSpace(kv[1])
		case "SignedHeaders":
			signedHeadersStr = strings.TrimSpace(kv[1])
		case "Signature":
			signature = strings.TrimSpace(kv[1])
		}
	}

	if credStr == "" || signedHeadersStr == "" || signature == "" {
		return credentials{}, nil, "", fmt.Errorf("malformed authorization header")
	}

	creds, err := parseCredential(credStr)
	if err != nil {
		return credentials{}, nil, "", err
	}

	return creds, strings.Split(signedHeadersStr, ";"), signature, nil
}

// readBody streams the request body into a temporary file, computing its SHA256
// hash while reading, so large uploads do not exhaust memory. It replaces r.Body
// with a reader over the temporary file. The caller is responsible for closing
// r.Body; the temp file is removed when closed.
func readBody(r *http.Request) (string, error) {
	if r.Body == nil {
		return "", nil
	}

	tmp, err := os.CreateTemp("", "s3hog-body-*")
	if err != nil {
		return "", err
	}

	hasher := sha256.New()

	w := io.MultiWriter(tmp, hasher)
	if _, err := io.Copy(w, r.Body); err != nil {
		r.Body.Close()
		tmp.Close()
		os.Remove(tmp.Name())

		return "", err
	}

	r.Body.Close()
	tmp.Close()

	f, err := os.Open(tmp.Name())
	if err != nil {
		os.Remove(tmp.Name())

		return "", err
	}
	// Wrap so the temp file is cleaned up when the body is closed.
	r.Body = &tempFileBody{File: f, path: tmp.Name()}

	return hex.EncodeToString(hasher.Sum(nil)), nil
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

func checkSkew(amzDate string) error {
	t, err := time.Parse("20060102T150405Z", amzDate)
	if err != nil {
		return fmt.Errorf("invalid amz date")
	}

	if d := time.Since(t); d < -15*time.Minute || d > 15*time.Minute {
		return fmt.Errorf("request time too skewed")
	}

	return nil
}

func buildCanonicalRequest(r *http.Request, signedHeaders []string, payloadHash string) string {
	// canonical URI
	canonicalURI := r.URL.EscapedPath()
	if canonicalURI == "" {
		canonicalURI = "/"
	}
	// canonical query string (from URL, sorted)
	canonicalQS := canonicalQueryString(r)
	// canonical headers
	canonicalHeaders, signedHeadersList := canonicalHeaders(r, signedHeaders)

	return r.Method + "\n" +
		canonicalURI + "\n" +
		canonicalQS + "\n" +
		canonicalHeaders + "\n" +
		signedHeadersList + "\n" +
		payloadHash
}

func buildCanonicalRequestPresigned(r *http.Request, signedHeaders []string, payloadHash, canonicalQS string) string {
	canonicalURI := r.URL.EscapedPath()
	if canonicalURI == "" {
		canonicalURI = "/"
	}

	canonicalHeaders, signedHeadersList := canonicalHeaders(r, signedHeaders)

	return r.Method + "\n" +
		canonicalURI + "\n" +
		canonicalQS + "\n" +
		canonicalHeaders + "\n" +
		signedHeadersList + "\n" +
		payloadHash
}

func canonicalQueryString(r *http.Request) string {
	return canonicalQueryStringValues(r.URL.Query())
}

func canonicalQueryStringValues(v url.Values) string {
	if len(v) == 0 {
		return ""
	}

	keys := make([]string, 0, len(v))
	for k := range v {
		keys = append(keys, k)
	}

	sort.Strings(keys)

	var pairs []string

	for _, k := range keys {
		vals := append([]string(nil), v[k]...)
		sort.Strings(vals)

		for _, val := range vals {
			pairs = append(pairs, awsEscape(k)+"="+awsEscape(val))
		}
	}

	return strings.Join(pairs, "&")
}

func canonicalHeaders(r *http.Request, signedHeaders []string) (canonical, signed string) {
	// build lowercased header map
	hdrMap := map[string]string{}
	for k, vals := range r.Header {
		hdrMap[strings.ToLower(k)] = strings.Join(vals, ",")
	}
	// host header from request if missing
	if _, ok := hdrMap["host"]; !ok {
		hdrMap["host"] = r.Host
	}
	// sort signed headers
	sh := append([]string(nil), signedHeaders...)
	sort.Strings(sh)

	var b strings.Builder

	for _, h := range sh {
		h = strings.ToLower(strings.TrimSpace(h))
		val := hdrMap[h]
		// collapse whitespace
		val = strings.TrimSpace(val)
		// collapse multiple spaces
		fields := strings.Fields(val)
		val = strings.Join(fields, " ")

		b.WriteString(h)
		b.WriteString(":")
		b.WriteString(val)
		b.WriteString("\n")
	}

	return b.String(), strings.Join(sh, ";")
}

func awsEscape(s string) string {
	// RFC 3986 unreserved
	var b strings.Builder

	for i := 0; i < len(s); i++ {
		c := s[i]
		if isUnreserved(c) {
			b.WriteByte(c)
		} else {
			fmt.Fprintf(&b, "%%%02X", c)
		}
	}

	return b.String()
}

func isUnreserved(c byte) bool {
	if (c >= 'A' && c <= 'Z') || (c >= 'a' && c <= 'z') || (c >= '0' && c <= '9') {
		return true
	}

	switch c {
	case '-', '_', '.', '~':
		return true
	}

	return false
}

func sha256Hex(b []byte) string {
	h := sha256.Sum256(b)

	return hex.EncodeToString(h[:])
}

func hmacSHA256(key, data []byte) []byte {
	h := hmac.New(sha256.New, key)
	h.Write(data)

	return h.Sum(nil)
}

func makeSigningKey(secret, date, region, service string) []byte {
	kDate := hmacSHA256([]byte("AWS4"+secret), []byte(date))
	kRegion := hmacSHA256(kDate, []byte(region))
	kService := hmacSHA256(kRegion, []byte(service))
	kSigning := hmacSHA256(kService, []byte("aws4_request"))

	return kSigning
}

func atoi(s string) (int, error) {
	n := 0

	for _, c := range s {
		if c < '0' || c > '9' {
			return 0, fmt.Errorf("not a number")
		}

		n = n*decimalBase + int(c-'0')
	}

	return n, nil
}

func cloneValues(v url.Values) url.Values {
	out := url.Values{}
	for k, vals := range v {
		out[k] = append([]string(nil), vals...)
	}

	return out
}
