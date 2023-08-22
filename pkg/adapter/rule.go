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
	"errors"
	"fmt"
	"strings"

	"github.com/containerd/containerd/reference/docker"
	"github.com/goharbor/acceleration-service/pkg/config"
	"github.com/goharbor/acceleration-service/pkg/errdefs"
)

const (
	TagSuffix      = "tag_suffix"
	CacheTagSuffix = "cache_tag_suffix"
)

// Add suffix to source image reference as the target
// image reference, for example:
// Source: 192.168.1.1/nginx:latest
// Target: 192.168.1.1/nginx:latest-suffix
func addSuffix(ref, suffix string) (string, error) {
	named, err := docker.ParseDockerRef(ref)
	if err != nil {
		return "", fmt.Errorf("invalid source image reference: %s", err)
	}
	if _, ok := named.(docker.Digested); ok {
		return "", fmt.Errorf("unsupported digested image reference: %s", named.String())
	}
	named = docker.TagNameOnly(named)
	target := named.String() + suffix
	return target, nil
}

type Rule struct {
	items []config.ConversionRule
}

// Map maps the source image reference to a new one according to
// a rule, the new one will be used as the reference of target image.
func (rule *Rule) Map(ref, opt string) (string, error) {
	switch opt {
	case TagSuffix:
		for _, item := range rule.items {
			if item.TagSuffix != "" {
				if strings.HasSuffix(ref, item.TagSuffix) {
					// FIXME: To check if an image has been converted, a better solution
					// is to use the annotation on image manifest.
					return "", errdefs.ErrAlreadyConverted
				}
				return addSuffix(ref, item.TagSuffix)
			}
		}
	case CacheTagSuffix:
		for _, item := range rule.items {
			if item.CacheTagSuffix != "" {
				if strings.HasSuffix(ref, item.CacheTagSuffix) {
					// FIXME: Ditto.A better way is to use the annotation on image manifest.
					return "", errdefs.ErrIsRemoteCache
				}
				return addSuffix(ref, item.CacheTagSuffix)
			}
		}
		// CacheTagSuffix empty means do not provide remote cache, just return empty string.
		return "", nil
	default:
		return "", fmt.Errorf("unsupported map option: %s", opt)
	}
	return "", errors.New("not found matched conversion rule")
}
