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
	"net"
	"net/http"
	"os"
	"time"

	"github.com/containerd/containerd/v2/core/remotes"
	"github.com/containerd/containerd/v2/core/remotes/docker"
	dockerconfig "github.com/docker/cli/cli/config"
)

func newDefaultClient(skipTLSVerify bool) *http.Client {
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
			TLSClientConfig: &tls.Config{
				InsecureSkipVerify: skipTLSVerify,
			},
		},
	}
}

// CredentialFunc accepts host url parameter and returns with
// username, password and error.
type CredentialFunc = func(string) (string, string, error)

// HostFunc  accepts host url parameter and returns with
// CredentialFunc, insecure and error.
type HostFunc = func(ref string) (CredentialFunc, bool, error)

// NewDockerConfigCredFunc attempts to read docker auth config file `$DOCKER_CONFIG/config.json`
// to communicate with remote registry, `$DOCKER_CONFIG` defaults to `~/.docker`.
func NewDockerConfigCredFunc() CredentialFunc {
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

func NewResolver(insecure, plainHTTP bool, credFunc CredentialFunc) remotes.Resolver {
	registryHosts := docker.ConfigureDefaultRegistries(
		docker.WithAuthorizer(
			docker.NewDockerAuthorizer(
				docker.WithAuthClient(newDefaultClient(insecure)),
				docker.WithAuthCreds(credFunc),
			),
		),
		docker.WithClient(newDefaultClient(insecure)),
		docker.WithPlainHTTP(func(_ string) (bool, error) {
			return plainHTTP, nil
		}),
	)

	return docker.NewResolver(docker.ResolverOptions{
		Hosts: registryHosts,
	})
}
