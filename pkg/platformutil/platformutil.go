package platformutil

import (
	"fmt"
	"strings"

	"github.com/containerd/containerd/platforms"
	nydusUtils "github.com/goharbor/acceleration-service/pkg/driver/nydus/utils"
	ocispec "github.com/opencontainers/image-spec/specs-go/v1"
)

// Ported from nerdctl project, copyright The containerd Authors.
// https://github.com/containerd/nerdctl/blob/0f1c52a2d5c76b49789699d0a0018a99e4c1cff8/pkg/strutil/strutil.go#L72
func dedupeStrSlice(in []string) []string {
	m := make(map[string]struct{})
	var res []string
	for _, s := range in {
		if _, ok := m[s]; !ok {
			res = append(res, s)
			m[s] = struct{}{}
		}
	}
	return res
}

// Parse platform strings into MatchComparer, ss is multiple platforms split by ",",
// For example: linux/amd64,linux/arm64
func ParsePlatforms(all bool, ss string) (platforms.MatchComparer, error) {
	platforms := []string{}
	for _, plat := range strings.Split(ss, ",") {
		plat := strings.TrimSpace(plat)
		if plat != "" {
			platforms = append(platforms, plat)
		}
	}
	platformMC, err := NewMatchComparer(all, platforms)
	if err != nil {
		return nil, err
	}
	return nydusUtils.ExcludeNydusPlatformComparer{MatchComparer: platformMC}, nil
}

// Ported from nerdctl project, copyright The containerd Authors.
// https://github.com/containerd/nerdctl/blob/0f1c52a2d5c76b49789699d0a0018a99e4c1cff8/pkg/platformutil/platformutil.go#L40
//
// NewMatchComparer returns MatchComparer.
// If all is true, NewMatchComparer always returns All, regardless to the value of ss.
// If all is false and ss is empty, NewMatchComparer returns DefaultStrict (not Default).
// Otherwise NewMatchComparer returns Ordered MatchComparer.
func NewMatchComparer(all bool, ss []string) (platforms.MatchComparer, error) {
	if all {
		return platforms.All, nil
	}
	if len(ss) == 0 {
		// return DefaultStrict, not Default
		return platforms.DefaultStrict(), nil
	}
	op, err := NewOCISpecPlatformSlice(false, ss)
	return platforms.Ordered(op...), err
}

// Ported from nerdctl project, copyright The containerd Authors.
// https://github.com/containerd/nerdctl/blob/0f1c52a2d5c76b49789699d0a0018a99e4c1cff8/pkg/platformutil/platformutil.go#L56
//
// NewOCISpecPlatformSlice returns a slice of ocispec.Platform
// If all is true, NewOCISpecPlatformSlice always returns an empty slice, regardless to the value of ss.
// If all is false and ss is empty, NewOCISpecPlatformSlice returns DefaultSpec.
// Otherwise NewOCISpecPlatformSlice returns the slice that correspond to ss.
func NewOCISpecPlatformSlice(all bool, ss []string) ([]ocispec.Platform, error) {
	if all {
		return nil, nil
	}
	if dss := dedupeStrSlice(ss); len(dss) > 0 {
		var op []ocispec.Platform
		for _, s := range dss {
			p, err := platforms.Parse(s)
			if err != nil {
				return nil, fmt.Errorf("invalid platform: %q", s)
			}
			op = append(op, p)
		}
		return op, nil
	}
	return []ocispec.Platform{platforms.DefaultSpec()}, nil
}
