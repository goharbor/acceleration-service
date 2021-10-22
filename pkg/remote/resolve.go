// Copyright Project Harbor Authors
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//     http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.

package remote

import (
	"crypto/tls"
	"encoding/base64"
	"net"
	"net/http"
	"os"
	"strings"
	"time"

	"github.com/containerd/containerd/remotes"
	"github.com/containerd/containerd/remotes/docker"
	dockerconfig "github.com/docker/cli/cli/config"
	"github.com/pkg/errors"
)

func newDefaultClient() *http.Client {
	return &http.Client{
		Transport: &http.Transport{
			Proxy: http.ProxyFromEnvironment,
			DialContext: (&net.Dialer{
				Timeout:   30 * time.Second,
				KeepAlive: 30 * time.Second,
				DualStack: true,
			}).DialContext,
			MaxIdleConns:          10,
			IdleConnTimeout:       30 * time.Second,
			TLSHandshakeTimeout:   10 * time.Second,
			ExpectContinueTimeout: 5 * time.Second,
			DisableKeepAlives:     true,
			TLSNextProto:          make(map[string]func(authority string, c *tls.Conn) http.RoundTripper),
		},
	}
}

// withCredentialFunc accepts host url parameter and returns with
// username, password and error.
type withCredentialFunc = func(string) (string, string, error)

// NewDockerConfigCredFunc attempts to read docker auth config file `$DOCKER_CONFIG/config.json`
// to communicate with remote registry, `$DOCKER_CONFIG` defaults to `~/.docker`.
func NewDockerConfigCredFunc() withCredentialFunc {
	return func(host string) (string, string, error) {
		// The host of docker hub image will be converted to `registry-1.docker.io` in:
		// github.com/containerd/containerd/remotes/docker/registry.go
		// But we need use the key `https://index.docker.io/v1/` to find auth from docker config.
		if host == "registry-1.docker.io" {
			host = "https://index.docker.io/v1/"
		}

		config := dockerconfig.LoadDefaultConfigFile(os.Stderr)
		authConfig, err := config.GetAuthConfig(host)
		if err != nil {
			return "", "", err
		}

		return authConfig.Username, authConfig.Password, nil
	}
}

// NewBasicAuthCredFunc parses base64 encoded auth string to communicate with remote registry.
func NewBasicAuthCredFunc(auth string) withCredentialFunc {
	return func(host string) (string, string, error) {
		// Leave auth empty if no authorization be required
		if strings.TrimSpace(auth) == "" {
			return "", "", nil
		}
		decoded, err := base64.StdEncoding.DecodeString(auth)
		if err != nil {
			return "", "", errors.Wrap(err, "decode base64 encoded auth string")
		}
		ary := strings.Split(string(decoded), ":")
		if len(ary) != 2 {
			return "", "", errors.New("invalid base64 encoded auth string")
		}
		return ary[0], ary[1], nil
	}
}

func NewResolver(insecure bool, credFunc withCredentialFunc) remotes.Resolver {
	registryHosts := docker.ConfigureDefaultRegistries(
		docker.WithAuthorizer(docker.NewAuthorizer(
			newDefaultClient(),
			credFunc,
		)),
		docker.WithClient(newDefaultClient()),
		docker.WithPlainHTTP(func(host string) (bool, error) {
			_insecure, err := docker.MatchLocalhost(host)
			if err != nil {
				return false, err
			}
			if _insecure {
				return true, nil
			}
			return insecure, nil
		}),
	)

	return docker.NewResolver(docker.ResolverOptions{
		Hosts: registryHosts,
	})
}
