// Copyright Project BuildKit Authors
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

// Ported from BuildKit project, copyright The BuildKit Authors
// https://github.com/moby/buildkit/blob/40c0091bd0aef940a78b7aed050efc2f9416ee88/util/overlay/overlay_linux.go

package packer

import (
	"strings"

	"github.com/containerd/containerd/mount"
	"github.com/pkg/errors"
)

// GetUpperdir parses the passed mounts and identifies the directory
// that contains diff between upper and lower.
func GetUpperdir(lower, upper []mount.Mount) (string, error) {
	var upperdir string
	if len(lower) == 0 && len(upper) == 1 { // upper is the bottommost snapshot
		// Get layer directories of upper snapshot
		upperM := upper[0]
		if upperM.Type != "bind" {
			return "", errors.Errorf("bottommost upper must be bind mount but %q", upperM.Type)
		}
		upperdir = upperM.Source
	} else if len(lower) == 1 && len(upper) == 1 {
		// Get layer directories of lower snapshot
		var lowerlayers []string
		lowerM := lower[0]
		switch lowerM.Type {
		case "bind":
			// lower snapshot is a bind mount of one layer
			lowerlayers = []string{lowerM.Source}
		case "overlay":
			// lower snapshot is an overlay mount of multiple layers
			var err error
			lowerlayers, err = GetOverlayLayers(lowerM)
			if err != nil {
				return "", err
			}
		default:
			return "", errors.Errorf("cannot get layer information from mount option (type = %q)", lowerM.Type)
		}

		// Get layer directories of upper snapshot
		upperM := upper[0]
		if upperM.Type != "overlay" {
			return "", errors.Errorf("upper snapshot isn't overlay mounted (type = %q)", upperM.Type)
		}
		upperlayers, err := GetOverlayLayers(upperM)
		if err != nil {
			return "", err
		}

		// Check if the diff directory can be determined
		if len(upperlayers) != len(lowerlayers)+1 {
			return "", errors.Errorf("cannot determine diff of more than one upper directories")
		}
		for i := 0; i < len(lowerlayers); i++ {
			if upperlayers[i] != lowerlayers[i] {
				return "", errors.Errorf("layer %d must be common between upper and lower snapshots", i)
			}
		}
		upperdir = upperlayers[len(upperlayers)-1] // get the topmost layer that indicates diff
	} else {
		return "", errors.Errorf("multiple mount configurations are not supported")
	}
	if upperdir == "" {
		return "", errors.Errorf("cannot determine upperdir from mount option")
	}
	return upperdir, nil
}

// GetOverlayLayers returns all layer directories of an overlayfs mount.
func GetOverlayLayers(m mount.Mount) ([]string, error) {
	var u string
	var uFound bool
	var l []string // l[0] = bottommost
	for _, o := range m.Options {
		if strings.HasPrefix(o, "upperdir=") {
			u, uFound = strings.TrimPrefix(o, "upperdir="), true
		} else if strings.HasPrefix(o, "lowerdir=") {
			l = strings.Split(strings.TrimPrefix(o, "lowerdir="), ":")
			for i, j := 0, len(l)-1; i < j; i, j = i+1, j-1 {
				l[i], l[j] = l[j], l[i] // make l[0] = bottommost
			}
		} else if strings.HasPrefix(o, "workdir=") || o == "index=off" || o == "userxattr" || strings.HasPrefix(o, "redirect_dir=") {
			// these options are possible to specified by the snapshotter but not indicate dir locations.
			continue
		} else {
			// encountering an unknown option. return error and fallback to walking differ
			// to avoid unexpected diff.
			return nil, errors.Errorf("unknown option %q specified by snapshotter", o)
		}
	}
	if uFound {
		return append(l, u), nil
	}
	return l, nil
}
