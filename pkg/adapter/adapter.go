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

package adapter

import (
	"context"
	"fmt"

	"github.com/containerd/containerd"
	"github.com/pkg/errors"
	"github.com/sirupsen/logrus"

	"github.com/goharbor/acceleration-service/pkg/config"
	"github.com/goharbor/acceleration-service/pkg/content"
	"github.com/goharbor/acceleration-service/pkg/converter"
	"github.com/goharbor/acceleration-service/pkg/errdefs"
	"github.com/goharbor/acceleration-service/pkg/metrics"
	"github.com/goharbor/acceleration-service/pkg/task"
)

type Adapter interface {
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

type LocalAdapter struct {
	cfg    *config.Config
	client *containerd.Client
	rule   *Rule
	worker *Worker
	cvt    *converter.LocalConverter
}

func NewLocalAdapter(cfg *config.Config) (*LocalAdapter, error) {
	client, err := containerd.New(
		cfg.Provider.Containerd.Address,
		containerd.WithDefaultNamespace("harbor-acceleration-service"),
	)
	if err != nil {
		return nil, errors.Wrap(err, "create containerd client")
	}

	provider, err := content.NewLocalProvider(client, cfg.Host)
	if err != nil {
		return nil, errors.Wrap(err, "create content provider")
	}

	cvt, err := converter.NewLocalConverter(
		converter.WithProvider(provider),
		converter.WithDriver(cfg.Converter.Driver.Type, cfg.Converter.Driver.Config),
	)
	if err != nil {
		return nil, err
	}

	worker, err := NewWorker(cfg.Converter.Worker)
	if err != nil {
		return nil, errors.Wrap(err, "create worker")
	}

	rule := &Rule{
		items: cfg.Converter.Rules,
	}

	handler := &LocalAdapter{
		cfg:    cfg,
		client: client,
		rule:   rule,
		worker: worker,
		cvt:    cvt,
	}

	return handler, nil
}

func (adp *LocalAdapter) Convert(ctx context.Context, source string) error {
	target, err := adp.rule.Map(source)
	if err != nil {
		if errors.Is(err, errdefs.ErrAlreadyConverted) {
			logrus.Infof("image has been converted: %s", source)
			return nil
		}
		return errors.Wrap(err, "create target reference by rule")
	}

	ctx, done, err := adp.client.WithLease(ctx)
	if err != nil {
		return errors.Wrap(err, "create lease")
	}
	defer done(ctx)

	return adp.cvt.Convert(ctx, source, target)
}

func (adp *LocalAdapter) Dispatch(ctx context.Context, ref string, sync bool) error {
	taskID := task.Manager.Create(ref)

	if sync {
		// FIXME: The synchronous conversion task should also be
		// executed in a limited worker queue.
		return metrics.Conversion.OpWrap(func() error {
			err := adp.Convert(ctx, ref)
			task.Manager.Finish(taskID, err)
			return err
		}, "convert")
	}

	adp.worker.Dispatch(func() error {
		return metrics.Conversion.OpWrap(func() error {
			err := adp.Convert(context.Background(), ref)
			task.Manager.Finish(taskID, err)
			return err
		}, "convert")
	})

	return nil
}

func (adp *LocalAdapter) CheckHealth(ctx context.Context) error {
	health, err := adp.client.IsServing(ctx)

	msg := "containerd service is unhealthy"
	if err != nil {
		return errors.Wrap(err, msg)
	}

	if !health {
		return fmt.Errorf(msg)
	}

	return nil
}
