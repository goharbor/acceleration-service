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
	"io/ioutil"

	"github.com/pkg/errors"
	"gopkg.in/yaml.v3"
)

type Config struct {
	Server ServerConfig `yaml:"server"`
}

type ServerConfig struct {
	Name string `yaml:"name"`
	Uds  string `yaml:"uds"`
	Host string `yaml:"host"`
	Port string `yaml:"port"`
}

func Parse(configPath string) (*Config, error) {
	bytes, err := ioutil.ReadFile(configPath)
	if err != nil {
		return nil, errors.Wrapf(err, "failed to load config: %s", configPath)
	}

	var config Config
	if err := yaml.Unmarshal(bytes, &config); err != nil {
		return nil, errors.Wrapf(err, "failed to parse config: %s", configPath)
	}

	return &config, nil
}
