package utils

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"

	"github.com/containerd/containerd/content"
	"github.com/containerd/containerd/images"
	"github.com/containerd/containerd/labels"
	"github.com/containerd/platforms"
	"github.com/opencontainers/go-digest"
	ocispec "github.com/opencontainers/image-spec/specs-go/v1"
	"github.com/pkg/errors"
)

// Ported from containerd project, copyright The containerd Authors.
// https://github.com/containerd/containerd/blob/26d356d09de89b609cb75562fd87da6aa3c70740/images/converter/default.go#L385
func ReadJSON(ctx context.Context, cs content.Store, x interface{}, desc ocispec.Descriptor) (map[string]string, error) {
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

// Modified from containerd project, copyright The containerd Authors.
// https://github.com/containerd/containerd/blob/26d356d09de89b609cb75562fd87da6aa3c70740/images/converter/default.go#L401
func WriteJSON(ctx context.Context, cs content.Store, x interface{}, oldDesc ocispec.Descriptor, ref string, labels map[string]string) (*ocispec.Descriptor, error) {
	b, err := json.MarshalIndent(x, "", "  ")
	if err != nil {
		return nil, err
	}
	dgst := digest.SHA256.FromBytes(b)

	newDesc := oldDesc
	newDesc.Size = int64(len(b))
	newDesc.Digest = dgst

	if ref == "" {
		ref = dgst.String()
	}

	if err := content.WriteBlob(ctx, cs, ref, bytes.NewReader(b), newDesc, content.WithLabels(labels)); err != nil {
		return nil, err
	}

	return &newDesc, nil
}

func GetManifests(ctx context.Context, provider content.Provider, desc ocispec.Descriptor, platform platforms.MatchComparer) ([]ocispec.Descriptor, error) {
	switch desc.MediaType {
	case ocispec.MediaTypeImageIndex, images.MediaTypeDockerSchema2ManifestList:
		return images.FilterPlatforms(images.ChildrenHandler(provider), platform)(ctx, desc)
	case ocispec.MediaTypeImageManifest, images.MediaTypeDockerSchema2Manifest:
		return []ocispec.Descriptor{desc}, nil
	}
	return nil, nil
}

func UpdateLayerDiffID(ctx context.Context, cs content.Store, image ocispec.Descriptor, platform platforms.MatchComparer) error {
	maniDescs, err := GetManifests(ctx, cs, image, platform)
	if err != nil {
		return errors.Wrap(err, "get manifests")
	}

	for _, desc := range maniDescs {
		bytes, err := content.ReadBlob(ctx, cs, desc)
		if err != nil {
			return errors.Wrap(err, "read manifest")
		}

		var manifest ocispec.Manifest
		if err := json.Unmarshal(bytes, &manifest); err != nil {
			return errors.Wrap(err, "unmarshal manifest")
		}

		diffIDs, err := images.RootFS(ctx, cs, manifest.Config)
		if err != nil {
			return errors.Wrap(err, "get diff ids from config")
		}
		if len(manifest.Layers) != len(diffIDs) {
			return fmt.Errorf("unmatched layers between manifest and config: %d != %d", len(manifest.Layers), len(diffIDs))
		}

		for idx, diffID := range diffIDs {
			layerDesc := manifest.Layers[idx]
			info, err := cs.Info(ctx, layerDesc.Digest)
			if err != nil {
				return errors.Wrap(err, "get layer info")
			}
			if info.Labels == nil {
				info.Labels = map[string]string{}
			}
			info.Labels[labels.LabelUncompressed] = diffID.String()
			_, err = cs.Update(ctx, info)
			if err != nil {
				return errors.Wrap(err, "update layer label")
			}
		}
	}

	return nil
}
