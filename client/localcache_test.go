package client

import (
	"bytes"
	"regexp"
	"testing"

	"github.com/containerd/containerd/v2/core/content"
	contentlocal "github.com/containerd/containerd/v2/plugins/content/local"
	"github.com/moby/buildkit/client/ociindex"
	digest "github.com/opencontainers/go-digest"
	ocispecs "github.com/opencontainers/image-spec/specs-go/v1"
	"github.com/stretchr/testify/require"
)

func TestLocalCacheTag(t *testing.T) {
	base, err := localCacheTag(map[string]string{})
	require.NoError(t, err)
	require.Equal(t, "latest", base)

	tagged, err := localCacheTag(map[string]string{"tag": "v1"})
	require.NoError(t, err)
	require.Equal(t, "v1", tagged)

	tenantA, err := localCacheTag(map[string]string{"tenant": "team-a", "tag": "v1"})
	require.NoError(t, err)
	tenantA2, err := localCacheTag(map[string]string{"tag": "v1", "tenant": "team-a"})
	require.NoError(t, err)
	require.Equal(t, tenantA, tenantA2)
	require.NotEqual(t, tagged, tenantA)

	tenantB, err := localCacheTag(map[string]string{"tenant": "team-b", "tag": "v1"})
	require.NoError(t, err)
	require.NotEqual(t, tenantA, tenantB)

	tenantAV2, err := localCacheTag(map[string]string{"tenant": "team-a", "tag": "v2"})
	require.NoError(t, err)
	require.NotEqual(t, tenantA, tenantAV2)

	longTag, err := localCacheTag(map[string]string{
		"tenant": "team-a",
		"tag":    string(bytes.Repeat([]byte("a"), localCacheMaxTagLength+1)),
	})
	require.NoError(t, err)
	require.LessOrEqual(t, len(longTag), localCacheMaxTagLength)
	require.Regexp(t, regexp.MustCompile(`^[\w][\w.-]*$`), longTag)

	_, err = localCacheTag(map[string]string{"tag": ""})
	require.ErrorContains(t, err, "tag must not be empty")
}

func TestLocalCacheTenantImportIsolation(t *testing.T) {
	ctx := t.Context()
	dir := t.TempDir()
	cs, err := contentlocal.NewStore(dir)
	require.NoError(t, err)

	writeBlob := func(value string) ocispecs.Descriptor {
		t.Helper()
		desc := ocispecs.Descriptor{
			MediaType: ocispecs.MediaTypeImageManifest,
			Digest:    digest.FromString(value),
			Size:      int64(len(value)),
		}
		require.NoError(t, content.WriteBlob(ctx, cs, desc.Digest.String(), bytes.NewBufferString(value), desc))
		return desc
	}
	tenantADesc := writeBlob("tenant-a cache manifest")
	tenantBDesc := writeBlob("tenant-b cache manifest")

	idx := ociindex.NewStoreIndex(dir)
	tagA, err := localCacheTag(map[string]string{"tenant": "team-a", "tag": "v1"})
	require.NoError(t, err)
	tagB, err := localCacheTag(map[string]string{"tenant": "team-b", "tag": "v1"})
	require.NoError(t, err)
	require.NoError(t, idx.Put(tenantADesc, ociindex.Tag(tagA)))
	require.NoError(t, idx.Put(tenantBDesc, ociindex.Tag(tagB)))

	for _, tc := range []struct {
		name     string
		attrs    map[string]string
		expected digest.Digest
	}{
		{
			name:     "tenant-a",
			attrs:    map[string]string{"src": dir, "tenant": "team-a", "tag": "v1"},
			expected: tenantADesc.Digest,
		},
		{
			name:     "tenant-b",
			attrs:    map[string]string{"src": dir, "tenant": "team-b", "tag": "v1"},
			expected: tenantBDesc.Digest,
		},
		{
			name:     "explicit-digest-wins",
			attrs:    map[string]string{"src": dir, "tenant": "team-a", "tag": "v1", "digest": tenantBDesc.Digest.String()},
			expected: tenantBDesc.Digest,
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			opts, err := parseCacheOptions(ctx, false, SolveOpt{
				CacheImports: []CacheOptionsEntry{{Type: "local", Attrs: tc.attrs}},
			})
			require.NoError(t, err)
			require.Len(t, opts.options.Imports, 1)
			require.Equal(t, tc.expected.String(), opts.options.Imports[0].Attrs["digest"])
		})
	}
}

func TestLocalCacheImportRejectsInvalidDigest(t *testing.T) {
	_, err := parseCacheOptions(t.Context(), false, SolveOpt{
		CacheImports: []CacheOptionsEntry{{
			Type:  "local",
			Attrs: map[string]string{"src": t.TempDir(), "digest": "sha256:not-a-digest"},
		}},
	})
	require.ErrorContains(t, err, "invalid local cache digest")
}

func TestLocalCacheTenantExportTag(t *testing.T) {
	dir := t.TempDir()
	attrsA := map[string]string{"dest": dir, "tenant": "team-a", "tag": "v1"}
	attrsB := map[string]string{"dest": dir, "tenant": "team-b", "tag": "v1"}
	opts, err := parseCacheOptions(t.Context(), false, SolveOpt{
		CacheExports: []CacheOptionsEntry{
			{Type: "local", Attrs: attrsA},
			{Type: "local", Attrs: attrsB},
		},
	})
	require.NoError(t, err)

	expectedA, err := localCacheTag(attrsA)
	require.NoError(t, err)
	expectedB, err := localCacheTag(attrsB)
	require.NoError(t, err)
	require.Equal(t, []cacheStoreUpdate{
		{tag: expectedA},
		{tag: expectedB},
	}, opts.storesToUpdate[dir])
}
