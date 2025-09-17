package nydus

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"testing"

	"github.com/containerd/containerd/v2/core/content"
	"github.com/containerd/containerd/v2/core/images"
	"github.com/containerd/containerd/v2/plugins/content/local"
	nydusutils "github.com/goharbor/acceleration-service/pkg/driver/nydus/utils"
	"github.com/opencontainers/go-digest"
	specs "github.com/opencontainers/image-spec/specs-go"
	ocispec "github.com/opencontainers/image-spec/specs-go/v1"
	"github.com/pkg/errors"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type testManifest struct {
	name               string
	mediaType          string
	layers             []ocispec.Descriptor
	config             ocispec.Image
	configMediaType    string
	expectedLayerCount int
}

func Test_PrependEmptyLayer(t *testing.T) {
	tests := []testManifest{
		{
			name:      "oci_manifest_single_layer",
			mediaType: ocispec.MediaTypeImageManifest,
			layers: []ocispec.Descriptor{
				{
					MediaType: ocispec.MediaTypeImageLayerGzip,
					Digest:    "sha256:existing-layer-digest",
					Size:      1000,
				},
			},
			config: ocispec.Image{
				RootFS: ocispec.RootFS{
					Type:    "layers",
					DiffIDs: []digest.Digest{"sha256:existing-diff-id"},
				},
				History: []ocispec.History{
					{
						CreatedBy: "test layer",
					},
				},
			},
			configMediaType:    ocispec.MediaTypeImageConfig,
			expectedLayerCount: 2,
		},
		{
			name:      "oci_manifest_multiple_layers",
			mediaType: ocispec.MediaTypeImageManifest,
			layers: []ocispec.Descriptor{
				{
					MediaType: ocispec.MediaTypeImageLayerGzip,
					Digest:    "sha256:layer1-digest",
					Size:      1000,
				},
				{
					MediaType: ocispec.MediaTypeImageLayerGzip,
					Digest:    "sha256:layer2-digest",
					Size:      2000,
				},
			},
			config: ocispec.Image{
				RootFS: ocispec.RootFS{
					Type:    "layers",
					DiffIDs: []digest.Digest{"sha256:diff-id1", "sha256:diff-id2"},
				},
				History: []ocispec.History{
					{
						CreatedBy: "layer 1",
					},
					{
						CreatedBy: "layer 2",
					},
				},
			},
			configMediaType:    ocispec.MediaTypeImageConfig,
			expectedLayerCount: 3,
		},
		{
			name:      "oci_manifest_no_layers",
			mediaType: ocispec.MediaTypeImageManifest,
			layers:    []ocispec.Descriptor{},
			config: ocispec.Image{
				RootFS: ocispec.RootFS{
					Type:    "layers",
					DiffIDs: []digest.Digest{},
				},
				History: []ocispec.History{},
			},
			configMediaType:    ocispec.MediaTypeImageConfig,
			expectedLayerCount: 1,
		},
		{
			name:      "docker_manifest_single_layer",
			mediaType: images.MediaTypeDockerSchema2Manifest,
			layers: []ocispec.Descriptor{
				{
					MediaType: images.MediaTypeDockerSchema2LayerGzip,
					Digest:    "sha256:docker-layer-digest",
					Size:      1500,
				},
			},
			config: ocispec.Image{
				RootFS: ocispec.RootFS{
					Type:    "layers",
					DiffIDs: []digest.Digest{"sha256:docker-diff-id"},
				},
				History: []ocispec.History{
					{
						CreatedBy: "docker layer",
					},
				},
			},
			configMediaType:    images.MediaTypeDockerSchema2Config,
			expectedLayerCount: 2,
		},
		{
			name:      "docker_manifest_multiple_layers",
			mediaType: images.MediaTypeDockerSchema2Manifest,
			layers: []ocispec.Descriptor{
				{
					MediaType: images.MediaTypeDockerSchema2LayerGzip,
					Digest:    "sha256:docker-layer1-digest",
					Size:      1000,
				},
				{
					MediaType: images.MediaTypeDockerSchema2LayerGzip,
					Digest:    "sha256:docker-layer2-digest",
					Size:      2000,
				},
			},
			config: ocispec.Image{
				RootFS: ocispec.RootFS{
					Type:    "layers",
					DiffIDs: []digest.Digest{"sha256:docker-diff-id1", "sha256:docker-diff-id2"},
				},
				History: []ocispec.History{
					{
						CreatedBy: "docker layer 1",
					},
					{
						CreatedBy: "docker layer 2",
					},
				},
			},
			configMediaType:    images.MediaTypeDockerSchema2Config,
			expectedLayerCount: 3,
		},
		{
			name:      "docker_manifest_no_layers",
			mediaType: images.MediaTypeDockerSchema2Manifest,
			layers:    []ocispec.Descriptor{},
			config: ocispec.Image{
				RootFS: ocispec.RootFS{
					Type:    "layers",
					DiffIDs: []digest.Digest{},
				},
				History: []ocispec.History{},
			},
			configMediaType:    images.MediaTypeDockerSchema2Config,
			expectedLayerCount: 1,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ctx := context.Background()
			tempDir := t.TempDir()
			defer func() {
				if err := os.RemoveAll(tempDir); err != nil {
					t.Logf("failed to cleanup temp dir: %v", err)
				}
			}()

			// Create content store
			cs, err := local.NewStore(tempDir)
			require.NoError(t, err, "failed to create content store")

			// Setup test data
			configDesc, err := createConfigBlob(ctx, cs, tt.configMediaType, tt.config)
			require.NoError(t, err, "failed to create config blob")

			manifestDesc, err := createManifestBlob(ctx, cs, tt.mediaType, tt.layers, *configDesc)
			require.NoError(t, err, "failed to create manifest blob")

			// Test PrependEmptyLayer
			newManifestDesc, err := PrependEmptyLayer(ctx, cs, *manifestDesc)
			require.NoError(t, err, "PrependEmptyLayer should succeed")

			// Verify results
			verifyPrependResults(ctx, t, cs, *manifestDesc, newManifestDesc, tt.config, tt.expectedLayerCount)
		})
	}
}

func createConfigBlob(ctx context.Context, cs content.Store, mediaType string, config ocispec.Image) (*ocispec.Descriptor, error) {
	configDesc, configBytes, err := nydusutils.MarshalToDesc(config, mediaType)
	if err != nil {
		return nil, errors.Wrap(err, "failed to marshal config")
	}

	err = content.WriteBlob(ctx, cs, configDesc.Digest.String(), bytes.NewReader(configBytes), *configDesc)
	if err != nil {
		return nil, errors.Wrap(err, "failed to write config blob")
	}
	return configDesc, nil
}

func createManifestBlob(ctx context.Context, cs content.Store, mediaType string, layers []ocispec.Descriptor, configDesc ocispec.Descriptor) (*ocispec.Descriptor, error) {
	manifest := ocispec.Manifest{
		Versioned: specs.Versioned{
			SchemaVersion: 2,
		},
		MediaType: mediaType,
		Config:    configDesc,
		Layers:    layers,
	}

	manifestDesc, manifestBytes, err := nydusutils.MarshalToDesc(manifest, mediaType)
	if err != nil {
		return nil, errors.Wrap(err, "failed to marshal manifest")
	}

	err = content.WriteBlob(ctx, cs, manifestDesc.Digest.String(), bytes.NewReader(manifestBytes), *manifestDesc)
	if err != nil {
		return nil, errors.Wrap(err, "failed to write manifest blob")
	}
	return manifestDesc, nil
}

func verifyPrependResults(ctx context.Context, t *testing.T, cs content.Store, originalManifestDesc, newManifestDesc ocispec.Descriptor, originalConfig ocispec.Image, expectedLayerCount int) {
	// Verify manifest descriptor changes
	assert.Equal(t, originalManifestDesc.MediaType, newManifestDesc.MediaType, "media type should remain unchanged")
	assert.Equal(t, originalManifestDesc.Digest.String(), newManifestDesc.Annotations[annotationSourceDigest], "source digest annotation should match original manifest digest")

	// Read the new manifest
	newManifestBytes, err := content.ReadBlob(ctx, cs, newManifestDesc)
	require.NoError(t, err, "failed to read new manifest")

	var newManifest ocispec.Manifest
	require.NoError(t, json.Unmarshal(newManifestBytes, &newManifest), "failed to unmarshal new manifest")

	assert.Len(t, newManifest.Layers, expectedLayerCount, "layer count should match expected")

	// Verify empty layer is first
	emptyLayer := newManifest.Layers[0]
	expectedEmptyDigest := "sha256:4f4fb700ef54461cfa02571ae0db9a0dc1e0cdb5577484a6d75e68dc38e8acc1"
	assert.Equal(t, expectedEmptyDigest, emptyLayer.Digest.String(), "empty layer digest should match expected")
	assert.Equal(t, int64(32), emptyLayer.Size, "empty layer size should be 32 bytes")

	// Verify media types based on the original manifest type
	var expectedEmptyLayerMediaType, expectedconfigDescriptorMediaType string
	switch originalManifestDesc.MediaType {
	case ocispec.MediaTypeImageManifest:
		expectedEmptyLayerMediaType = ocispec.MediaTypeImageLayerGzip
		expectedconfigDescriptorMediaType = ocispec.MediaTypeImageConfig
	case images.MediaTypeDockerSchema2Manifest, images.MediaTypeDockerSchema1Manifest:
		expectedEmptyLayerMediaType = images.MediaTypeDockerSchema2LayerGzip
		expectedconfigDescriptorMediaType = images.MediaTypeDockerSchema2Config
	}
	assert.Equal(t, expectedEmptyLayerMediaType, emptyLayer.MediaType, "empty layer media type should match manifest type")
	assert.Equal(t, expectedconfigDescriptorMediaType, newManifest.Config.MediaType, "config media type should match manifest type")

	assert.Equal(t, originalManifestDesc.Digest.String(), newManifest.Annotations[annotationSourceDigest],
		"source digest annotation should match original manifest digest")

	// Read and verify new config
	newConfigBytes, err := content.ReadBlob(ctx, cs, newManifest.Config)
	require.NoError(t, err, "failed to read new config")

	var newConfig ocispec.Image
	require.NoError(t, json.Unmarshal(newConfigBytes, &newConfig), "failed to unmarshal new config")

	expectedDiffIDCount := expectedLayerCount
	assert.Len(t, newConfig.RootFS.DiffIDs, expectedDiffIDCount, "diff IDs count should increase by 1")

	// Verify empty diff ID is first and matches constant
	assert.Equal(t, emptyTarGzipUnpackedDigest, newConfig.RootFS.DiffIDs[0],
		"first diff ID should match empty tar gzip unpacked digest constant")

	expectedHistoryCount := len(originalConfig.History) + 1
	assert.Len(t, newConfig.History, expectedHistoryCount, "history count should increase by 1")

	// Verify empty layer history entry
	emptyHistory := newConfig.History[0]
	assert.Equal(t, "Nydus Converter", emptyHistory.CreatedBy, "empty layer should be created by Nydus Converter")
	assert.Equal(t, "Nydus Empty Layer", emptyHistory.Comment, "empty layer comment should be correct")
}

func Test_generateDockerEmptyLayer(t *testing.T) {
	emptyLayer := generateDockerEmptyLayer()

	expectedSize := 32
	expectedDigest := "sha256:4f4fb700ef54461cfa02571ae0db9a0dc1e0cdb5577484a6d75e68dc38e8acc1"

	assert.Len(t, emptyLayer, expectedSize, "empty layer should be 32 bytes")

	actualDigest := digest.FromBytes(emptyLayer).String()
	assert.Equal(t, expectedDigest, actualDigest, "empty layer digest should match expected")
}
