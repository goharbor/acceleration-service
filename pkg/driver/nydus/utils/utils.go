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

package utils

import (
	"encoding/binary"
	"encoding/json"
	"fmt"
	"io"
	"unsafe"

	"github.com/containerd/containerd/platforms"
	digest "github.com/opencontainers/go-digest"
	ocispec "github.com/opencontainers/image-spec/specs-go/v1"
	"github.com/pkg/errors"
)

const RafsV6Magic = 0xE0F5E1E2
const ChunkInfoOffset = 1024 + 128 + 24
const RafsV6SuppeOffset = 1024
const MaxSuperBlockSize = 8192

var nativeEndian binary.ByteOrder

func init() {
	buf := [2]byte{}
	*(*uint16)(unsafe.Pointer(&buf[0])) = uint16(0xABCD)

	switch buf {
	case [2]byte{0xCD, 0xAB}:
		nativeEndian = binary.LittleEndian
	case [2]byte{0xAB, 0xCD}:
		nativeEndian = binary.BigEndian
	default:
		panic("Could not determine native endianness.")
	}
}

func MarshalToDesc(data interface{}, mediaType string) (*ocispec.Descriptor, []byte, error) {
	bytes, err := json.MarshalIndent(data, "", "  ")
	if err != nil {
		return nil, nil, err
	}

	dataDigest := digest.FromBytes(bytes)
	desc := ocispec.Descriptor{
		Digest:    dataDigest,
		Size:      int64(len(bytes)),
		MediaType: mediaType,
	}

	return &desc, bytes, nil
}

func IsNydusPlatform(platform *ocispec.Platform) bool {
	if platform != nil && platform.OSFeatures != nil {
		for _, key := range platform.OSFeatures {
			if key == ManifestOSFeatureNydus {
				return true
			}
		}
	}
	return false
}

func IsNydusManifest(manifest *ocispec.Manifest) bool {
	for _, layer := range manifest.Layers {
		if layer.Annotations == nil {
			continue
		}
		if _, ok := layer.Annotations[LayerAnnotationNydusBootstrap]; ok {
			return true
		}
	}
	return false
}

type ExcludeNydusPlatformComparer struct {
	platforms.MatchComparer
}

func (c ExcludeNydusPlatformComparer) Match(platform ocispec.Platform) bool {
	for _, key := range platform.OSFeatures {
		if key == ManifestOSFeatureNydus {
			return false
		}
	}
	return c.MatchComparer.Match(platform)
}

func (c ExcludeNydusPlatformComparer) Less(a, b ocispec.Platform) bool {
	return c.MatchComparer.Less(a, b)
}

type NydusPlatformComparer struct {
	platforms.MatchComparer
}

func (c NydusPlatformComparer) Match(platform ocispec.Platform) bool {
	for _, key := range platform.OSFeatures {
		if key == ManifestOSFeatureNydus {
			return true
		}
	}
	return false
}

func (c NydusPlatformComparer) Less(a, b ocispec.Platform) bool {
	return c.MatchComparer.Less(a, b)
}

func GetRawBootstrapFromV6(file io.ReadSeeker) (io.Reader, error) {
	buf := make([]byte, 8)
	if _, err := file.Seek(RafsV6SuppeOffset, io.SeekStart); err != nil {
		return nil, errors.Wrapf(err, "invalid bootstrap size, the bootstrap must large than %d", RafsV6SuppeOffset)
	}

	if _, err := file.Read(buf); err != nil {
		return nil, errors.Wrapf(err, "failed to read magic number")
	}

	if nativeEndian.Uint32(buf) != RafsV6Magic {
		return nil, fmt.Errorf("invalid bootstrap v6 format")
	}

	if _, err := file.Seek(ChunkInfoOffset, io.SeekStart); err != nil {
		return nil, errors.Wrapf(err, "invalid bootstrap size, the bootstrap must large than %d", ChunkInfoOffset)
	}

	if _, err := file.Read(buf); err != nil {
		return nil, errors.Wrapf(err, "failed to read from bootstrap")
	}

	rawSize := nativeEndian.Uint64(buf)
	if rawSize < MaxSuperBlockSize {
		return nil, fmt.Errorf("invalid bootstrap size %d", rawSize)
	}
	if _, err := file.Seek(0, io.SeekStart); err != nil {
		return nil, errors.Wrapf(err, "failed to seek to start for bootstrap")
	}
	return io.LimitReader(file, int64(rawSize)), nil
}
