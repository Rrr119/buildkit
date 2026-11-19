package local

import (
	"bytes"
	"encoding/json"
	"testing"
	"time"

	"github.com/containerd/containerd/v2/core/content"
	contentlocal "github.com/containerd/containerd/v2/plugins/content/local"
	"github.com/moby/buildkit/cache/remotecache"
	cacheimporttypes "github.com/moby/buildkit/cache/remotecache/v1/types"
	"github.com/moby/buildkit/session"
	"github.com/moby/buildkit/solver"
	digest "github.com/opencontainers/go-digest"
	ocispecs "github.com/opencontainers/image-spec/specs-go/v1"
	"github.com/stretchr/testify/require"
)

func TestLocalCacheManifestIsRegistryCompatible(t *testing.T) {
	ctx := t.Context()
	store, err := contentlocal.NewStore(t.TempDir())
	require.NoError(t, err)

	layer := []byte("cache layer")
	layerDesc := ocispecs.Descriptor{
		MediaType: ocispecs.MediaTypeImageLayerGzip,
		Digest:    digest.FromBytes(layer),
		Size:      int64(len(layer)),
	}
	require.NoError(t, content.WriteBlob(ctx, store, layerDesc.Digest.String(), bytes.NewReader(layer), layerDesc))

	exporter := remotecache.NewExporter(store, "", true, true)
	_, ok, err := exporter.Add(digest.FromString("vertex"), nil, []solver.CacheExportResult{{
		CreatedAt: time.Unix(0, 0),
		Result: &solver.Remote{
			Descriptors: []ocispecs.Descriptor{layerDesc},
			Provider:    store,
		},
	}})
	require.NoError(t, err)
	require.True(t, ok)

	response, err := exporter.Finalize(ctx)
	require.NoError(t, err)

	manifestDescJSON := response[remotecache.ExporterResponseManifestDesc]
	require.NotEmpty(t, manifestDescJSON)
	var manifestDesc ocispecs.Descriptor
	require.NoError(t, json.Unmarshal([]byte(manifestDescJSON), &manifestDesc))
	require.Equal(t, ocispecs.MediaTypeImageManifest, manifestDesc.MediaType)

	manifestJSON, err := content.ReadBlob(ctx, store, manifestDesc)
	require.NoError(t, err)
	var manifest ocispecs.Manifest
	require.NoError(t, json.Unmarshal(manifestJSON, &manifest))
	require.Equal(t, cacheimporttypes.CacheConfigMediaTypeV0, manifest.Config.MediaType)
	require.Len(t, manifest.Layers, 1)
	require.Equal(t, layerDesc.Digest, manifest.Layers[0].Digest)

	// The registry cache importer and local cache importer are the same
	// implementation, so this also verifies that a local OCI layout can be
	// consumed from a registry without a translation step.
	manager, err := remotecache.NewImporter(store).Resolve(ctx, manifestDesc, "local-test", nil)
	require.NoError(t, err)
	require.NotNil(t, manager)
}

func TestLocalCacheImporterRejectsInvalidDigest(t *testing.T) {
	sm, err := session.NewManager()
	require.NoError(t, err)

	_, _, err = ResolveCacheImporterFunc(sm)(t.Context(), nil, map[string]string{
		attrSrc:    t.TempDir(),
		attrDigest: "sha256:not-a-digest",
	})
	require.ErrorContains(t, err, "invalid local cache digest")
}
