/*
Copyright 2014 The Kubernetes Authors.

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

package config

import (
	"crypto/md5"
	"encoding/hex"
	"fmt"
	"strings"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	api "k8s.io/kubernetes/pkg/apis/core"
	"k8s.io/kubernetes/pkg/apis/core/helper"

	// Ensure that core apis are installed
	_ "k8s.io/kubernetes/pkg/apis/core/install"
	kubetypes "k8s.io/kubernetes/pkg/kubelet/types"
	"k8s.io/kubernetes/pkg/util/hash"

	"k8s.io/klog/v2"
)

const (
	maxConfigLength = 10 * 1 << 20 // 10MB
)

// Generate a pod name that is unique among nodes by appending the nodeName.
func generatePodName(name string, nodeName types.NodeName) string {
	return fmt.Sprintf("%s-%s", name, strings.ToLower(string(nodeName)))
}

func applyDefaults(pod *api.Pod, source string, isFile bool, nodeName types.NodeName) error {
	if len(pod.UID) == 0 {
		hasher := md5.New()
		hash.DeepHashObject(hasher, pod)
		// DeepHashObject resets the hash, so we should write the pod source
		// information AFTER it.
		if isFile {
			fmt.Fprintf(hasher, "host:%s", nodeName)
			fmt.Fprintf(hasher, "file:%s", source)
		} else {
			fmt.Fprintf(hasher, "url:%s", source)
		}
		pod.UID = types.UID(hex.EncodeToString(hasher.Sum(nil)[0:]))
		klog.V(5).InfoS("Generated UID", "pod", klog.KObj(pod), "podUID", pod.UID, "source", source)
	}

	pod.Name = generatePodName(pod.Name, nodeName)
	klog.V(5).InfoS("Generated pod name", "pod", klog.KObj(pod), "podUID", pod.UID, "source", source)

	if pod.Namespace == "" {
		pod.Namespace = metav1.NamespaceDefault
	}
	klog.V(5).InfoS("Set namespace for pod", "pod", klog.KObj(pod), "source", source)

	// Set the Host field to indicate this pod is scheduled on the current node.
	pod.Spec.NodeName = string(nodeName)

	pod.ObjectMeta.SelfLink = getSelfLink(pod.Name, pod.Namespace)

	if pod.Annotations == nil {
		pod.Annotations = make(map[string]string)
	}
	// The generated UID is the hash of the file.
	pod.Annotations[kubetypes.ConfigHashAnnotationKey] = string(pod.UID)

	if isFile {
		// Applying the default Taint tolerations to static pods,
		// so they are not evicted when there are node problems.
		helper.AddOrUpdateTolerationInPod(pod, &api.Toleration{
			Operator: "Exists",
			Effect:   api.TaintEffectNoExecute,
		})
	}

	// Set the default status to pending.
	pod.Status.Phase = api.PodPending
	return nil
}

func getSelfLink(name, namespace string) string {
	var selfLink string
	if len(namespace) == 0 {
		namespace = metav1.NamespaceDefault
	}
	selfLink = fmt.Sprintf("/api/v1/namespaces/%s/pods/%s", namespace, name)
	return selfLink
}

type defaultFunc func(pod *api.Pod) error
