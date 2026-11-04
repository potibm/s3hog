package auth

import (
	"bytes"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"sort"
	"strings"
	"time"
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
	parts := strings.Split(creds.Scope, "/")
	if len(parts) != 4 || parts[3] != "aws4_request" {
		return fmt.Errorf("invalid credential scope")
	}
	dateScope := parts[0]
	region := parts[1]
	service := parts[2]
	if service != "s3" {
		return fmt.Errorf("invalid service %s", service)
	}
	if dateScope != amzDate[:8] {
		return fmt.Errorf("date mismatch")
	}

	// payload hash
	body, err := readBody(r)
	if err != nil {
		return err
	}
	payloadHash := r.Header.Get("X-Amz-Content-Sha256")
	if payloadHash == "" {
		h := sha256.Sum256(body)
		payloadHash = hex.EncodeToString(h[:])
	}
	if payloadHash != "UNSIGNED-PAYLOAD" && !strings.HasPrefix(payloadHash, "STREAMING-") {
		h := sha256.Sum256(body)
		if payloadHash != hex.EncodeToString(h[:]) {
			return fmt.Errorf("payload hash mismatch")
		}
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

	if credential == "" || signedHeadersStr == "" || signature == "" || amzDate == "" {
		return fmt.Errorf("missing presigned params")
	}

	creds, err := parseCredential(credential)
	if err != nil {
		return err
	}
	if creds.AccessKey != accessKey {
		return fmt.Errorf("invalid access key")
	}
	parts := strings.Split(creds.Scope, "/")
	if len(parts) != 4 || parts[3] != "aws4_request" {
		return fmt.Errorf("invalid credential scope")
	}
	dateScope := parts[0]
	region := parts[1]
	service := parts[2]
	if service != "s3" {
		return fmt.Errorf("invalid service")
	}
	if dateScope != amzDate[:8] {
		return fmt.Errorf("date mismatch")
	}
	if err := checkSkew(amzDate); err != nil {
		// for presigned, also check expiry; skew check lenient
	}

	// expiry
	expires, _ := atoi(expiresStr)
	if expires > 0 {
		t, err := time.Parse("20060102T150405Z", amzDate)
		if err == nil {
			if time.Since(t) > time.Duration(expires)*time.Second {
				return fmt.Errorf("presigned URL expired")
			}
		}
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

func parseAuthHeader(auth string) (credentials, []string, string, error) {
	// AWS4-HMAC-SHA256 Credential=..., SignedHeaders=..., Signature=...
	auth = strings.TrimPrefix(auth, "AWS4-HMAC-SHA256")
	auth = strings.TrimSpace(auth)
	parts := strings.Split(auth, ",")
	var credStr, signedHeadersStr, signature string
	for _, p := range parts {
		p = strings.TrimSpace(p)
		kv := strings.SplitN(p, "=", 2)
		if len(kv) != 2 {
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

func readBody(r *http.Request) ([]byte, error) {
	if r.Body == nil {
		return nil, nil
	}
	b, err := io.ReadAll(r.Body)
	r.Body.Close()
	r.Body = io.NopCloser(bytes.NewReader(b))
	return b, err
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

func canonicalHeaders(r *http.Request, signedHeaders []string) (string, string) {
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
		n = n*10 + int(c-'0')
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
