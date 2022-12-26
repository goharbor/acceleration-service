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
	"strings"

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
	Source     map[string]SourceConfig `yaml:"source"`
	Containerd ContainerdConfig        `yaml:"containerd"`
}

type Webhook struct {
	AuthHeader string `yaml:"auth_header"`
}

type SourceConfig struct {
	Auth     string  `yaml:"auth"`
	Insecure bool    `yaml:"insecure"`
	Webhook  Webhook `yaml:"webhook"`
}

type ContainerdConfig struct {
	Address     string `yaml:"address"`
	Snapshotter string `yaml:"snapshotter"`
}

type ConversionRule struct {
	TagSuffix string `yaml:"tag_suffix"`
}

type ConverterConfig struct {
	Worker           int              `yaml:"worker"`
	Driver           DriverConfig     `yaml:"driver"`
	HarborAnnotation bool             `yaml:"harbor_annotation"`
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

		auth, ok := cfg.Provider.Source[refURL.Host]
		if !ok {
			return nil, fmt.Errorf("not found matched hostname %s in config", refURL.Host)
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
