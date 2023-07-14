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

package nydus

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"sync"

	//I don't know why can not import logrus directly
	"github.com/sirupsen/logrus"

	"github.com/containerd/containerd/archive/compression"
	"github.com/containerd/containerd/content"
	images "github.com/containerd/containerd/images"
	converter "github.com/containerd/containerd/images/converter"
	"github.com/containerd/containerd/labels"
	"github.com/containerd/containerd/platforms"
	accelcontent "github.com/goharbor/acceleration-service/pkg/content"
	driverUtils "github.com/goharbor/acceleration-service/pkg/driver/nydus/utils"
	"github.com/opencontainers/go-digest"
	ocispec "github.com/opencontainers/image-spec/specs-go/v1"
	"golang.org/x/sync/errgroup"
)

// Modified from containerd project, copyright The containerd Authors.
// https://github.com/containerd/containerd/blob/main/images/converter/default.go
type ConvertFunc func(ctx context.Context, cs content.Store, desc ocispec.Descriptor, cache *accelcontent.Cache) (*ocispec.Descriptor, error)

// Ported from containerd project, copyright The containerd Authors.
// github.com/containerd/containerd/blob/main/pull.go
type defaultConverter struct {
	layerConvertFunc converter.ConvertFunc
	docker2oci       bool
	platformMC       platforms.MatchComparer
	diffIDMap        map[digest.Digest]digest.Digest // key: old diffID, value: new diffID
	diffIDMapMu      sync.RWMutex
	hooks            converter.ConvertHooks
}

// Ported from containerd project, copyright The containerd Authors.
// https://github.com/containerd/containerd/blob/main/images/converter/default.go
func IndexConvertFuncWithHook(layerConvertFunc converter.ConvertFunc, docker2oci bool, platformMC platforms.MatchComparer, hooks converter.ConvertHooks) ConvertFunc {
	c := &defaultConverter{
		layerConvertFunc: layerConvertFunc,
		docker2oci:       docker2oci,
		platformMC:       platformMC,
		diffIDMap:        make(map[digest.Digest]digest.Digest),
		hooks:            hooks,
	}
	return c.convert
}

// Modified from containerd project, copyright The containerd Authors.
// https://github.com/containerd/containerd/blob/main/images/converter/default.go
func (c *defaultConverter) convert(ctx context.Context, cs content.Store, desc ocispec.Descriptor, cache *accelcontent.Cache) (*ocispec.Descriptor, error) {
	var (
		newDesc *ocispec.Descriptor
		err     error
		cached  bool
	)
	cached = false
	// Check if the descriptor has been converted, if converted, return desc
	if cache != nil && cache.Len() > 0 {
		layer, ok := cache.Get(desc.Digest.String())
		if ok {
			//TODO:may doesnot need to remove the source image layer annotation
			// delete(newDesc.Annotations, "source image layer")
			tmp := layer.(ocispec.Descriptor)
			newDesc = &tmp
			cached = true
		}
	}

	if !cached && images.IsLayerType(desc.MediaType) {
		newDesc, err = c.convertLayer(ctx, cs, desc, cache)
		cache.Add(desc.Digest.String(), *newDesc)
	} else if !cached && images.IsManifestType(desc.MediaType) {
		newDesc, err = c.convertManifest(ctx, cs, desc, cache)
	} else if !cached && images.IsIndexType(desc.MediaType) {
		newDesc, err = c.convertIndex(ctx, cs, desc, cache)
	} else if !cached && images.IsConfigType(desc.MediaType) {
		newDesc, err = c.convertConfig(ctx, cs, desc, cache)
	}

	if err != nil {
		return nil, err
	}

	if c.hooks.PostConvertHook != nil {
		if newDescPost, err := c.hooks.PostConvertHook(ctx, cs, desc, newDesc); err != nil {
			return nil, err
		} else if newDescPost != nil {
			newDesc = newDescPost
		}
	}

	if images.IsDockerType(desc.MediaType) {
		if c.docker2oci {
			if newDesc == nil {
				newDesc = copyDesc(desc)
			}
			newDesc.MediaType = converter.ConvertDockerMediaTypeToOCI(newDesc.MediaType)
		} else if (newDesc == nil && len(desc.Annotations) != 0) || (newDesc != nil && len(newDesc.Annotations) != 0) {
			// Annotations is supported only on OCI manifest.
			// We need to remove annotations for Docker media types.
			if newDesc == nil {
				newDesc = copyDesc(desc)
			}
			newDesc.Annotations = nil
		}
	}
	logrus.WithField("old", desc).WithField("new", newDesc).Debugf("converted")
	return newDesc, nil
}

// Ported from containerd project, copyright The containerd Authors.
// https://github.com/containerd/containerd/blob/main/images/converter/default.go
func (c *defaultConverter) convertLayer(ctx context.Context, cs content.Store, desc ocispec.Descriptor, cache *accelcontent.Cache) (*ocispec.Descriptor, error) {

	if c.layerConvertFunc != nil {
		return c.layerConvertFunc(ctx, cs, desc)
	}
	return nil, nil
}

// Modified from containerd project, copyright The containerd Authors.
// https://github.com/containerd/containerd/blob/main/images/converter/default.go
func (c *defaultConverter) convertManifest(ctx context.Context, cs content.Store, desc ocispec.Descriptor, cache *accelcontent.Cache) (*ocispec.Descriptor, error) {
	var (
		manifest ocispec.Manifest
		modified bool
	)
	labels, err := readJSON(ctx, cs, &manifest, desc)
	if err != nil {
		return nil, err
	}
	if labels == nil {
		labels = make(map[string]string)
	}
	if images.IsDockerType(manifest.MediaType) && c.docker2oci {
		manifest.MediaType = converter.ConvertDockerMediaTypeToOCI(manifest.MediaType)
		modified = true
	}
	var mu sync.Mutex
	eg, ctx2 := errgroup.WithContext(ctx)
	for i, l := range manifest.Layers {
		i := i
		l := l
		oldDiffID, err := images.GetDiffID(ctx, cs, l)
		if err != nil {
			return nil, err
		}
		eg.Go(func() error {
			newL, err := c.convert(ctx2, cs, l, cache)
			if err != nil {
				return err
			}
			if newL != nil {
				mu.Lock()
				// update GC labels
				converter.ClearGCLabels(labels, l.Digest)
				labelKey := fmt.Sprintf("containerd.io/gc.ref.content.l.%d", i)
				labels[labelKey] = newL.Digest.String()
				manifest.Layers[i] = *newL
				modified = true
				mu.Unlock()

				// diffID changes if the tar entries were modified.
				// diffID stays same if only the compression type was changed.
				// When diffID changed, add a map entry so that we can update image config.
				newDiffID, err := GetDiffID(ctx, cs, *newL)
				if err != nil {
					return err
				}
				if newDiffID != oldDiffID {
					c.diffIDMapMu.Lock()
					c.diffIDMap[oldDiffID] = newDiffID
					c.diffIDMapMu.Unlock()
				}
			}
			return nil
		})
	}
	if err := eg.Wait(); err != nil {
		return nil, err
	}

	newConfig, err := c.convert(ctx, cs, manifest.Config, cache)
	if err != nil {
		return nil, err
	}
	if newConfig != nil {
		converter.ClearGCLabels(labels, manifest.Config.Digest)
		labels["containerd.io/gc.ref.content.config"] = newConfig.Digest.String()
		manifest.Config = *newConfig
		modified = true
	}

	if modified {
		return writeJSON(ctx, cs, &manifest, desc, labels)
	}
	return nil, nil
}

// Ported from containerd project, copyright The containerd Authors.
// https://github.com/containerd/containerd/blob/main/images/converter/default.go
func (c *defaultConverter) convertIndex(ctx context.Context, cs content.Store, desc ocispec.Descriptor, cache *accelcontent.Cache) (*ocispec.Descriptor, error) {
	var (
		index    ocispec.Index
		modified bool
	)
	labels, err := readJSON(ctx, cs, &index, desc)
	if err != nil {
		return nil, err
	}
	if labels == nil {
		labels = make(map[string]string)
	}
	if images.IsDockerType(index.MediaType) && c.docker2oci {
		index.MediaType = converter.ConvertDockerMediaTypeToOCI(index.MediaType)
		modified = true
	}

	newManifests := make([]ocispec.Descriptor, len(index.Manifests))
	newManifestsToBeRemoved := make(map[int]struct{}) // slice index
	var mu sync.Mutex
	eg, ctx2 := errgroup.WithContext(ctx)
	for i, mani := range index.Manifests {
		i := i
		mani := mani
		labelKey := fmt.Sprintf("containerd.io/gc.ref.content.m.%d", i)
		eg.Go(func() error {
			if mani.Platform != nil && !c.platformMC.Match(*mani.Platform) {
				mu.Lock()
				converter.ClearGCLabels(labels, mani.Digest)
				newManifestsToBeRemoved[i] = struct{}{}
				modified = true
				mu.Unlock()
				return nil
			}
			newMani, err := c.convert(ctx2, cs, mani, cache)
			if err != nil {
				return err
			}
			mu.Lock()
			if newMani != nil {
				converter.ClearGCLabels(labels, mani.Digest)
				labels[labelKey] = newMani.Digest.String()
				// NOTE: for keeping manifest order, we specify `i` index explicitly
				newManifests[i] = *newMani
				modified = true
			} else {
				newManifests[i] = mani
			}
			mu.Unlock()
			return nil
		})
	}
	if err := eg.Wait(); err != nil {
		return nil, err
	}
	if modified {
		var newManifestsClean []ocispec.Descriptor
		for i, m := range newManifests {
			if _, ok := newManifestsToBeRemoved[i]; !ok {
				newManifestsClean = append(newManifestsClean, m)
			}
		}
		index.Manifests = newManifestsClean
		return writeJSON(ctx, cs, &index, desc, labels)
	}
	return nil, nil
}

// Ported from containerd project, copyright The containerd Authors.
// https://github.com/containerd/containerd/blob/main/images/converter/default.go
func copyDesc(desc ocispec.Descriptor) *ocispec.Descriptor {
	descCopy := desc
	return &descCopy
}

// Ported from containerd project, copyright The containerd Authors.
// https://github.com/containerd/containerd/blob/main/images/converter/default.go
func clearDockerV1DummyID(cfg converter.DualConfig) (bool, error) {
	var modified bool
	f := func(k string) error {
		if configX, ok := cfg[k]; ok && configX != nil {
			var configField map[string]*json.RawMessage
			if err := json.Unmarshal(*configX, &configField); err != nil {
				return err
			}
			delete(configField, "Image")
			b, err := json.Marshal(configField)
			if err != nil {
				return err
			}
			cfg[k] = (*json.RawMessage)(&b)
			modified = true
		}
		return nil
	}
	if err := f("config"); err != nil {
		return modified, err
	}
	if err := f("container_config"); err != nil {
		return modified, err
	}
	return modified, nil
}

// Ported from containerd project, copyright The containerd Authors.
// https://github.com/containerd/containerd/blob/main/images/converter/default.go
func (c *defaultConverter) convertConfig(ctx context.Context, cs content.Store, desc ocispec.Descriptor, cache *accelcontent.Cache) (*ocispec.Descriptor, error) {
	var (
		cfg      converter.DualConfig
		cfgAsOCI ocispec.Image // read only, used for parsing cfg
		modified bool
	)

	labels, err := readJSON(ctx, cs, &cfg, desc)
	if err != nil {
		return nil, err
	}
	if labels == nil {
		labels = make(map[string]string)
	}
	if _, err := readJSON(ctx, cs, &cfgAsOCI, desc); err != nil {
		return nil, err
	}

	if rootfs := cfgAsOCI.RootFS; rootfs.Type == "layers" {
		rootfsModified := false
		c.diffIDMapMu.RLock()
		for i, oldDiffID := range rootfs.DiffIDs {
			if newDiffID, ok := c.diffIDMap[oldDiffID]; ok && newDiffID != oldDiffID {
				rootfs.DiffIDs[i] = newDiffID
				rootfsModified = true
			}
		}
		c.diffIDMapMu.RUnlock()
		if rootfsModified {
			rootfsB, err := json.Marshal(rootfs)
			if err != nil {
				return nil, err
			}
			cfg["rootfs"] = (*json.RawMessage)(&rootfsB)
			modified = true
		}
	}

	if modified {
		// cfg may have dummy value for legacy `.config.Image` and `.container_config.Image`
		// We should clear the ID if we changed the diff IDs.
		if _, err := clearDockerV1DummyID(cfg); err != nil {
			return nil, err
		}
		return writeJSON(ctx, cs, &cfg, desc, labels)
	}
	return nil, nil
}

// Ported from containerd project, copyright The containerd Authors.
// https://github.com/containerd/containerd/blob/main/images/converter/default.go
func readJSON(ctx context.Context, cs content.Store, x interface{}, desc ocispec.Descriptor) (map[string]string, error) {
	info, err := cs.Info(ctx, desc.Digest)
	if err != nil {
		return nil, err
	}
	labels := info.Labels
	b, err := content.ReadBlob(ctx, cs, desc)
	if err != nil {
		return nil, err
	}
	if err := json.Unmarshal(b, x); err != nil {
		return nil, err
	}
	return labels, nil
}

// Ported from containerd project, copyright The containerd Authors.
// https://github.com/containerd/containerd/blob/main/images/converter/default.go
func writeJSON(ctx context.Context, cs content.Store, x interface{}, oldDesc ocispec.Descriptor, labels map[string]string) (*ocispec.Descriptor, error) {
	b, err := json.Marshal(x)
	if err != nil {
		return nil, err
	}
	dgst := digest.SHA256.FromBytes(b)
	ref := fmt.Sprintf("converter-write-json-%s", dgst.String())
	w, err := content.OpenWriter(ctx, cs, content.WithRef(ref))
	if err != nil {
		return nil, err
	}
	if err := content.Copy(ctx, w, bytes.NewReader(b), int64(len(b)), dgst, content.WithLabels(labels)); err != nil {
		w.Close()
		return nil, err
	}
	if err := w.Close(); err != nil {
		return nil, err
	}
	newDesc := oldDesc
	newDesc.Size = int64(len(b))
	newDesc.Digest = dgst
	return &newDesc, nil
}

// Modified from containerd project, copyright The containerd Authors.
// https://github.com/containerd/containerd/blob/main/images/converter/default.go
func GetDiffID(ctx context.Context, cs content.Store, desc ocispec.Descriptor) (digest.Digest, error) {

	switch desc.MediaType {
	case
		// If the layer is already uncompressed, we can just return its digest
		images.MediaTypeDockerSchema2Layer,
		ocispec.MediaTypeImageLayer,
		images.MediaTypeDockerSchema2LayerForeign,
		ocispec.MediaTypeImageLayerNonDistributable,

		//If the layer is NydusBlob,means it hit cache, return its digest
		driverUtils.MediaTypeNydusBlob:
		return desc.Digest, nil
	}
	info, err := cs.Info(ctx, desc.Digest)
	if err != nil {
		return "", err
	}
	v, ok := info.Labels[labels.LabelUncompressed]
	if ok {
		// Fast path: if the image is already unpacked, we can use the label value
		return digest.Parse(v)
	}
	// if the image is not unpacked, we may not have the label
	ra, err := cs.ReaderAt(ctx, desc)
	if err != nil {
		return "", err
	}
	defer ra.Close()
	r := content.NewReader(ra)
	uR, err := compression.DecompressStream(r)
	if err != nil {
		return "", err
	}
	defer uR.Close()
	digester := digest.Canonical.Digester()
	hashW := digester.Hash()
	if _, err := io.Copy(hashW, uR); err != nil {
		return "", err
	}
	if err := ra.Close(); err != nil {
		return "", err
	}
	digest := digester.Digest()
	// memorize the computed value
	if info.Labels == nil {
		info.Labels = make(map[string]string)
	}
	info.Labels[labels.LabelUncompressed] = digest.String()
	if _, err := cs.Update(ctx, info, "labels"); err != nil {
		logrus.WithError(err).Warnf("failed to set %s label for %s", labels.LabelUncompressed, desc.Digest)
	}
	return digest, nil
}
