package utils

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"

	"github.com/containerd/containerd/content"
	"github.com/opencontainers/go-digest"
	ocispec "github.com/opencontainers/image-spec/specs-go/v1"
	"golang.org/x/sync/singleflight"
)

var sg Singleflight

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

	if labels == nil {
		labels = map[string]string{}
	}

	return labels, nil
}

// Modified from containerd project, copyright The containerd Authors.
// https://github.com/containerd/containerd/blob/26d356d09de89b609cb75562fd87da6aa3c70740/images/converter/default.go#L401
func WriteJSON(ctx context.Context, cs content.Store, x interface{}, oldDesc ocispec.Descriptor, labels map[string]string) (*ocispec.Descriptor, error) {
	return sg.Do(oldDesc.Digest.String(), func() (*ocispec.Descriptor, error) {
		b, err := json.Marshal(x)
		if err != nil {
			return nil, err
		}
		dgst := digest.SHA256.FromBytes(b)
		ref := fmt.Sprintf("acceld-converter-write-json-%s", dgst.String())
		w, err := content.OpenWriter(ctx, cs, content.WithRef(ref))
		if err != nil {
			return nil, err
		}
		if err := content.Copy(ctx, w, bytes.NewReader(b), int64(len(b)), dgst, content.WithLabels(labels)); err != nil {
			return nil, err
		}
		if err := w.Close(); err != nil {
			return nil, err
		}
		newDesc := oldDesc
		newDesc.Size = int64(len(b))
		newDesc.Digest = dgst
		return &newDesc, nil
	})
}

type Singleflight struct {
	sg singleflight.Group
}

func (s *Singleflight) Do(key string, cb func() (*ocispec.Descriptor, error)) (*ocispec.Descriptor, error) {
	ret, err, _ := s.sg.Do(key, func() (interface{}, error) {
		return cb()
	})
	if err != nil {
		return nil, err
	}
	return ret.(*ocispec.Descriptor), nil
}
