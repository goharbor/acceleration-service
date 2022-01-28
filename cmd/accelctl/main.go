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
	"text/tabwriter"
	"time"

	"github.com/sirupsen/logrus"
	"github.com/urfave/cli/v2"

	"github.com/goharbor/acceleration-service/pkg/client"
	"github.com/goharbor/acceleration-service/pkg/config"
	"github.com/goharbor/acceleration-service/pkg/handler"
)

var versionGitCommit string
var versionBuildTime string

var ctl *client.Client

func ellipsis(txt string, max int) string {
	if len(txt) > max {
		return fmt.Sprintf("%s...", txt[0:max])
	}
	return txt
}

func main() {
	logrus.SetFormatter(&logrus.TextFormatter{
		FullTimestamp:   true,
		TimestampFormat: time.RFC3339Nano,
	})

	version := fmt.Sprintf("%s.%s", versionGitCommit, versionBuildTime)

	app := &cli.App{
		Name:    "accelctl",
		Usage:   "A CLI tool to manage acceleration service",
		Version: version,
		Commands: []*cli.Command{
			{
				Flags: []cli.Flag{
					&cli.StringFlag{Name: "addr", Value: "localhost:2077", Usage: "Service address in format <host:port>."},
				},
				Before: func(c *cli.Context) error {
					ctl = client.NewClient(c.String("addr"))
					return nil
				},
				Name:  "task",
				Usage: "Manage conversion tasks in remote acceld",
				Subcommands: []*cli.Command{
					{
						Name:  "create",
						Usage: "Convert a source to an acceleration image",
						Flags: []cli.Flag{
							&cli.BoolFlag{Name: "sync", Value: false},
						},
						ArgsUsage: "[SOURCE]",
						Action: func(c *cli.Context) error {
							source := c.Args().First()
							if source == "" {
								return fmt.Errorf("source is required")
							}

							sync := c.Bool("sync")
							if sync {
								logrus.Info("Waiting task to be completed...")
							}

							if err := ctl.CreateTask(source, sync); err != nil {
								return err
							}

							if sync {
								logrus.Info("Task has been completed.")
							} else {
								logrus.Info("Submitted asynchronous task, check status by `task list`.")
							}

							return nil
						},
					},
					{
						Name:    "list",
						Aliases: []string{"ls"},
						Usage:   "List image conversion tasks",
						Action: func(c *cli.Context) error {
							tasks, err := ctl.ListTask()
							if err != nil {
								return err
							}

							writer := tabwriter.NewWriter(os.Stdout, 0, 8, 1, '\t', tabwriter.AlignRight)
							fmt.Fprintln(writer, "ID\tCREATED\tSTATUS\tSOURCE\tREASON")
							for _, task := range tasks {
								created := task.Created.Format("2006-01-02 15:04:05")
								source := ellipsis(task.Source, 80)
								reason := ellipsis(task.Reason, 80)
								fmt.Fprintf(writer, "%s\t%s\t%s\t%s\t%s\n", task.ID, created, task.Status, source, reason)
							}
							writer.Flush()

							return nil
						},
					},
				},
			},
			{
				Name:  "convert",
				Usage: "Convert an image locally (one-time mode)",
				Flags: []cli.Flag{
					&cli.StringFlag{Name: "config", Required: true, Usage: "Specify the path of config in yaml format"},
				},
				ArgsUsage: "[SOURCE]",
				Action: func(c *cli.Context) error {
					source := c.Args().First()
					if source == "" {
						return fmt.Errorf("source is required")
					}

					cfg, err := config.Parse(c.String("config"))
					if err != nil {
						return err
					}

					handler, err := handler.NewLocalHandler(cfg)
					if err != nil {
						return err
					}

					return handler.Convert(c.Context, source, true)
				},
			},
		},
	}

	err := app.Run(os.Args)
	if err != nil {
		logrus.Fatal(err)
	}
}
