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

package converter

import (
	"context"
	"fmt"
	"time"

	"github.com/containerd/containerd"
	"github.com/containerd/containerd/snapshots"
	"github.com/pkg/errors"
	"github.com/sirupsen/logrus"

	"github.com/goharbor/acceleration-service/pkg/config"
	"github.com/goharbor/acceleration-service/pkg/content"
	"github.com/goharbor/acceleration-service/pkg/converter/annotation"
	"github.com/goharbor/acceleration-service/pkg/driver"
	"github.com/goharbor/acceleration-service/pkg/errdefs"
	"github.com/goharbor/acceleration-service/pkg/metrics"
	"github.com/goharbor/acceleration-service/pkg/task"
)

var logger = logrus.WithField("module", "converter")

type Converter interface {
	// Dispatch dispatches a conversion task to worker queue
	// by specifying source image reference, the conversion is
	// asynchronous, and if the sync option is specified,
	// Dispatch will be blocked until the conversion is complete.
	Dispatch(ctx context.Context, ref string, sync bool) error
	// CheckHealth checks the containerd client can successfully
	// connect to the containerd daemon and the healthcheck service
	// returns the SERVING response.
	CheckHealth(ctx context.Context) error
}

type LocalConverter struct {
	cfg         *config.Config
	rule        *Rule
	worker      *Worker
	client      *containerd.Client
	snapshotter snapshots.Snapshotter
	driver      driver.Driver
}

func NewLocalConverter(cfg *config.Config) (*LocalConverter, error) {
	client, err := containerd.New(
		cfg.Provider.Containerd.Address,
		containerd.WithDefaultNamespace("harbor-acceleration-service"),
	)
	if err != nil {
		return nil, errors.Wrap(err, "create containerd client")
	}
	snapshotter := client.SnapshotService(cfg.Provider.Containerd.Snapshotter)

	driver, err := driver.NewLocalDriver(&cfg.Converter.Driver)
	if err != nil {
		return nil, errors.Wrap(err, "create driver")
	}

	worker, err := NewWorker(cfg.Converter.Worker)
	if err != nil {
		return nil, errors.Wrap(err, "create worker")
	}

	rule := &Rule{
		items: cfg.Converter.Rules,
	}

	handler := &LocalConverter{
		cfg:         cfg,
		rule:        rule,
		worker:      worker,
		client:      client,
		snapshotter: snapshotter,
		driver:      driver,
	}

	return handler, nil
}

func (cvt *LocalConverter) Convert(ctx context.Context, source string) error {
	ctx, done, err := cvt.client.WithLease(ctx)
	if err != nil {
		return errors.Wrap(err, "create lease")
	}
	defer done(ctx)

	target, err := cvt.rule.Map(source)
	if err != nil {
		if errors.Is(err, errdefs.ErrAlreadyConverted) {
			logrus.Infof("image has been converted: %s", source)
			return nil
		}
		return errors.Wrap(err, "create target reference by rule")
	}

	content, err := content.NewLocalProvider(
		&cvt.cfg.Provider, cvt.client, cvt.snapshotter,
	)
	if err != nil {
		return errors.Wrap(err, "create content provider")
	}

	logger.Infof("pulling image %s", source)
	start := time.Now()
	if err := content.Pull(ctx, source); err != nil {
		return errors.Wrap(err, "pull image")
	}
	logger.Infof("pulled image %s, elapse %s", source, time.Since(start))

	logger.Infof("converting image %s", source)
	start = time.Now()
	desc, err := cvt.driver.Convert(ctx, content)
	if err != nil {
		return errors.Wrap(err, "convert image")
	}

	if cvt.cfg.Converter.HarborAnnotation {
		// Append extra annotations to converted image for harbor usage.
		// FIXME: implement a containerd#converter.ConvertFunc to avoid creating the new manifest/index.
		desc, err = annotation.Append(ctx, content, desc, annotation.Appended{
			DriverName:    cvt.driver.Name(),
			DriverVersion: cvt.driver.Version(),
			SourceDigest:  content.Image().Target().Digest.String(),
		})
		if err != nil {
			return errors.Wrap(err, "append annotations")
		}
	}
	logger.Infof("converted image %s, elapse %s", target, time.Since(start))

	start = time.Now()
	logger.Infof("pushing image %s", target)
	if err := content.Push(ctx, *desc, target); err != nil {
		return errors.Wrap(err, "push image")
	}
	logger.Infof("pushed image %s, elapse %s", target, time.Since(start))

	return nil
}

func (cvt *LocalConverter) Dispatch(ctx context.Context, ref string, sync bool) error {
	taskID := task.Manager.Create(ref)

	if sync {
		// FIXME: The synchronous conversion task should also be
		// executed in a limited worker queue.
		return metrics.Conversion.OpWrap(func() error {
			err := cvt.Convert(ctx, ref)
			task.Manager.Finish(taskID, err)
			return err
		}, "convert")
	}

	cvt.worker.Dispatch(func() error {
		return metrics.Conversion.OpWrap(func() error {
			err := cvt.Convert(context.Background(), ref)
			task.Manager.Finish(taskID, err)
			return err
		}, "convert")
	})

	return nil
}

func (cvt *LocalConverter) CheckHealth(ctx context.Context) error {
	health, err := cvt.client.IsServing(ctx)

	msg := "containerd service is unhealthy"
	if err != nil {
		return errors.Wrap(err, msg)
	}

	if !health {
		return fmt.Errorf(msg)
	}

	return nil
}
