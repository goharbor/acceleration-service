package client

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"time"

	"github.com/goharbor/acceleration-service/pkg/server/util"
	"github.com/pkg/errors"
)

type Client struct {
	addr   string
	client *http.Client
}

func marshal(payload interface{}) (io.Reader, error) {
	data, err := json.Marshal(payload)
	if err != nil {
		return nil, errors.Wrap(err, "marshal json object")
	}

	return bytes.NewReader(data), nil
}

func NewClient(addr string) *Client {
	transport := &http.Transport{
		MaxIdleConns:          10,
		IdleConnTimeout:       10 * time.Second,
		ExpectContinueTimeout: 1 * time.Second,
		DialContext: func(ctx context.Context, _, _ string) (net.Conn, error) {
			dialer := &net.Dialer{
				Timeout:   5 * time.Second,
				KeepAlive: 5 * time.Second,
			}
			return dialer.DialContext(ctx, "tcp", addr)
		},
	}

	client := &http.Client{
		// Since an image conversion task may take a long time,
		// the timeout need be set to a larger value here.
		Timeout:   1 * time.Hour,
		Transport: transport,
	}

	return &Client{
		addr:   addr,
		client: client,
	}
}

func (client *Client) Request(method, path string, body io.Reader, header map[string]string) (*http.Response, error) {
	req, err := http.NewRequest(method, fmt.Sprintf("http://%s%s", client.addr, path), body)
	if err != nil {
		return nil, errors.Wrap(err, "create http request")
	}

	req.Header.Add("Content-Type", "application/json")
	for k, v := range header {
		req.Header.Add(k, v)
	}

	resp, err := client.client.Do(req)
	if err != nil {
		return nil, errors.Wrap(err, "do http request")
	}

	if resp.StatusCode >= 400 && resp.StatusCode <= 500 {
		defer resp.Body.Close()

		decoder := json.NewDecoder(resp.Body)
		var errResp util.ErrorResp
		if err := decoder.Decode(&errResp); err != nil {
			return nil, errors.Wrap(err, "decode error response")
		}

		return nil, fmt.Errorf("service response: %s", errResp.Message)
	}

	return resp, nil
}
