/*
Copyright 2015 The Kubernetes Authors.

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

package container

import (
	"fmt"

	"k8s.io/klog/v2"

	v1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/types"
	runtimeapi "k8s.io/cri-api/pkg/apis/runtime/v1alpha2"
	podutil "k8s.io/kubernetes/pkg/api/v1/pod"

	utilsnet "k8s.io/utils/net"
)

// HandlerRunner runs a lifecycle handler for a container.

// RuntimeHelper wraps kubelet to make container runtime
// able to get necessary informations like the RunContainerOptions, DNS settings, Host IP.
type RuntimeHelper interface {
	GenerateRunContainerOptions(pod *v1.Pod, container *v1.Container, podIP string, podIPs []string) (contOpts *RunContainerOptions, cleanupAction func(), err error)
	GetPodDNS(pod *v1.Pod) (dnsConfig *runtimeapi.DNSConfig, err error)
	// GetPodCgroupParent returns the CgroupName identifier, and its literal cgroupfs form on the host
	// of a pod.
	GetPodCgroupParent(pod *v1.Pod) string
	GetPodDir(podUID types.UID) string
	GeneratePodHostNameAndDomain(pod *v1.Pod) (hostname string, hostDomain string, err error)
	// GetExtraSupplementalGroupsForPod returns a list of the extra
	// supplemental groups for the Pod. These extra supplemental groups come
	// from annotations on persistent volumes that the pod depends on.
	GetExtraSupplementalGroupsForPod(pod *v1.Pod) []int64
}

// ShouldContainerBeRestarted checks whether a container needs to be restarted.
// TODO(yifan): Think about how to refactor this.
func ShouldContainerBeRestarted(container *v1.Container, pod *v1.Pod, podStatus *PodStatus) bool {
	// Once a pod has been marked deleted, it should not be restarted
	if pod.DeletionTimestamp != nil {
		return false
	}
	// Get latest container status.
	status := podStatus.FindContainerStatusByName(container.Name)
	// If the container was never started before, we should start it.
	// NOTE(random-liu): If all historical containers were GC'd, we'll also return true here.
	if status == nil {
		return true
	}
	// Check whether container is running
	if status.State == ContainerStateRunning {
		return false
	}
	// Always restart container in the unknown, or in the created state.
	if status.State == ContainerStateUnknown || status.State == ContainerStateCreated {
		return true
	}
	// Check RestartPolicy for dead container
	if pod.Spec.RestartPolicy == v1.RestartPolicyNever {
		klog.V(4).InfoS("Already ran container, do nothing", "pod", klog.KObj(pod), "containerName", container.Name)
		return false
	}
	if pod.Spec.RestartPolicy == v1.RestartPolicyOnFailure {
		// Check the exit code.
		if status.ExitCode == 0 {
			klog.V(4).InfoS("Already successfully ran container, do nothing", "pod", klog.KObj(pod), "containerName", container.Name)
			return false
		}
	}
	return true
}

// envVarsToMap constructs a map of environment name to value from a slice
// of env vars.
func envVarsToMap(envs []EnvVar) map[string]string {
	result := map[string]string{}
	for _, env := range envs {
		result[env.Name] = env.Value
	}
	return result
}

// v1EnvVarsToMap constructs a map of environment name to value from a slice
// of env vars.
func v1EnvVarsToMap(envs []v1.EnvVar) map[string]string {
	result := map[string]string{}
	for _, env := range envs {
		result[env.Name] = env.Value
	}

	return result
}

// IsHostNetworkPod returns whether the host networking requested for the given Pod.
// Pod must not be nil.
func IsHostNetworkPod(pod *v1.Pod) bool {
	return pod.Spec.HostNetwork
}

// ConvertPodStatusToRunningPod returns Pod given PodStatus and container runtime string.
// TODO(random-liu): Convert PodStatus to running Pod, should be deprecated soon
func ConvertPodStatusToRunningPod(runtimeName string, podStatus *PodStatus) Pod {
	runningPod := Pod{
		ID:        podStatus.ID,
		Name:      podStatus.Name,
		Namespace: podStatus.Namespace,
	}
	for _, containerStatus := range podStatus.ContainerStatuses {
		if containerStatus.State != ContainerStateRunning {
			continue
		}
		container := &Container{
			ID:      containerStatus.ID,
			Name:    containerStatus.Name,
			Image:   containerStatus.Image,
			ImageID: containerStatus.ImageID,
			Hash:    containerStatus.Hash,
			State:   containerStatus.State,
		}
		runningPod.Containers = append(runningPod.Containers, container)
	}

	// Populate sandboxes in kubecontainer.Pod
	for _, sandbox := range podStatus.SandboxStatuses {
		runningPod.Sandboxes = append(runningPod.Sandboxes, &Container{
			ID:    ContainerID{Type: runtimeName, ID: sandbox.Id},
			State: SandboxToContainerState(sandbox.State),
		})
	}
	return runningPod
}

// SandboxToContainerState converts runtimeapi.PodSandboxState to
// kubecontainer.State.
// This is only needed because we need to return sandboxes as if they were
// kubecontainer.Containers to avoid substantial changes to PLEG.
// TODO: Remove this once it becomes obsolete.
func SandboxToContainerState(state runtimeapi.PodSandboxState) State {
	switch state {
	case runtimeapi.PodSandboxState_SANDBOX_READY:
		return ContainerStateRunning
	case runtimeapi.PodSandboxState_SANDBOX_NOTREADY:
		return ContainerStateExited
	}
	return ContainerStateUnknown
}

// FormatPod returns a string representing a pod in a human readable format,
// with pod UID as part of the string.
func FormatPod(pod *Pod) string {
	// Use underscore as the delimiter because it is not allowed in pod name
	// (DNS subdomain format), while allowed in the container name format.
	return fmt.Sprintf("%s_%s(%s)", pod.Name, pod.Namespace, pod.ID)
}

// GetContainerSpec gets the container spec by containerName.
func GetContainerSpec(pod *v1.Pod, containerName string) *v1.Container {
	var containerSpec *v1.Container
	podutil.VisitContainers(&pod.Spec, podutil.AllFeatureEnabledContainers(), func(c *v1.Container, containerType podutil.ContainerType) bool {
		if containerName == c.Name {
			containerSpec = c
			return false
		}
		return true
	})
	return containerSpec
}

// HasPrivilegedContainer returns true if any of the containers in the pod are privileged.
func HasPrivilegedContainer(pod *v1.Pod) bool {
	var hasPrivileged bool
	podutil.VisitContainers(&pod.Spec, podutil.AllFeatureEnabledContainers(), func(c *v1.Container, containerType podutil.ContainerType) bool {
		if c.SecurityContext != nil && c.SecurityContext.Privileged != nil && *c.SecurityContext.Privileged {
			hasPrivileged = true
			return false
		}
		return true
	})
	return hasPrivileged
}

// HasWindowsHostProcessContainer returns true if any of the containers in a pod are HostProcess containers.
func HasWindowsHostProcessContainer(pod *v1.Pod) bool {
	var hasHostProcess bool
	podutil.VisitContainers(&pod.Spec, podutil.AllFeatureEnabledContainers(), func(c *v1.Container, containerType podutil.ContainerType) bool {

		return true
	})

	return hasHostProcess
}

// AllContainersAreWindowsHostProcess returns true if all containres in a pod are HostProcess containers.
func AllContainersAreWindowsHostProcess(pod *v1.Pod) bool {
	allHostProcess := true
	podutil.VisitContainers(&pod.Spec, podutil.AllFeatureEnabledContainers(), func(c *v1.Container, containerType podutil.ContainerType) bool {

		return true
	})

	return allHostProcess
}

// MakePortMappings creates internal port mapping from api port mapping.
func MakePortMappings(container *v1.Container) (ports []PortMapping) {
	names := make(map[string]struct{})
	for _, p := range container.Ports {
		pm := PortMapping{
			HostPort:      int(p.HostPort),
			ContainerPort: int(p.ContainerPort),
			Protocol:      p.Protocol,
			HostIP:        p.HostIP,
		}

		// We need to determine the address family this entry applies to. We do this to ensure
		// duplicate containerPort / protocol rules work across different address families.
		// https://github.com/kubernetes/kubernetes/issues/82373
		family := "any"
		if p.HostIP != "" {
			if utilsnet.IsIPv6String(p.HostIP) {
				family = "v6"
			} else {
				family = "v4"
			}
		}

		var name string = p.Name
		if name == "" {
			name = fmt.Sprintf("%s-%s-%s:%d:%d", family, p.Protocol, p.HostIP, p.ContainerPort, p.HostPort)
		}

		// Protect against a port name being used more than once in a container.
		if _, ok := names[name]; ok {
			klog.InfoS("Port name conflicted, it is defined more than once", "portName", name)
			continue
		}
		ports = append(ports, pm)
		names[name] = struct{}{}
	}
	return
}
