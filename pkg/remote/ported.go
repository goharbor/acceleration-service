// Copyright Project Harbor Authors
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//	http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.

package remote

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"path"
	"strings"

	"github.com/containerd/containerd/v2/core/content"
	"github.com/containerd/containerd/v2/core/images"
	docker "github.com/containerd/containerd/v2/core/remotes/docker"
	"github.com/containerd/containerd/v2/pkg/reference"
	"github.com/containerd/containerd/v2/pkg/tracing"
	"github.com/containerd/errdefs"
	"github.com/containerd/log"
	acceldErrdefs "github.com/goharbor/acceleration-service/pkg/errdefs"
	ocispec "github.com/opencontainers/image-spec/specs-go/v1"
)

const maxRetry = 3

// Modified from containerd project, copyright The containerd Authors.
// https://github.com/containerd/containerd/remotes/docker/fetcher.go
func Fetch(ctx context.Context, cacheRef string, desc ocispec.Descriptor, host HostFunc, plainHTTP bool) (content.ReaderAt, error) {
	ctx = log.WithLogger(ctx, log.G(ctx).WithField("digest", desc.Digest))

	refspec, err := reference.Parse(cacheRef)
	if err != nil {
		return nil, err
	}
	cred, insecure, err := host(cacheRef)
	if err != nil {
		return nil, err
	}

	registryHosts := docker.ConfigureDefaultRegistries(
		docker.WithAuthorizer(
			docker.NewDockerAuthorizer(
				docker.WithAuthClient(newDefaultClient(insecure)),
				docker.WithAuthCreds(cred),
			),
		),
		docker.WithClient(newDefaultClient(insecure)),
		docker.WithPlainHTTP(func(_ string) (bool, error) {
			return plainHTTP, nil
		}),
	)
	hosts, err := registryHosts(refspec.Hostname())
	if err != nil {
		return nil, err
	}

	hosts = filterHosts(hosts, docker.HostCapabilityPull)
	if len(hosts) == 0 {
		return nil, fmt.Errorf("no pull hosts: %w", errdefs.ErrNotFound)
	}

	parts := strings.SplitN(refspec.Locator, "/", 2)
	if len(parts) < 2 {
		return nil, fmt.Errorf("invalid cache ref: %w", errdefs.ErrInvalidArgument)
	}
	repository := parts[1]

	ctx, err = docker.ContextWithRepositoryScope(ctx, refspec, false)
	if err != nil {
		return nil, err
	}

	hrs, _ := newHTTPReadSeeker(desc.Size, func(offset int64) (io.ReadCloser, error) {
		// firstly try fetch via external urls
		for _, us := range desc.URLs {
			u, err := url.Parse(us)
			if err != nil {
				log.G(ctx).WithError(err).Debugf("failed to parse %q", us)
				continue
			}
			if u.Scheme != "http" && u.Scheme != "https" {
				log.G(ctx).Debug("non-http(s) alternative url is unsupported")
				continue
			}

			ctx = log.WithLogger(ctx, log.G(ctx).WithField("url", u))
			log.G(ctx).Info("request")

			// Try this first, parse it
			host := docker.RegistryHost{
				Client:       http.DefaultClient,
				Host:         u.Host,
				Scheme:       u.Scheme,
				Path:         u.Path,
				Capabilities: docker.HostCapabilityPull,
			}
			header := http.Header{}
			req := newRequest(header, host, http.MethodGet, repository)
			// Strip namespace from base
			req.path = u.Path
			if u.RawQuery != "" {
				req.path = req.path + "?" + u.RawQuery
			}

			rc, err := open(ctx, req, desc.MediaType, offset)
			if err != nil {
				if errdefs.IsNotFound(err) {
					continue // try one of the other urls.
				}

				return nil, err
			}
			return rc, nil
		}

		// Try manifests endpoints for manifests types
		switch desc.MediaType {
		case images.MediaTypeDockerSchema2Manifest, images.MediaTypeDockerSchema2ManifestList,
			images.MediaTypeDockerSchema1Manifest,
			ocispec.MediaTypeImageManifest, ocispec.MediaTypeImageIndex:

			var firstErr error
			for _, host := range hosts {
				header := http.Header{}
				req := newRequest(header, host, http.MethodGet, repository, "manifests", desc.Digest.String())
				if err := req.addNamespace(refspec.Hostname()); err != nil {
					return nil, err
				}

				rc, err := open(ctx, req, desc.MediaType, offset)
				if err != nil {
					// Store the error for referencing later
					if firstErr == nil {
						firstErr = err
					}
					continue // try another host
				}

				return rc, nil
			}

			return nil, firstErr
		}

		// Finally use blobs endpoints
		var firstErr error
		for _, host := range hosts {
			header := http.Header{}
			req := newRequest(header, host, http.MethodGet, repository, "blobs", desc.Digest.String())
			if err := req.addNamespace(refspec.Hostname()); err != nil {
				return nil, err
			}

			rc, err := open(ctx, req, desc.MediaType, offset)
			if err != nil {
				// Store the error for referencing later
				if firstErr == nil {
					firstErr = err
				}
				continue // try another host
			}

			return rc, nil
		}

		if errdefs.IsNotFound(firstErr) {
			firstErr = fmt.Errorf("could not fetch content descriptor %v (%v) from remote: %w",
				desc.Digest, desc.MediaType, errdefs.ErrNotFound,
			)
		}

		return nil, firstErr

	})
	// try to use open to trigger http request
	if _, err = hrs.open(0); err != nil {
		if acceldErrdefs.NeedsRetryWithHTTP(err) {
			return Fetch(ctx, cacheRef, desc, host, true)
		}
	}
	return hrs, nil
}

// Modified from containerd project, copyright The containerd Authors.
// https://github.com/containerd/containerd/remotes/docker/httpreadseeker.go
type httpReadSeeker struct {
	size   int64
	offset int64
	rc     io.ReadCloser
	open   func(offset int64) (io.ReadCloser, error)
	closed bool

	errsWithNoProgress int
}

func newHTTPReadSeeker(size int64, open func(offset int64) (io.ReadCloser, error)) (*httpReadSeeker, error) {
	return &httpReadSeeker{
		size: size,
		open: open,
	}, nil
}

func (hrs *httpReadSeeker) ReadAt(p []byte, offset int64) (n int, err error) {
	if _, err = hrs.Seek(offset, io.SeekStart); err != nil {
		return 0, err
	}
	return hrs.Read(p)
}

func (hrs *httpReadSeeker) Size() int64 {
	return hrs.size
}

func (hrs *httpReadSeeker) Read(p []byte) (n int, err error) {
	if hrs.closed {
		return 0, io.EOF
	}

	rd, err := hrs.reader()
	if err != nil {
		return 0, err
	}

	n, err = rd.Read(p)
	hrs.offset += int64(n)
	if n > 0 || err == nil {
		hrs.errsWithNoProgress = 0
	}
	if err == io.ErrUnexpectedEOF {
		// connection closed unexpectedly. try reconnecting.
		if n == 0 {
			hrs.errsWithNoProgress++
			if hrs.errsWithNoProgress > maxRetry {
				return // too many retries for this offset with no progress
			}
		}
		if hrs.rc != nil {
			if clsErr := hrs.rc.Close(); clsErr != nil {
				log.L.WithError(clsErr).Error("httpReadSeeker: failed to close ReadCloser")
			}
			hrs.rc = nil
		}
		if _, err2 := hrs.reader(); err2 == nil {
			return n, nil
		}
	}
	return
}

func (hrs *httpReadSeeker) Close() error {
	if hrs.closed {
		return nil
	}
	hrs.closed = true
	if hrs.rc != nil {
		return hrs.rc.Close()
	}

	return nil
}

func (hrs *httpReadSeeker) Seek(offset int64, whence int) (int64, error) {
	if hrs.closed {
		return 0, fmt.Errorf("Fetcher.Seek: closed: %w", errdefs.ErrUnavailable)
	}

	abs := hrs.offset
	switch whence {
	case io.SeekStart:
		abs = offset
	case io.SeekCurrent:
		abs += offset
	case io.SeekEnd:
		if hrs.size == -1 {
			return 0, fmt.Errorf("Fetcher.Seek: unknown size, cannot seek from end: %w", errdefs.ErrUnavailable)
		}
		abs = hrs.size + offset
	default:
		return 0, fmt.Errorf("Fetcher.Seek: invalid whence: %w", errdefs.ErrInvalidArgument)
	}

	if abs < 0 {
		return 0, fmt.Errorf("Fetcher.Seek: negative offset: %w", errdefs.ErrInvalidArgument)
	}

	if abs != hrs.offset {
		if hrs.rc != nil {
			if err := hrs.rc.Close(); err != nil {
				log.L.WithError(err).Error("Fetcher.Seek: failed to close ReadCloser")
			}

			hrs.rc = nil
		}

		hrs.offset = abs
	}

	return hrs.offset, nil
}

func (hrs *httpReadSeeker) reader() (io.Reader, error) {
	if hrs.rc != nil {
		return hrs.rc, nil
	}

	if hrs.size == -1 || hrs.offset < hrs.size {
		// only try to reopen the body request if we are seeking to a value
		// less than the actual size.
		if hrs.open == nil {
			return nil, fmt.Errorf("cannot open: %w", errdefs.ErrNotImplemented)
		}

		rc, err := hrs.open(hrs.offset)
		if err != nil {
			return nil, fmt.Errorf("httpReadSeeker: failed open: %w", err)
		}

		if hrs.rc != nil {
			if err := hrs.rc.Close(); err != nil {
				log.L.WithError(err).Error("httpReadSeeker: failed to close ReadCloser")
			}
		}
		hrs.rc = rc
	} else {
		// There is an edge case here where offset == size of the content. If
		// we seek, we will probably get an error for content that cannot be
		// sought (?). In that case, we should err on committing the content,
		// as the length is already satisfied but we just return the empty
		// reader instead.

		hrs.rc = io.NopCloser(bytes.NewReader([]byte{}))
	}

	return hrs.rc, nil
}

// Ported from containerd project, copyright The containerd Authors.
// https://github.com/containerd/containerd/remotes/docker/fetcher.go
func open(ctx context.Context, req *request, mediatype string, offset int64) (_ io.ReadCloser, retErr error) {
	if mediatype == "" {
		req.header.Set("Accept", "*/*")
	} else {
		req.header.Set("Accept", strings.Join([]string{mediatype, `*/*`}, ", "))
	}

	if offset > 0 {
		// Note: "Accept-Ranges: bytes" cannot be trusted as some endpoints
		// will return the header without supporting the range. The content
		// range must always be checked.
		req.header.Set("Range", fmt.Sprintf("bytes=%d-", offset))
	}

	resp, err := req.doWithRetries(ctx, nil)
	if err != nil {
		return nil, err
	}
	defer func() {
		if retErr != nil {
			resp.Body.Close()
		}
	}()

	if resp.StatusCode > 299 {
		// TODO(stevvooe): When doing a offset specific request, we should
		// really distinguish between a 206 and a 200. In the case of 200, we
		// can discard the bytes, hiding the seek behavior from the
		// implementation.

		if resp.StatusCode == http.StatusNotFound {
			return nil, fmt.Errorf("content at %v not found: %w", req.String(), errdefs.ErrNotFound)
		}
		var registryErr docker.Errors
		if err := json.NewDecoder(resp.Body).Decode(&registryErr); err != nil || registryErr.Len() < 1 {
			return nil, fmt.Errorf("unexpected status code %v: %v", req.String(), resp.Status)
		}
		return nil, fmt.Errorf("unexpected status code %v: %s - Server message: %s", req.String(), resp.Status, registryErr.Error())
	}
	if offset > 0 {
		cr := resp.Header.Get("content-range")
		if cr != "" {
			if !strings.HasPrefix(cr, fmt.Sprintf("bytes %d-", offset)) {
				return nil, fmt.Errorf("unhandled content range in response: %v", cr)

			}
		} else {
			// TODO: Should any cases where use of content range
			// without the proper header be considered?
			// 206 responses?

			// Discard up to offset
			// Could use buffer pool here but this case should be rare
			n, err := io.Copy(io.Discard, io.LimitReader(resp.Body, offset))
			if err != nil {
				return nil, fmt.Errorf("failed to discard to offset: %w", err)
			}
			if n != offset {
				return nil, errors.New("unable to discard to offset")
			}

		}
	}

	return resp.Body, nil
}

// Ported from containerd project, copyright The containerd Authors.
// https://github.com/containerd/containerd/remotes/docker/resolver.go
type request struct {
	method string
	path   string
	header http.Header
	host   docker.RegistryHost
	body   func() (io.ReadCloser, error)
	size   int64
}

func newRequest(dockerHeader http.Header, host docker.RegistryHost, method string,
	repository string, ps ...string) *request {
	header := dockerHeader.Clone()
	if header == nil {
		header = http.Header{}
	}

	for key, value := range host.Header {
		header[key] = append(header[key], value...)
	}
	parts := append([]string{"/", host.Path, repository}, ps...)
	p := path.Join(parts...)
	// Join strips trailing slash, re-add ending "/" if included
	if len(parts) > 0 && strings.HasSuffix(parts[len(parts)-1], "/") {
		p = p + "/"
	}
	return &request{
		method: method,
		path:   p,
		header: header,
		host:   host,
	}
}

func (r *request) do(ctx context.Context) (*http.Response, error) {
	u := r.host.Scheme + "://" + r.host.Host + r.path
	req, err := http.NewRequestWithContext(ctx, r.method, u, nil)
	if err != nil {
		return nil, err
	}
	req.Header = http.Header{} // headers need to be copied to avoid concurrent map access
	for k, v := range r.header {
		req.Header[k] = v
	}
	if r.body != nil {
		body, err := r.body()
		if err != nil {
			return nil, err
		}
		req.Body = body
		req.GetBody = r.body
		if r.size > 0 {
			req.ContentLength = r.size
		}
	}

	ctx = log.WithLogger(ctx, log.G(ctx).WithField("url", u))
	if err := r.authorize(ctx, req); err != nil {
		return nil, fmt.Errorf("failed to authorize: %w", err)
	}

	client := &http.Client{}
	if r.host.Client != nil {
		*client = *r.host.Client
	}
	if client.CheckRedirect == nil {
		client.CheckRedirect = func(req *http.Request, via []*http.Request) error {
			if len(via) >= 10 {
				return errors.New("stopped after 10 redirects")
			}
			if err := r.authorize(ctx, req); err != nil {
				return fmt.Errorf("failed to authorize redirect: %w", err)
			}
			return nil
		}
	}
	tracing.UpdateHTTPClient(client, tracing.Name("remotes.docker.resolver", "HTTPRequest"))
	resp, err := client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("failed to do request: %w", err)
	}
	return resp, nil
}

func (r *request) doWithRetries(ctx context.Context, responses []*http.Response) (*http.Response, error) {
	resp, err := r.do(ctx)
	if err != nil {
		return nil, err
	}

	responses = append(responses, resp)
	retry, err := r.retryRequest(ctx, responses)
	if err != nil {
		resp.Body.Close()
		return nil, err
	}
	if retry {
		resp.Body.Close()
		return r.doWithRetries(ctx, responses)
	}
	return resp, err
}

func (r *request) authorize(ctx context.Context, req *http.Request) error {
	// Check if has header for host
	if r.host.Authorizer != nil {
		if err := r.host.Authorizer.Authorize(ctx, req); err != nil {
			return err
		}
	}
	return nil
}

func (r *request) addNamespace(ns string) (err error) {
	if !isProxy(r.host.Host, ns) {
		return nil
	}
	var q url.Values
	// Parse query
	if i := strings.IndexByte(r.path, '?'); i > 0 {
		r.path = r.path[:i+1]
		q, err = url.ParseQuery(r.path[i+1:])
		if err != nil {
			return
		}
	} else {
		r.path = r.path + "?"
		q = url.Values{}
	}
	q.Add("ns", ns)

	r.path = r.path + q.Encode()

	return
}

func (r *request) retryRequest(ctx context.Context, responses []*http.Response) (bool, error) {
	if len(responses) > 5 {
		return false, nil
	}
	last := responses[len(responses)-1]
	switch last.StatusCode {
	case http.StatusUnauthorized:
		log.G(ctx).WithField("header", last.Header.Get("WWW-Authenticate")).Debug("Unauthorized")
		if r.host.Authorizer != nil {
			if err := r.host.Authorizer.AddResponses(ctx, responses); err == nil {
				return true, nil
			} else if !errdefs.IsNotImplemented(err) {
				return false, err
			}
		}

		return false, nil
	case http.StatusMethodNotAllowed:
		// Support registries which have not properly implemented the HEAD method for
		// manifests endpoint
		if r.method == http.MethodHead && strings.Contains(r.path, "/manifests/") {
			r.method = http.MethodGet
			return true, nil
		}
	case http.StatusRequestTimeout, http.StatusTooManyRequests:
		return true, nil
	}

	// TODO: Handle 50x errors accounting for attempt history
	return false, nil
}

func (r *request) String() string {
	return r.host.Scheme + "://" + r.host.Host + r.path
}

func isProxy(host, refhost string) bool {
	if refhost != host {
		if refhost != "docker.io" || host != "registry-1.docker.io" {
			return true
		}
	}
	return false
}

func filterHosts(hosts []docker.RegistryHost, caps docker.HostCapabilities) []docker.RegistryHost {
	for _, host := range hosts {
		if host.Capabilities.Has(caps) {
			hosts = append(hosts, host)
		}
	}
	return hosts
}
