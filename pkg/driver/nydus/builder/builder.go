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

package builder

import (
	"encoding/json"
	"io"
	"io/ioutil"
	"os/exec"
	"strings"

	"github.com/sirupsen/logrus"
)

var logger = logrus.WithField("module", "builder")

type Option struct {
	BootstrapPath string
	BlobDirPath   string

	DiffLayerPaths []string
	HintLayerPaths []string

	OutputJSONPath string
}

type Builder struct {
	builderPath string
	stdout      io.Writer
	stderr      io.Writer
}

type debugJSON struct {
	Version string
	Blobs   []string
}

func New(builderPath string) *Builder {
	return &Builder{
		builderPath: builderPath,
		stdout:      logger.Writer(),
		stderr:      logger.Writer(),
	}
}

// Run excutes nydus-image CLI to build source layer to Nydus format.
func (builder *Builder) Run(option Option) ([]string, error) {
	args := []string{
		"create",
		"--bootstrap",
		option.BootstrapPath,
		"--log-level",
		"warn",
		"--output-json",
		option.OutputJSONPath,
		"--blob-dir",
		option.BlobDirPath,
		"--source-type",
		"diff",
	}
	args = append(args, option.DiffLayerPaths...)
	args = append(args, option.HintLayerPaths...)

	logrus.Debugf("\tCommand: %s %s", builder.builderPath, strings.Join(args[:], " "))

	cmd := exec.Command(builder.builderPath, args...)
	cmd.Stdout = builder.stdout
	cmd.Stderr = builder.stderr

	if err := cmd.Run(); err != nil {
		logrus.WithError(err).Errorf("fail to run %v %+v", builder.builderPath, args)
		return nil, err
	}

	var data debugJSON
	jsonBytes, err := ioutil.ReadFile(option.OutputJSONPath)
	if err != nil {
		return nil, err
	}
	if err := json.Unmarshal(jsonBytes, &data); err != nil {
		return nil, err
	}

	return data.Blobs, nil
}
