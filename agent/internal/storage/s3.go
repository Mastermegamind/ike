// Package storage keeps remote storage targets (S3, Backblaze B2, Wasabi,
// Cloudflare R2, DigitalOcean Spaces, MinIO, SFTP) and talks to S3-compatible
// ones with a small AWS Signature Version 4 client from the standard library.
package storage

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/xml"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"sort"
	"strconv"
	"strings"
	"time"
)

// S3 is an S3-compatible bucket.
type S3 struct {
	Endpoint  string // https://s3.eu-central-003.backblazeb2.com
	Region    string
	Bucket    string
	AccessKey string
	SecretKey string
	PathStyle bool // https://host/bucket/key instead of https://bucket.host/key
	HTTP      *http.Client
	now       func() time.Time
}

func (s *S3) client() *http.Client {
	if s.HTTP != nil {
		return s.HTTP
	}
	return &http.Client{Timeout: 0}
}

func (s *S3) clock() time.Time {
	if s.now != nil {
		return s.now()
	}
	return time.Now().UTC()
}

// objectURL builds the URL for a key ("" for the bucket).
func (s *S3) objectURL(key string, q url.Values) (*url.URL, error) {
	u, err := url.Parse(strings.TrimRight(s.Endpoint, "/"))
	if err != nil || u.Host == "" || (u.Scheme != "https" && u.Scheme != "http") {
		return nil, fmt.Errorf("endpoint must be a URL like https://s3.us-east-1.amazonaws.com")
	}
	esc := escapePath(key)
	if s.PathStyle {
		u.Path = "/" + s.Bucket
		if key != "" {
			u.Path += "/" + key
		}
		u.RawPath = "/" + s.Bucket
		if key != "" {
			u.RawPath += "/" + esc
		}
	} else {
		u.Host = s.Bucket + "." + u.Host
		u.Path = "/" + key
		u.RawPath = "/" + esc
	}
	if q != nil {
		u.RawQuery = canonicalQuery(q)
	}
	return u, nil
}

// escapePath URI-encodes each path segment the way SigV4 expects.
func escapePath(p string) string {
	segs := strings.Split(p, "/")
	for i, s := range segs {
		segs[i] = uriEncode(s, false)
	}
	return strings.Join(segs, "/")
}

func uriEncode(s string, slash bool) string {
	var b strings.Builder
	for i := 0; i < len(s); i++ {
		c := s[i]
		if (c >= 'A' && c <= 'Z') || (c >= 'a' && c <= 'z') || (c >= '0' && c <= '9') || c == '-' || c == '_' || c == '.' || c == '~' || (c == '/' && !slash) {
			b.WriteByte(c)
		} else {
			fmt.Fprintf(&b, "%%%02X", c)
		}
	}
	return b.String()
}

func canonicalQuery(q url.Values) string {
	keys := make([]string, 0, len(q))
	for k := range q {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	var parts []string
	for _, k := range keys {
		vs := append([]string(nil), q[k]...)
		sort.Strings(vs)
		for _, v := range vs {
			parts = append(parts, uriEncode(k, true)+"="+uriEncode(v, true))
		}
	}
	return strings.Join(parts, "&")
}

func hmacSHA256(key []byte, data string) []byte {
	h := hmac.New(sha256.New, key)
	h.Write([]byte(data))
	return h.Sum(nil)
}

// sign adds SigV4 headers. payloadHash is the hex SHA-256 of the body or
// "UNSIGNED-PAYLOAD" for streamed uploads over HTTPS.
func (s *S3) sign(req *http.Request, payloadHash string) {
	t := s.clock()
	amzDate := t.Format("20060102T150405Z")
	date := t.Format("20060102")
	region := s.Region
	if region == "" {
		region = "us-east-1"
	}
	req.Header.Set("x-amz-date", amzDate)
	req.Header.Set("x-amz-content-sha256", payloadHash)
	host := req.URL.Host
	req.Host = host
	// canonical headers: host and every x-amz-* header, sorted
	names := []string{"host"}
	vals := map[string]string{"host": host}
	for k, v := range req.Header {
		lk := strings.ToLower(k)
		if strings.HasPrefix(lk, "x-amz-") || lk == "content-type" || lk == "content-md5" {
			names = append(names, lk)
			vals[lk] = strings.TrimSpace(strings.Join(v, ","))
		}
	}
	sort.Strings(names)
	var ch strings.Builder
	for _, n := range names {
		ch.WriteString(n + ":" + vals[n] + "\n")
	}
	signed := strings.Join(names, ";")
	path := req.URL.EscapedPath()
	if path == "" {
		path = "/"
	}
	canonical := strings.Join([]string{req.Method, path, req.URL.RawQuery, ch.String(), signed, payloadHash}, "\n")
	scope := date + "/" + region + "/s3/aws4_request"
	sum := sha256.Sum256([]byte(canonical))
	toSign := "AWS4-HMAC-SHA256\n" + amzDate + "\n" + scope + "\n" + hex.EncodeToString(sum[:])
	k := hmacSHA256([]byte("AWS4"+s.SecretKey), date)
	k = hmacSHA256(k, region)
	k = hmacSHA256(k, "s3")
	k = hmacSHA256(k, "aws4_request")
	sig := hex.EncodeToString(hmacSHA256(k, toSign))
	req.Header.Set("Authorization", fmt.Sprintf("AWS4-HMAC-SHA256 Credential=%s/%s, SignedHeaders=%s, Signature=%s", s.AccessKey, scope, signed, sig))
}

const emptySHA = "e3b0c44298fc1c149afbf4c8996fb92427ae41e4649b934ca495991b7852b855"

type s3Error struct {
	Code    string `xml:"Code"`
	Message string `xml:"Message"`
}

func (s *S3) do(ctx context.Context, method, key string, q url.Values, body io.Reader, size int64, payloadHash string) (*http.Response, error) {
	u, err := s.objectURL(key, q)
	if err != nil {
		return nil, err
	}
	req, err := http.NewRequestWithContext(ctx, method, u.String(), body)
	if err != nil {
		return nil, err
	}
	req.URL = u
	if size >= 0 && body != nil {
		req.ContentLength = size
	}
	if payloadHash == "" {
		payloadHash = emptySHA
	}
	s.sign(req, payloadHash)
	resp, err := s.client().Do(req)
	if err != nil {
		return nil, err
	}
	if resp.StatusCode >= 300 {
		defer resp.Body.Close()
		b, _ := io.ReadAll(io.LimitReader(resp.Body, 64<<10))
		var e s3Error
		_ = xml.Unmarshal(b, &e)
		if e.Code == "" {
			e.Code = resp.Status
		}
		return nil, fmt.Errorf("%s: %s %s", s.Bucket, e.Code, e.Message)
	}
	return resp, nil
}

// Check signs a request to list one key, proving the credentials and bucket work.
func (s *S3) Check(ctx context.Context) error {
	resp, err := s.do(ctx, "GET", "", url.Values{"list-type": {"2"}, "max-keys": {"1"}}, nil, -1, "")
	if err != nil {
		return err
	}
	resp.Body.Close()
	return nil
}

// Put uploads a stream. size must be known (S3 needs Content-Length).
func (s *S3) Put(ctx context.Context, key string, r io.Reader, size int64) error {
	resp, err := s.do(ctx, "PUT", key, nil, r, size, "UNSIGNED-PAYLOAD")
	if err != nil {
		return err
	}
	resp.Body.Close()
	return nil
}

// Get downloads an object.
func (s *S3) Get(ctx context.Context, key string) (io.ReadCloser, int64, error) {
	resp, err := s.do(ctx, "GET", key, nil, nil, -1, "")
	if err != nil {
		return nil, 0, err
	}
	return resp.Body, resp.ContentLength, nil
}

func (s *S3) Delete(ctx context.Context, key string) error {
	resp, err := s.do(ctx, "DELETE", key, nil, nil, -1, "")
	if err != nil {
		return err
	}
	resp.Body.Close()
	return nil
}

// Object is one listed object.
type Object struct {
	Key      string    `json:"key"`
	Size     int64     `json:"size"`
	Modified time.Time `json:"modified"`
	Dir      bool      `json:"dir,omitempty"`
}

// List returns objects and "folders" under a prefix.
func (s *S3) List(ctx context.Context, prefix string, max int) ([]Object, error) {
	if max <= 0 || max > 1000 {
		max = 1000
	}
	q := url.Values{"list-type": {"2"}, "prefix": {prefix}, "delimiter": {"/"}, "max-keys": {strconv.Itoa(max)}}
	resp, err := s.do(ctx, "GET", "", q, nil, -1, "")
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	var res struct {
		Contents []struct {
			Key          string `xml:"Key"`
			Size         int64  `xml:"Size"`
			LastModified string `xml:"LastModified"`
		} `xml:"Contents"`
		Prefixes []struct {
			Prefix string `xml:"Prefix"`
		} `xml:"CommonPrefixes"`
	}
	if err := xml.NewDecoder(io.LimitReader(resp.Body, 32<<20)).Decode(&res); err != nil {
		return nil, err
	}
	out := []Object{}
	for _, p := range res.Prefixes {
		out = append(out, Object{Key: p.Prefix, Dir: true})
	}
	for _, c := range res.Contents {
		t, _ := time.Parse(time.RFC3339, c.LastModified)
		out = append(out, Object{Key: c.Key, Size: c.Size, Modified: t})
	}
	return out, nil
}
