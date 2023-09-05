package mycore

import (
	"fmt"
	v1 "k8s.io/api/core/v1"
	"k8s.io/cri-api/pkg/apis/runtime/v1alpha2"
	"k8s.io/kubernetes/pkg/kubelet/container"
	"time"
)

// SetPodStatus 设置pod状态
func SetPodStatus(pod *v1.Pod, state container.State) *container.PodStatus {

	// pod status
	status := &container.PodStatus{
		ID:        pod.UID,
		Name:      pod.Name,
		Namespace: pod.Namespace,
		SandboxStatuses: []*v1alpha2.PodSandboxStatus{
			{
				Id:    string(pod.UID),
				State: v1alpha2.PodSandboxState_SANDBOX_READY,
			},
		},
	}

	// container status
	containerStatus := make([]*container.Status, 0)
	for _, c := range pod.Spec.Containers {
		cs := &container.Status{
			Name:      c.Name,
			Image:     c.Image,
			State:     state,
			CreatedAt: time.Now(),
			StartedAt: time.Now().Add(time.Second * 3),
		}
		containerStatus = append(containerStatus, cs)
	}

	status.ContainerStatuses = containerStatus
	return status
}

// SetPodCompleted 设置Pod完成
func SetPodCompleted(pod *v1.Pod) *container.PodStatus {
	// pod status
	status := &container.PodStatus{
		ID:        pod.UID,
		Name:      pod.Name,
		Namespace: pod.Namespace,
		SandboxStatuses: []*v1alpha2.PodSandboxStatus{
			{
				Id:    string(pod.UID),
				State: v1alpha2.PodSandboxState_SANDBOX_NOTREADY,
			},
		},
	}

	// container status
	containerStatus := make([]*container.Status, 0)
	for _, c := range pod.Spec.Containers {
		cs := &container.Status{
			Name:       c.Name,
			Image:      c.Image,
			State:      container.ContainerStateExited,
			ExitCode:   0,
			Reason:     "Completed",
			FinishedAt: time.Now(),
		}
		containerStatus = append(containerStatus, cs)
	}
	status.ContainerStatuses = containerStatus
	return status
}

// SetPodTerminated 设置Pod停止
func SetPodTerminated(pod *v1.Pod) *container.PodStatus {

	// pod status
	status := &container.PodStatus{
		ID:        pod.UID,
		Name:      pod.Name,
		Namespace: pod.Namespace,
		SandboxStatuses: []*v1alpha2.PodSandboxStatus{
			{
				Id:    string(pod.UID),
				State: v1alpha2.PodSandboxState_SANDBOX_READY,
			},
		},
	}

	// container status
	containerStatus := make([]*container.Status, 0)
	for _, c := range pod.Spec.Containers {
		cs := &container.Status{
			Name:       c.Name,
			Image:      c.Image,
			State:      container.ContainerStateExited,
			ExitCode:   0,
			Reason:     "Terminated",
			FinishedAt: time.Now(),
		}
		containerStatus = append(containerStatus, cs)
	}
	status.ContainerStatuses = containerStatus
	return status
}

// SetContainerRunning 设置container状态为running
func SetContainerRunning(ps *container.PodStatus, containerName string) *container.PodStatus {
	for i, _ := range ps.SandboxStatuses {
		ps.SandboxStatuses[i].State = v1alpha2.PodSandboxState_SANDBOX_READY
	}
	containerStatus := ps.ContainerStatuses
	for i, c := range containerStatus {
		if c.Name == containerName {
			fmt.Println("set container running", containerName)
			ps.ContainerStatuses[i].State = container.ContainerStateRunning
			ps.ContainerStatuses[i].StartedAt = time.Now()
		}
	}
	return ps
}

// SetContainerExit 设置container状态为exit
func SetContainerExit(ps *container.PodStatus, pod *v1.Pod, containerName string, exitCode int) *container.PodStatus {

	// running 不设置  默认就是running
	var podState v1alpha2.PodSandboxState
	if len(pod.Spec.Containers) == 1 {
		podState = v1alpha2.PodSandboxState_SANDBOX_NOTREADY
	} else {
		podState = v1alpha2.PodSandboxState_SANDBOX_READY
	}
	for i, _ := range ps.SandboxStatuses {
		ps.SandboxStatuses[i].State = podState // 重新设置
	}

	containerStatus := ps.ContainerStatuses
	for i, c := range containerStatus {
		if c.Name == containerName {
			reason := "Error"
			if exitCode == 0 {
				reason = "Completed"
			}
			ps.ContainerStatuses[i].State = container.ContainerStateExited
			ps.ContainerStatuses[i].ExitCode = exitCode
			ps.ContainerStatuses[i].Reason = reason
			ps.ContainerStatuses[i].FinishedAt = time.Now()
		}
	}

	//for _, c := range pod.Spec.Containers {
	//	if c.Name == containerName {
	//		reason := "Error"
	//		if exitCode == 0 {
	//			reason = "Completed"
	//		}
	//		cs := &container.Status{
	//			Name:       c.Name,
	//			Image:      c.Image,
	//			State:      container.ContainerStateExited,
	//			ExitCode:   exitCode,
	//			Reason:     reason,
	//			FinishedAt: time.Now(),
	//		}
	//		container_status = append(container_status, cs)
	//	} else {
	//		cs := &container.Status{
	//			Name:  c.Name,
	//			Image: c.Image,
	//			State: container.ContainerStateRunning,
	//		}
	//		container_status = append(container_status, cs)
	//	}
	//}
	return ps
}
