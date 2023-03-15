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

	"github.com/pkg/errors"
	"github.com/sirupsen/logrus"

	"github.com/containerd/containerd/platforms"
	"github.com/containerd/containerd/reference/docker"
	"github.com/goharbor/acceleration-service/pkg/content"
	"github.com/goharbor/acceleration-service/pkg/driver"
	"github.com/goharbor/acceleration-service/pkg/errdefs"
	"github.com/goharbor/acceleration-service/pkg/utils"
)

var logger = logrus.WithField("module", "converter")

type Converter struct {
	driver     driver.Driver
	provider   content.Provider
	platformMC platforms.MatchComparer
}

func New(opts ...ConvertOpt) (*Converter, error) {
	var options ConvertOpts
	for _, opt := range opts {
		if err := opt(&options); err != nil {
			return nil, fmt.Errorf("failed to apply option: %w", err)
		}
	}

	platformMC := platforms.All
	if options.platformMC != nil {
		platformMC = options.platformMC
	}

	driver, err := driver.NewLocalDriver(options.driverType, options.driverConfig, platformMC)
	if err != nil {
		return nil, errors.Wrap(err, "create driver")
	}

	handler := &Converter{
		driver:     driver,
		provider:   options.provider,
		platformMC: platformMC,
	}

	return handler, nil
}

func (cvt *Converter) pull(ctx context.Context, source string) error {
	if err := cvt.provider.Pull(ctx, source); err != nil {
		return errors.Wrapf(err, "pull image %s", source)
	}

	image, err := cvt.provider.Image(ctx, source)
	if err != nil {
		return errors.Wrapf(err, "get image %s", source)
	}

	// Write a diff id label of layer in content store for simplifying
	// diff id calculation to speed up the conversion.
	// See: https://github.com/containerd/containerd/blob/e4fefea5544d259177abb85b64e428702ac49c97/images/diffid.go#L49
	if err := utils.UpdateLayerDiffID(ctx, cvt.provider.ContentStore(), *image, cvt.platformMC); err != nil {
		return errors.Wrap(err, "update layer diff id")
	}

	return nil
}

func (cvt *Converter) Convert(ctx context.Context, source, target string) error {
	sourceNamed, err := docker.ParseDockerRef(source)
	if err != nil {
		return errors.Wrap(err, "parse source reference")
	}
	targetNamed, err := docker.ParseDockerRef(target)
	if err != nil {
		return errors.Wrap(err, "parse target reference")
	}
	source = sourceNamed.String()
	target = targetNamed.String()

	logger.Infof("pulling image %s", source)
	start := time.Now()
	if err := cvt.pull(ctx, source); err != nil {
		if errdefs.NeedsRetryWithHTTP(err) {
			logger.Infof("try to pull with plain HTTP for %s", source)
			cvt.provider.UsePlainHTTP()
			if err := cvt.pull(ctx, source); err != nil {
				return errors.Wrap(err, "try to pull image")
			}
		} else {
			return errors.Wrap(err, "pull image")
		}
	}
	logger.Infof("pulled image %s, elapse %s", source, time.Since(start))

	logger.Infof("converting image %s", source)
	start = time.Now()
	desc, err := cvt.driver.Convert(ctx, cvt.provider, source)
	if err != nil {
		return errors.Wrap(err, "convert image")
	}
	logger.Infof("converted image %s, elapse %s", target, time.Since(start))

	start = time.Now()
	logger.Infof("pushing image %s", target)
	if err := cvt.provider.Push(ctx, *desc, target); err != nil {
		if errdefs.NeedsRetryWithHTTP(err) {
			logger.Infof("try to push with plain HTTP for %s", target)
			cvt.provider.UsePlainHTTP()
			if err := cvt.provider.Push(ctx, *desc, target); err != nil {
				return errors.Wrap(err, "try to push image")
			}
		} else {
			return errors.Wrap(err, "push image")
		}
	}
	logger.Infof("pushed image %s, elapse %s", target, time.Since(start))

	return nil
}
