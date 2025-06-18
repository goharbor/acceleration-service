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

package main

import (
	"fmt"
	"os"
	"time"

	"github.com/pkg/errors"
	"github.com/sirupsen/logrus"
	cli "github.com/urfave/cli/v2"

	"github.com/goharbor/acceleration-service/pkg/config"
	"github.com/goharbor/acceleration-service/pkg/daemon"
)

var versionTag string
var versionGitCommit string
var versionBuildTime string

func main() {
	logrus.SetFormatter(&logrus.TextFormatter{
		FullTimestamp:   true,
		TimestampFormat: time.RFC3339Nano,
	})

	version := fmt.Sprintf("%s %s.%s", versionTag, versionGitCommit, versionBuildTime)
	logrus.Infof("Version: %s\n", version)

	app := &cli.App{
		Name:    "acceld",
		Usage:   "Provides a general service to support image acceleration based on kinds of accelerator like Nydus and eStargz etc.",
		Version: version,
		Flags: []cli.Flag{
			&cli.StringFlag{Name: "config", Required: true, Usage: "Specify the path of config in yaml format"},
		},
		Action: func(c *cli.Context) error {
			cfg, err := config.Parse(c.String("config"))
			if err != nil {
				return err
			}

			d, err := daemon.NewDaemon(cfg)
			if err != nil {
				return errors.Wrap(err, "create daemon")
			}

			if err := d.Run(); err != nil {
				return errors.Wrap(err, "run daemon")
			}

			return nil
		},
	}

	err := app.Run(os.Args)
	if err != nil {
		logrus.Fatal(err)
	}
}
