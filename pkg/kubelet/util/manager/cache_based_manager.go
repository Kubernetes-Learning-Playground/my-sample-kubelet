/*
Copyright 2018 The Kubernetes Authors.

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

    http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software
distributed under the License is distributed on an "AS IS" BASIS,
WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
See the License for the specific language governing permissions and
limitations under the License.
*/

package manager

import (
	"strconv"
	"sync"
	"time"

	"k8s.io/api/core/v1"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/apimachinery/pkg/util/sets"
	"k8s.io/utils/clock"
)

// GetObjectTTLFunc defines a function to get value of TTL.
type GetObjectTTLFunc func() (time.Duration, bool)

// GetObjectFunc defines a function to get object with a given namespace and name.
type GetObjectFunc func(string, string, metav1.GetOptions) (runtime.Object, error)

type objectKey struct {
	namespace string
	name      string
	uid       types.UID
}

// objectStoreItems is a single item stored in objectStore.
type objectStoreItem struct {
	refCount int
	data     *objectData
}

type objectData struct {
	sync.Mutex

	object         runtime.Object
	err            error
	lastUpdateTime time.Time
}

// objectStore is a local cache of objects.
type objectStore struct {
	getObject GetObjectFunc
	clock     clock.Clock

	lock  sync.Mutex
	items map[objectKey]*objectStoreItem

	defaultTTL time.Duration
	getTTL     GetObjectTTLFunc
}

// NewObjectStore returns a new ttl-based instance of Store interface.
func NewObjectStore(getObject GetObjectFunc, clock clock.Clock, getTTL GetObjectTTLFunc, ttl time.Duration) Store {
	return &objectStore{
		getObject:  getObject,
		clock:      clock,
		items:      make(map[objectKey]*objectStoreItem),
		defaultTTL: ttl,
		getTTL:     getTTL,
	}
}

func (s *objectStore) AddReference(namespace, name string) {
	key := objectKey{namespace: namespace, name: name}

	// AddReference is called from RegisterPod, thus it needs to be efficient.
	// Thus Add() is only increasing refCount and generation of a given object.
	// Then Get() is responsible for fetching if needed.
	s.lock.Lock()
	defer s.lock.Unlock()
	item, exists := s.items[key]
	if !exists {
		item = &objectStoreItem{
			refCount: 0,
			data:     &objectData{},
		}
		s.items[key] = item
	}

	item.refCount++
	// This will trigger fetch on the next Get() operation.
	item.data = nil
}

func (s *objectStore) DeleteReference(namespace, name string) {
	key := objectKey{namespace: namespace, name: name}

	s.lock.Lock()
	defer s.lock.Unlock()
	if item, ok := s.items[key]; ok {
		item.refCount--
		if item.refCount == 0 {
			delete(s.items, key)
		}
	}
}

// GetObjectTTLFromNodeFunc returns a function that returns TTL value
// from a given Node object.
func GetObjectTTLFromNodeFunc(getNode func() (*v1.Node, error)) GetObjectTTLFunc {
	return func() (time.Duration, bool) {
		node, err := getNode()
		if err != nil {
			return time.Duration(0), false
		}
		if node != nil && node.Annotations != nil {
			if value, ok := node.Annotations[v1.ObjectTTLAnnotationKey]; ok {
				if intValue, err := strconv.Atoi(value); err == nil {
					return time.Duration(intValue) * time.Second, true
				}
			}
		}
		return time.Duration(0), false
	}
}

func (s *objectStore) isObjectFresh(data *objectData) bool {
	objectTTL := s.defaultTTL
	if ttl, ok := s.getTTL(); ok {
		objectTTL = ttl
	}
	return s.clock.Now().Before(data.lastUpdateTime.Add(objectTTL))
}

func (s *objectStore) Get(namespace, name string) (runtime.Object, error) {
	return nil, nil
}

// cacheBasedManager keeps a store with objects necessary
// for registered pods. Different implementations of the store
// may result in different semantics for freshness of objects
// (e.g. ttl-based implementation vs watch-based implementation).
type cacheBasedManager struct {
	objectStore          Store
	getReferencedObjects func(*v1.Pod) sets.String

	lock           sync.Mutex
	registeredPods map[objectKey]*v1.Pod
}

func (c *cacheBasedManager) GetObject(namespace, name string) (runtime.Object, error) {
	return c.objectStore.Get(namespace, name)
}

func (c *cacheBasedManager) RegisterPod(pod *v1.Pod) {
	names := c.getReferencedObjects(pod)
	c.lock.Lock()
	defer c.lock.Unlock()
	for name := range names {
		c.objectStore.AddReference(pod.Namespace, name)
	}
	var prev *v1.Pod
	key := objectKey{namespace: pod.Namespace, name: pod.Name, uid: pod.UID}
	prev = c.registeredPods[key]
	c.registeredPods[key] = pod
	if prev != nil {
		for name := range c.getReferencedObjects(prev) {
			// On an update, the .Add() call above will have re-incremented the
			// ref count of any existing object, so any objects that are in both
			// names and prev need to have their ref counts decremented. Any that
			// are only in prev need to be completely removed. This unconditional
			// call takes care of both cases.
			c.objectStore.DeleteReference(prev.Namespace, name)
		}
	}
}

func (c *cacheBasedManager) UnregisterPod(pod *v1.Pod) {
	var prev *v1.Pod
	key := objectKey{namespace: pod.Namespace, name: pod.Name, uid: pod.UID}
	c.lock.Lock()
	defer c.lock.Unlock()
	prev = c.registeredPods[key]
	delete(c.registeredPods, key)
	if prev != nil {
		for name := range c.getReferencedObjects(prev) {
			c.objectStore.DeleteReference(prev.Namespace, name)
		}
	}
}

// NewCacheBasedManager creates a manager that keeps a cache of all objects
// necessary for registered pods.
// It implements the following logic:
//   - whenever a pod is created or updated, the cached versions of all objects
//     is referencing are invalidated
//   - every GetObject() call tries to fetch the value from local cache; if it is
//     not there, invalidated or too old, we fetch it from apiserver and refresh the
//     value in cache; otherwise it is just fetched from cache
func NewCacheBasedManager(objectStore Store, getReferencedObjects func(*v1.Pod) sets.String) Manager {
	return &cacheBasedManager{
		objectStore:          objectStore,
		getReferencedObjects: getReferencedObjects,
		registeredPods:       make(map[objectKey]*v1.Pod),
	}
}
