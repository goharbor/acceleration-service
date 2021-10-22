package test

import (
	"context"
	"fmt"
	"io"
	"io/ioutil"
	"net"
	"net/http"
	"time"

	"github.com/stretchr/testify/suite"
)

type Client struct {
	addr   string
	suite  *suite.Suite
	client *http.Client
}

func NewClient(suite *suite.Suite, addr string) *Client {
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
		Timeout:   2 * time.Minute,
		Transport: transport,
	}

	return &Client{
		addr:   addr,
		suite:  suite,
		client: client,
	}
}

func (client *Client) Request(method, path string, body io.Reader, header map[string]string) (*http.Response, error) {
	req, err := http.NewRequest(method, fmt.Sprintf("http://%s%s", client.addr, path), body)
	client.suite.NoError(err)

	for k, v := range header {
		req.Header.Add(k, v)
	}

	resp, err := client.client.Do(req)
	client.suite.NoError(err)

	if resp.StatusCode >= 400 && resp.StatusCode <= 500 {
		errMsg, err := ioutil.ReadAll(resp.Body)
		client.suite.NoError(err)
		client.suite.T().Log(string(errMsg))
	}

	return resp, nil
}
