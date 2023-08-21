// Copyright Project Harbor Authors
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//	http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.

package content

import (
	"context"
	"fmt"
	"math"
	"sort"
	"strconv"

	"github.com/containerd/containerd/leases"
	"github.com/containerd/containerd/namespaces"
	lru "github.com/hashicorp/golang-lru/v2"
)

// This is not thread-safe, which means it will depend on the parent implementation to do the locking mechanism.
type leaseCache struct {
	caches      map[string]*lru.Cache[string, any]
	cachesIndex []int
	size        int
}

// newleaseCache return new empty leaseCache
func newLeaseCache() *leaseCache {
	return &leaseCache{
		caches:      make(map[string]*lru.Cache[string, any]),
		cachesIndex: make([]int, 0),
		size:        0,
	}
}

// Init leaseCache by leases manager from db
func (leaseCache *leaseCache) Init(lm leases.Manager) error {
	ls, err := lm.List(namespaces.WithNamespace(context.Background(), accelerationServiceNamespace))
	if err != nil {
		return err
	}
	sort.Slice(ls, func(i, j int) bool {
		return ls[i].Labels[usedAtLabel] > ls[j].Labels[usedAtLabel]
	})
	for _, lease := range ls {
		if err := leaseCache.Add(lease.ID, lease.Labels[usedCountLabel]); err != nil {
			return err
		}
	}
	return nil
}

// Add the key into cache
func (leaseCache *leaseCache) Add(key string, usedCount string) error {
	count, err := strconv.Atoi(usedCount)
	if err != nil {
		return err
	}
	if cache, ok := leaseCache.caches[usedCount]; ok {
		cache.Add(key, nil)
	} else {
		cache, err := lru.New[string, any](math.MaxInt)
		if err != nil {
			return err
		}
		cache.Add(key, nil)
		leaseCache.caches[usedCount] = cache
		usedCount, err := strconv.Atoi(usedCount)
		if err != nil {
			return err
		}
		leaseCache.cachesIndex = append(leaseCache.cachesIndex, usedCount)
		sort.Ints(leaseCache.cachesIndex)
	}
	// remove old cache
	if usedCount != "1" {
		if cache, ok := leaseCache.caches[strconv.Itoa(count-1)]; ok {
			if cache.Contains(key) {
				leaseCache.remove(key, strconv.Itoa(count-1))
				leaseCache.size--
			}
		}
	}
	leaseCache.size++
	return nil
}

// Remove oldest key from cache
func (leaseCache *leaseCache) Remove() (string, error) {
	if key, _, ok := leaseCache.caches[strconv.Itoa(leaseCache.cachesIndex[0])].GetOldest(); ok {
		leaseCache.remove(key, strconv.Itoa(leaseCache.cachesIndex[0]))
		leaseCache.size--
		return key, nil
	}
	return "", fmt.Errorf("leaseCache have empty cache with cachesIndex")
}

func (leaseCache *leaseCache) remove(key string, usedCount string) {
	leaseCache.caches[usedCount].Remove(key)
	if leaseCache.caches[usedCount].Len() == 0 {
		delete(leaseCache.caches, usedCount)
		var newCachesIndex []int
		for _, index := range leaseCache.cachesIndex {
			if usedCount != strconv.Itoa(index) {
				newCachesIndex = append(newCachesIndex, index)
			}
		}
		leaseCache.cachesIndex = newCachesIndex
	}
}

// Len return the size of leaseCache
func (leaseCache *leaseCache) Len() int {
	return leaseCache.size
}
