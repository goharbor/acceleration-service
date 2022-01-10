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
	"strconv"
	"strings"

	"github.com/sirupsen/logrus"
)

var logger = logrus.WithField("module", "builder")

type Option struct {
	ParentBootstrapPath *string
	BootstrapDirPath    string
	BlobDirPath         string

	DiffLayerPaths     []string
	DiffHintLayerPaths []string
	DiffSkipLayer      *int

	OutputJSONPath string

	RafsVersion string
}

type Builder struct {
	builderPath string
	stdout      io.Writer
	stderr      io.Writer
}

type Blob struct {
	BlobID   string `json:"blob_id"`
	BlobSize int    `json:"blob_size"`
}

type Output struct {
	Version      string   `json:"version"`
	OrderedBlobs []*Blob  `json:"ordered_blobs"`
	Bootstraps   []string `json:"bootstraps"`
}

func New(builderPath string) *Builder {
	return &Builder{
		builderPath: builderPath,
		stdout:      logger.Writer(),
		stderr:      logger.Writer(),
	}
}

// Run excutes nydus-image CLI to build source layer to Nydus format.
func (builder *Builder) Run(option Option) (*Output, error) {
	args := []string{
		"create",
		"--log-level",
		"warn",
		"--output-json",
		option.OutputJSONPath,
		"--diff-bootstrap-dir",
		option.BootstrapDirPath,
		"--blob-dir",
		option.BlobDirPath,
		"--source-type",
		"diff",
		"--diff-overlay-hint",
		"--fs-version",
		option.RafsVersion,
	}
	if option.RafsVersion != "" {
		// FIXME: these options should be handled automatically in builder (nydus-image).
		args = append(args, "--disable-check")
	}

	args = append(args, option.DiffLayerPaths...)
	args = append(args, option.DiffHintLayerPaths...)
	if option.DiffSkipLayer != nil {
		args = append(args, "--diff-skip-layer", strconv.FormatUint(uint64(*option.DiffSkipLayer), 10))
	}
	if option.ParentBootstrapPath != nil {
		args = append(args, "--parent-bootstrap", *option.ParentBootstrapPath)
	}

	logrus.Debugf("\tCommand: %s %s", builder.builderPath, strings.Join(args[:], " "))

	cmd := exec.Command(builder.builderPath, args...)
	cmd.Stdout = builder.stdout
	cmd.Stderr = builder.stderr

	if err := cmd.Run(); err != nil {
		logrus.WithError(err).Errorf("fail to run %v %+v", builder.builderPath, args)
		return nil, err
	}

	var data Output
	jsonBytes, err := ioutil.ReadFile(option.OutputJSONPath)
	if err != nil {
		return nil, err
	}
	if err := json.Unmarshal(jsonBytes, &data); err != nil {
		return nil, err
	}

	return &data, nil
}
