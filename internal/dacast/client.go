package dacast

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"time"
)

const BaseURL = "https://developer.dacast.com"

type Client struct {
	APIKey     string
	HTTPClient *http.Client
	BaseURL    string
}

func New(apiKey string) *Client {
	return &Client{
		APIKey: apiKey,
		HTTPClient: &http.Client{
			Timeout: 2 * time.Minute,
		},
		BaseURL: BaseURL,
	}
}

type initRequest struct {
	Filename string `json:"filename"`
}

type InitResponse struct {
	S3Path     string `json:"s3_path"`
	UploaderID string `json:"uploader_id"`
}

type signaturesRequest struct {
	FromPartNumber int    `json:"from_part_number"`
	ToPartNumber   int    `json:"to_part_number"`
	S3Path         string `json:"s3_path"`
	UploaderID     string `json:"uploader_id"`
}

type SignaturesResponse struct {
	PresignedURLs []string `json:"presigned_urls"`
}

type completeRequest struct {
	S3Path       string   `json:"s3_path"`
	UploaderID   string   `json:"uploader_id"`
	OrderedETags []string `json:"ordered_etags"`
}

type CompleteResponse struct {
	VodID string `json:"vod_id"`
}

func (c *Client) InitMultipart(ctx context.Context, filename string) (*InitResponse, error) {
	var out InitResponse
	if err := c.postJSON(ctx, "/v2/vod/upload/init-multipart", initRequest{Filename: filename}, &out); err != nil {
		return nil, err
	}
	if out.S3Path == "" || out.UploaderID == "" {
		return nil, fmt.Errorf("init-multipart: empty s3_path or uploader_id")
	}
	return &out, nil
}

func (c *Client) PresignedURLs(ctx context.Context, s3Path, uploaderID string, fromPart, toPart int) ([]string, error) {
	var out SignaturesResponse
	err := c.postJSON(ctx, "/v2/vod/upload/signatures/multipart", signaturesRequest{
		FromPartNumber: fromPart,
		ToPartNumber:   toPart,
		S3Path:         s3Path,
		UploaderID:     uploaderID,
	}, &out)
	if err != nil {
		return nil, err
	}
	if len(out.PresignedURLs) == 0 {
		return nil, fmt.Errorf("signatures/multipart: no URLs returned")
	}
	return out.PresignedURLs, nil
}

func (c *Client) CompleteMultipart(ctx context.Context, s3Path, uploaderID string, etags []string) (*CompleteResponse, error) {
	var out CompleteResponse
	if err := c.postJSON(ctx, "/v2/vod/upload/complete-multipart", completeRequest{
		S3Path:       s3Path,
		UploaderID:   uploaderID,
		OrderedETags: etags,
	}, &out); err != nil {
		return nil, err
	}
	if out.VodID == "" {
		return nil, fmt.Errorf("complete-multipart: empty vod_id")
	}
	return &out, nil
}

// PutPart uploads a chunk to a presigned S3 URL and returns the ETag.
func (c *Client) PutPart(ctx context.Context, url string, body io.Reader, size int64) (string, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodPut, url, body)
	if err != nil {
		return "", err
	}
	if size >= 0 {
		req.ContentLength = size
	}
	// Longer timeout for large parts
	client := c.HTTPClient
	if client.Timeout < 10*time.Minute {
		client = &http.Client{Timeout: 30 * time.Minute}
	}
	resp, err := client.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	respBody, _ := io.ReadAll(io.LimitReader(resp.Body, 64<<10))
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return "", fmt.Errorf("s3 PUT %s: %s", resp.Status, truncate(string(respBody), 200))
	}
	etag := resp.Header.Get("ETag")
	if etag == "" {
		return "", fmt.Errorf("s3 PUT: missing ETag header")
	}
	return etag, nil
}

func (c *Client) postJSON(ctx context.Context, path string, in any, out any) error {
	payload, err := json.Marshal(in)
	if err != nil {
		return err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.BaseURL+path, bytes.NewReader(payload))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-Api-Key", c.APIKey)
	req.Header.Set("X-Format", "default")
	req.Header.Set("Accept", "application/json")

	resp, err := c.HTTPClient.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if err != nil {
		return err
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return fmt.Errorf("%s %s: %s", path, resp.Status, truncate(string(body), 300))
	}
	if out == nil || len(body) == 0 {
		return nil
	}
	if err := json.Unmarshal(body, out); err != nil {
		return fmt.Errorf("%s: decode: %w; body=%s", path, err, truncate(string(body), 200))
	}
	return nil
}

func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n] + "..."
}
