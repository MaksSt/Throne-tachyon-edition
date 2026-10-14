package tachyon

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"
)

type Client struct {
	base   string
	secret string
	http   *http.Client
}

type statusError struct {
	endpoint string
	status   int
}

func (e *statusError) Error() string {
	return fmt.Sprintf("Tachyon API %s: HTTP %d", e.endpoint, e.status)
}

func NewClient(base, secret string) *Client {
	// Control requests must bypass HTTP_PROXY and any system proxy.
	transport := http.DefaultTransport.(*http.Transport).Clone()
	transport.Proxy = nil
	return &Client{base: base, secret: secret, http: &http.Client{Transport: transport, Timeout: 2 * time.Second,
		CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}}
}

func (c *Client) Close() { c.http.CloseIdleConnections() }

func (c *Client) Request(ctx context.Context, method, path string, result any) error {
	req, err := http.NewRequestWithContext(ctx, method, c.base+path, nil)
	if err != nil {
		return err
	}
	req.Header.Set("Authorization", "Bearer "+c.secret)
	resp, err := c.http.Do(req)
	if err != nil {
		if requestErr, ok := err.(*url.Error); ok {
			return fmt.Errorf("Tachyon API request failed: %w", requestErr.Err)
		}
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		endpoint, _, _ := strings.Cut(path, "?")
		return &statusError{endpoint: endpoint, status: resp.StatusCode}
	}
	if result == nil {
		return nil
	}
	return json.NewDecoder(io.LimitReader(resp.Body, 16<<20)).Decode(result)
}

type Snapshot struct {
	Upload      int64        `json:"uploadTotal"`
	Download    int64        `json:"downloadTotal"`
	Connections []Connection `json:"connections"`
}

type Connection struct {
	ID       string    `json:"id"`
	Upload   int64     `json:"upload"`
	Download int64     `json:"download"`
	Start    time.Time `json:"start"`
	Chains   []string  `json:"chains"`
	Metadata struct {
		Network         string `json:"network"`
		SourceIP        string `json:"sourceIP"`
		SourcePort      string `json:"sourcePort"`
		DestinationIP   string `json:"destinationIP"`
		DestinationPort string `json:"destinationPort"`
		Host            string `json:"host"`
		ProcessPath     string `json:"processPath"`
	} `json:"metadata"`
}

func (c *Client) Snapshot(ctx context.Context) (*Snapshot, error) {
	var snapshot Snapshot
	err := c.Request(ctx, http.MethodGet, "/connections", &snapshot)
	return &snapshot, err
}

func (c *Client) CloseConnection(ctx context.Context, id string) (bool, error) {
	err := c.Request(ctx, http.MethodDelete, "/connections/"+url.PathEscape(id), nil)
	var status *statusError
	if errors.As(err, &status) && status.status == http.StatusNotFound {
		return false, nil // Stale table entries are a no-op, not newly closed traffic.
	}
	return err == nil, err
}

func (c *Client) Delay(ctx context.Context, tag, address string, timeout int32) (int32, error) {
	if timeout <= 0 {
		timeout = 3000
	}
	var result struct {
		Delay int32 `json:"delay"`
	}
	query := url.Values{"url": {address}, "timeout": {fmt.Sprint(timeout)}}
	// URL tests can legitimately outlast ordinary control calls.
	copy := *c.http
	copy.Timeout = time.Duration(timeout)*time.Millisecond + time.Second
	client := *c
	client.http = &copy
	err := client.Request(ctx, http.MethodGet, "/proxies/"+url.PathEscape(tag)+"/delay?"+query.Encode(), &result)
	return result.Delay, err
}
