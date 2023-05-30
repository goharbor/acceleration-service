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
	"strings"

	"github.com/containerd/containerd/namespaces"
	"github.com/pkg/errors"
	"github.com/sirupsen/logrus"

	"github.com/goharbor/acceleration-service/pkg/config"
	"github.com/goharbor/acceleration-service/pkg/content"
	"github.com/goharbor/acceleration-service/pkg/converter"
	"github.com/goharbor/acceleration-service/pkg/errdefs"
	"github.com/goharbor/acceleration-service/pkg/metrics"
	"github.com/goharbor/acceleration-service/pkg/platformutil"
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
	cfg     *config.Config
	rule    *Rule
	worker  *Worker
	cvt     *converter.Converter
	content *Content
}

func NewLocalAdapter(cfg *config.Config) (*LocalAdapter, error) {
	allPlatforms := len(strings.TrimSpace(cfg.Converter.Platforms)) == 0
	platformMC, err := platformutil.ParsePlatforms(allPlatforms, cfg.Converter.Platforms)
	if err != nil {
		return nil, errors.Wrap(err, "invalid platform configuration")
	}

	provider, db, err := content.NewLocalProvider(cfg.Provider.WorkDir, cfg.Host, platformMC)
	if err != nil {
		return nil, errors.Wrap(err, "create content provider")
	}
	content, err := NewContent(db, cfg)
	if err != nil {
		return nil, errors.Wrap(err, "create Content in LocalProvider")
	}
	cvt, err := converter.New(
		converter.WithProvider(provider),
		converter.WithDriver(cfg.Converter.Driver.Type, cfg.Converter.Driver.Config),
		converter.WithPlatform(platformMC),
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
		cfg:     cfg,
		rule:    rule,
		worker:  worker,
		cvt:     cvt,
		content: content,
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
	if _, err = adp.cvt.Convert(ctx, source, target); err != nil {
		return err
	}
	if err := adp.content.GC(ctx); err != nil {
		return err
	}
	return nil
}

func (adp *LocalAdapter) Dispatch(ctx context.Context, ref string, sync bool) error {
	taskID := task.Manager.Create(ref)

	if sync {
		// FIXME: The synchronous conversion task should also be
		// executed in a limited worker queue.
		return metrics.Conversion.OpWrap(func() error {
			err := adp.Convert(namespaces.WithNamespace(ctx, "acceleration-service"), ref)
			task.Manager.Finish(taskID, err)
			return err
		}, "convert")
	}

	adp.worker.Dispatch(func() error {
		return metrics.Conversion.OpWrap(func() error {
			err := adp.Convert(namespaces.WithNamespace(context.Background(), "acceleration-service"), ref)
			task.Manager.Finish(taskID, err)
			return err
		}, "convert")
	})

	return nil
}

func (adp *LocalAdapter) CheckHealth(ctx context.Context) error {
	_, err := adp.content.Size()
	return err
}
