package storage

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"secure-brain/internal/domain"
)

const (
	maxProviderErrorBody = 8 << 10
)

type ObjectMetadata struct {
	MediaType    string
	Size         int64
	ETag         string
	LastModified time.Time
}

type ObjectStore interface {
	Put(ctx context.Context, path, mediaType string, body io.Reader, size int64, upsert bool) error
	Get(ctx context.Context, path string) (io.ReadCloser, ObjectMetadata, error)
	Delete(ctx context.Context, paths []string) error
}

type Client struct {
	baseURL        string
	bucket         string
	serviceRoleKey string
	httpClient     *http.Client
	maxObjectBytes int64
}

func NewClient(baseURL, bucket, serviceRoleKey string, httpClient *http.Client, maxObjectBytes int64) (*Client, error) {
	baseURL = strings.TrimRight(strings.TrimSpace(baseURL), "/")
	u, err := url.Parse(baseURL)
	if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" {
		return nil, fmt.Errorf("storage base URL must be an absolute http(s) URL")
	}
	if strings.TrimSpace(bucket) == "" {
		return nil, fmt.Errorf("storage bucket is required")
	}
	if strings.TrimSpace(serviceRoleKey) == "" {
		return nil, fmt.Errorf("storage service-role key is required")
	}
	if maxObjectBytes <= 0 {
		return nil, fmt.Errorf("storage object limit must be positive")
	}
	if httpClient == nil {
		httpClient = http.DefaultClient
	}
	return &Client{
		baseURL:        baseURL,
		bucket:         bucket,
		serviceRoleKey: serviceRoleKey,
		httpClient:     httpClient,
		maxObjectBytes: maxObjectBytes,
	}, nil
}

func (c *Client) Put(ctx context.Context, path, mediaType string, body io.Reader, size int64, upsert bool) error {
	if body == nil {
		return fmt.Errorf("storage Put body is required")
	}
	if size < 0 || size > c.maxObjectBytes {
		return &domain.Error{Code: domain.CodePayloadTooLarge, Message: "The object exceeds the Storage size limit."}
	}
	endpoint, err := c.objectEndpoint("/storage/v1/object", path)
	if err != nil {
		return err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, io.LimitReader(body, size))
	if err != nil {
		return providerError("create upload request", 0, err)
	}
	c.authorize(req)
	if mediaType == "" {
		mediaType = "application/octet-stream"
	}
	req.Header.Set("Content-Type", mediaType)
	req.Header.Set("x-upsert", strconv.FormatBool(upsert))
	req.ContentLength = size

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return providerError("upload object", 0, err)
	}
	defer resp.Body.Close()
	discardBounded(resp.Body)
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return providerError("upload object", resp.StatusCode, nil)
	}
	return nil
}

func (c *Client) Get(ctx context.Context, path string) (io.ReadCloser, ObjectMetadata, error) {
	endpoint, err := c.objectEndpoint("/storage/v1/object/authenticated", path)
	if err != nil {
		return nil, ObjectMetadata{}, err
	}
	for attempt := 0; attempt < 2; attempt++ {
		req, reqErr := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
		if reqErr != nil {
			return nil, ObjectMetadata{}, providerError("create download request", 0, reqErr)
		}
		c.authorize(req)
		resp, doErr := c.httpClient.Do(req)
		if doErr != nil {
			if attempt == 0 && waitForRetry(ctx) {
				continue
			}
			return nil, ObjectMetadata{}, providerError("download object", 0, doErr)
		}
		if resp.StatusCode < 200 || resp.StatusCode >= 300 {
			discardBounded(resp.Body)
			resp.Body.Close()
			if attempt == 0 && resp.StatusCode >= 500 && waitForRetry(ctx) {
				continue
			}
			return nil, ObjectMetadata{}, providerError("download object", resp.StatusCode, nil)
		}

		metadata := ObjectMetadata{
			MediaType: resp.Header.Get("Content-Type"),
			Size:      resp.ContentLength,
			ETag:      resp.Header.Get("ETag"),
		}
		if value := resp.Header.Get("Last-Modified"); value != "" {
			metadata.LastModified, _ = http.ParseTime(value)
		}
		if metadata.Size > c.maxObjectBytes {
			resp.Body.Close()
			return nil, ObjectMetadata{}, providerError("download object", 0, fmt.Errorf("object exceeds configured bound"))
		}
		return &boundedReadCloser{ReadCloser: resp.Body, remaining: c.maxObjectBytes}, metadata, nil
	}
	panic("unreachable")
}

func (c *Client) Delete(ctx context.Context, paths []string) error {
	if len(paths) == 0 {
		return nil
	}
	cleanPaths := make([]string, len(paths))
	for i, path := range paths {
		if _, err := escapedObjectPath(path); err != nil {
			return err
		}
		cleanPaths[i] = path
	}
	body, err := json.Marshal(struct {
		Prefixes []string `json:"prefixes"`
	}{Prefixes: cleanPaths})
	if err != nil {
		return providerError("encode delete request", 0, err)
	}
	endpoint := c.baseURL + "/storage/v1/object/" + url.PathEscape(c.bucket)
	for attempt := 0; attempt < 2; attempt++ {
		req, reqErr := http.NewRequestWithContext(ctx, http.MethodDelete, endpoint, bytes.NewReader(body))
		if reqErr != nil {
			return providerError("create delete request", 0, reqErr)
		}
		c.authorize(req)
		req.Header.Set("Content-Type", "application/json")
		resp, doErr := c.httpClient.Do(req)
		if doErr != nil {
			if attempt == 0 && waitForRetry(ctx) {
				continue
			}
			return providerError("delete objects", 0, doErr)
		}
		discardBounded(resp.Body)
		resp.Body.Close()
		if resp.StatusCode >= 200 && resp.StatusCode < 300 {
			return nil
		}
		if attempt == 0 && resp.StatusCode >= 500 && waitForRetry(ctx) {
			continue
		}
		return providerError("delete objects", resp.StatusCode, nil)
	}
	panic("unreachable")
}

func (c *Client) authorize(req *http.Request) {
	req.Header.Set("apikey", c.serviceRoleKey)
	req.Header.Set("Authorization", "Bearer "+c.serviceRoleKey)
}

func (c *Client) objectEndpoint(prefix, path string) (string, error) {
	escaped, err := escapedObjectPath(path)
	if err != nil {
		return "", err
	}
	return c.baseURL + prefix + "/" + url.PathEscape(c.bucket) + "/" + escaped, nil
}

func escapedObjectPath(path string) (string, error) {
	if path == "" || strings.HasPrefix(path, "/") || strings.ContainsRune(path, '\x00') {
		return "", fmt.Errorf("storage object path is invalid")
	}
	segments := strings.Split(path, "/")
	for i, segment := range segments {
		if segment == "" || segment == "." || segment == ".." {
			return "", fmt.Errorf("storage object path is invalid")
		}
		segments[i] = url.PathEscape(segment)
	}
	return strings.Join(segments, "/"), nil
}

func providerError(operation string, status int, cause error) *domain.Error {
	if cause == nil && status != 0 {
		cause = fmt.Errorf("%s returned HTTP status %d", operation, status)
	} else if cause != nil {
		cause = fmt.Errorf("%s: %w", operation, cause)
	}
	return &domain.Error{
		Code:    domain.CodeStorageProviderError,
		Message: "Storage is temporarily unavailable.",
		Cause:   cause,
	}
}

func discardBounded(body io.Reader) {
	_, _ = io.Copy(io.Discard, io.LimitReader(body, maxProviderErrorBody))
}

func waitForRetry(ctx context.Context) bool {
	timer := time.NewTimer(10 * time.Millisecond)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return false
	case <-timer.C:
		return true
	}
}

type boundedReadCloser struct {
	io.ReadCloser
	remaining int64
}

func (r *boundedReadCloser) Read(p []byte) (int, error) {
	if r.remaining == 0 {
		var one [1]byte
		n, err := r.ReadCloser.Read(one[:])
		if n != 0 {
			return 0, providerError("download object", 0, fmt.Errorf("object exceeds configured bound"))
		}
		return 0, err
	}
	if int64(len(p)) > r.remaining {
		p = p[:r.remaining]
	}
	n, err := r.ReadCloser.Read(p)
	r.remaining -= int64(n)
	return n, err
}
