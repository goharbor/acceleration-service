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

package config

import (
	"encoding/base64"
	"fmt"
	"io/ioutil"
	"net/url"
	"os"
	"strings"

	"github.com/docker/cli/cli/config"
	"github.com/goharbor/acceleration-service/pkg/remote"
	"github.com/pkg/errors"
	"gopkg.in/yaml.v3"
)

type Config struct {
	Server ServerConfig `yaml:"server"`
	Metric MetricConfig `yaml:"metric"`

	Provider  ProviderConfig  `yaml:"provider"`
	Converter ConverterConfig `yaml:"converter"`
}

type ServerConfig struct {
	Name string `yaml:"name"`
	Uds  string `yaml:"uds"`
	Host string `yaml:"host"`
	Port string `yaml:"port"`
}

type MetricConfig struct {
	Enabled bool `yaml:"enabled"`
}

type ProviderConfig struct {
	Source    map[string]SourceConfig `yaml:"source"`
	WorkDir   string                  `yaml:"work_dir"`
	GCPolicy  GCPolicy                `yaml:"gcpolicy"`
	CacheSize int                     `yaml:"cache_size"`
}

type GCPolicy struct {
	Threshold string `yaml:"threshold"`
}

type Webhook struct {
	AuthHeader string `yaml:"auth_header"`
}

type SourceConfig struct {
	Auth     string  `yaml:"auth"`
	Insecure bool    `yaml:"insecure"`
	Webhook  Webhook `yaml:"webhook"`
}

type ConversionRule struct {
	TagSuffix string `yaml:"tag_suffix"`
	CacheTag  string `yaml:"cache_tag"`
}

type ConverterConfig struct {
	Worker           int              `yaml:"worker"`
	Driver           DriverConfig     `yaml:"driver"`
	HarborAnnotation bool             `yaml:"harbor_annotation"`
	Platforms        string           `yaml:"platforms"`
	Rules            []ConversionRule `yaml:"rules"`
}

type DriverConfig struct {
	Type   string            `yaml:"type"`
	Config map[string]string `yaml:"config"`
}

func Parse(configPath string) (*Config, error) {
	bytes, err := ioutil.ReadFile(configPath)
	if err != nil {
		return nil, errors.Wrapf(err, "load config: %s", configPath)
	}

	var config Config
	if err := yaml.Unmarshal(bytes, &config); err != nil {
		return nil, errors.Wrapf(err, "parse config: %s", configPath)
	}

	return &config, nil
}

func (cfg *Config) Host(ref string) (remote.CredentialFunc, bool, error) {
	authorizer := func(ref string) (*SourceConfig, error) {
		refURL, err := url.Parse(fmt.Sprintf("dummy://%s", ref))
		if err != nil {
			return nil, errors.Wrap(err, "parse reference of source image")
		}

		auth := cfg.Provider.Source[refURL.Host]
		// try to finds auth for a given host in docker's config.json settings.
		if len(auth.Auth) == 0 {
			config := config.LoadDefaultConfigFile(os.Stderr)
			authConfig, err := config.GetAuthConfig(refURL.Host)
			if err != nil {
				return nil, err
			}
			if len(authConfig.Username) != 0 || len(authConfig.Password) != 0 {
				auth.Auth = base64.StdEncoding.EncodeToString([]byte(fmt.Sprintf("%s:%s", authConfig.Username, authConfig.Password)))
			}
		}
		return &auth, nil
	}

	auth, err := authorizer(ref)
	if err != nil {
		return nil, false, err
	}

	return func(host string) (string, string, error) {
		auth, err := authorizer(host)
		if err != nil {
			return "", "", err
		}

		// Leave auth empty if no authorization be required
		if strings.TrimSpace(auth.Auth) == "" {
			return "", "", nil
		}
		decoded, err := base64.StdEncoding.DecodeString(auth.Auth)
		if err != nil {
			return "", "", errors.Wrap(err, "decode base64 encoded auth string")
		}
		ary := strings.Split(string(decoded), ":")
		if len(ary) != 2 {
			return "", "", errors.New("invalid base64 encoded auth string")
		}
		return ary[0], ary[1], nil
	}, auth.Insecure, nil
}

func (cfg *Config) EnableRemoteCache() bool {
	for _, rule := range cfg.Converter.Rules {
		if rule.CacheTag != "" {
			return true
		}
	}
	return false
}
