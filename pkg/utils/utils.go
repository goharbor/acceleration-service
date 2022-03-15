package utils

import (
	"bytes"
	"context"
	"encoding/json"

	"github.com/containerd/containerd/content"
	"github.com/opencontainers/go-digest"
	ocispec "github.com/opencontainers/image-spec/specs-go/v1"
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
