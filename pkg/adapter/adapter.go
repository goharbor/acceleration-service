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
	"golang.org/x/sync/singleflight"

	"github.com/goharbor/acceleration-service/pkg/config"
	"github.com/goharbor/acceleration-service/pkg/content"
	"github.com/goharbor/acceleration-service/pkg/converter"
	"github.com/goharbor/acceleration-service/pkg/errdefs"
	"github.com/goharbor/acceleration-service/pkg/metrics"
	"github.com/goharbor/acceleration-service/pkg/platformutil"
	"github.com/goharbor/acceleration-service/pkg/task"
)

var dispatchSingleflight = &singleflight.Group{}

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
	content *content.Content
}

func NewLocalAdapter(cfg *config.Config) (*LocalAdapter, error) {
	allPlatforms := len(strings.TrimSpace(cfg.Converter.Platforms)) == 0
	platformMC, err := platformutil.ParsePlatforms(allPlatforms, cfg.Converter.Platforms)
	if err != nil {
		return nil, errors.Wrap(err, "invalid platform configuration")
	}

	provider, content, err := content.NewLocalProvider(cfg, platformMC)
	if err != nil {
		return nil, errors.Wrap(err, "create content provider")
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
	target, err := adp.rule.Map(source, TagSuffix)
	if err != nil {
		if errors.Is(err, errdefs.ErrAlreadyConverted) {
			logrus.Infof("image has been converted: %s", source)
			return nil
		}
		return errors.Wrap(err, "create target reference by rule")
	}
	cacheRef, err := adp.rule.Map(source, CacheTagSuffix)
	if err != nil {
		if errors.Is(err, errdefs.ErrIsRemoteCache) {
			logrus.Infof("image was remote cache: %s", source)
			return nil
		}
	}
	if err = adp.content.NewRemoteCache(cacheRef); err != nil {
		return err
	}
	if _, err = adp.cvt.Convert(ctx, source, target, cacheRef); err != nil {
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
		// If the ref is same, we only convert once in the same time.
		_, err, _ := dispatchSingleflight.Do(ref, func() (interface{}, error) {
			return nil, metrics.Conversion.OpWrap(func() error {
				return adp.Convert(namespaces.WithNamespace(context.Background(), "acceleration-service"), ref)
			}, "convert")
		})
		task.Manager.Finish(taskID, err)
		return err
	})

	return nil
}

func (adp *LocalAdapter) CheckHealth(ctx context.Context) error {
	_, err := adp.content.Size()
	return err
}
