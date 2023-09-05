package mycore

import (
	"context"
	"fmt"
	v1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/tools/record"
	"k8s.io/klog/v2"
	podutil "k8s.io/kubernetes/pkg/api/v1/pod"
	kubecontainer "k8s.io/kubernetes/pkg/kubelet/container"
	"k8s.io/kubernetes/pkg/kubelet/prober"
	"k8s.io/kubernetes/pkg/kubelet/prober/results"
	"k8s.io/kubernetes/pkg/kubelet/status"
	kubetypes "k8s.io/kubernetes/pkg/kubelet/types"
	"sort"
)


func SyncPodFn(ctx context.Context, updateType kubetypes.SyncPodType, pod *v1.Pod, mirrorPod *v1.Pod, podStatus *kubecontainer.PodStatus) (bool, error) {
	fmt.Println("临时的syncpod函数")

	return true, nil
}

func SyncTerminatingFn(ctx context.Context, pod *v1.Pod, podStatus *kubecontainer.PodStatus, runningPod *kubecontainer.Pod, gracePeriod *int64, podStatusFn func(*v1.PodStatus)) error {
	fmt.Println("临时的SyncTerminating函数")
	return nil
}

// the function to invoke to terminate a pod (ensure no running processes are present)
func SyncTerminatedFn(ctx context.Context, pod *v1.Pod, podStatus *kubecontainer.PodStatus) error {
	fmt.Println("临时的SyncTerminated函数")
	return nil
}

// PodFn
type PodFn struct {
	kubeClient    *kubernetes.Clientset
	statusManager status.Manager
	reasonCache   *ReasonCache
	recorder      record.EventRecorder
	probeManager  prober.Manager
}

func NewPodFn(client *kubernetes.Clientset, statusManager status.Manager, recorder record.EventRecorder) *PodFn {
	// 存活、就绪、启动探针管理器
	lm, rm, sm := results.NewManager(), results.NewManager(), results.NewManager()
	cmdRunner := &CmdRunner{}
	pm := prober.NewManager(statusManager, lm, rm, sm, cmdRunner, recorder)
	return &PodFn{
		kubeClient:    client,
		statusManager: statusManager,
		reasonCache:   NewReasonCache(),
		recorder:      recorder,
		probeManager:  pm,
	}
}

func (pf *PodFn) SyncTerminatingFn(_ context.Context, pod *v1.Pod, podStatus *kubecontainer.PodStatus, runningPod *kubecontainer.Pod, gracePeriod *int64, podStatusFn func(*v1.PodStatus)) error {
	fmt.Println("临时的SyncTerminating函数")
	fmt.Println("runningpod: ", pod.Name)
	pod_status := pf.generateAPIPodStatus(pod, podStatus)
	pf.statusManager.SetPodStatus(pod, pod_status)
	return nil
}

// the function to invoke to terminate a pod (ensure no running processes are present)
func (pf *PodFn) SyncTerminatedFn(ctx context.Context, pod *v1.Pod, podStatus *kubecontainer.PodStatus) error {
	fmt.Println("临时的SyncTerminated函数")
	pod_status := pf.generateAPIPodStatus(pod, podStatus)
	pf.statusManager.SetPodStatus(pod, pod_status)
	return nil
}

func (pf *PodFn) SyncPodFn(ctx context.Context, updateType kubetypes.SyncPodType, pod *v1.Pod, mirrorPod *v1.Pod, podStatus *kubecontainer.PodStatus) (bool, error) {
	fmt.Println("进入同步过程", updateType)
	pod_status := pf.generateAPIPodStatus(pod, podStatus)
	pf.statusManager.SetPodStatus(pod, pod_status)
	//pf.probeManager.AddPod(pod)
	//if updateType == kubetypes.SyncPodCreate || updateType == kubetypes.SyncPodUpdate {
	//	if pod.Name == "nginx-kubelet" {
	//		podStatus := pf.generateAPIPodStatus(pod, podStatus)
	//		pod.Status = podStatus
	//
	//		_, err := pf.kubeClient.CoreV1().Pods(pod.Namespace).
	//			UpdateStatus(ctx, pod, metav1.UpdateOptions{})
	//		if err != nil {
	//			log.Println("SyncPodFn:", err)
	//		} else {
	//			pf.statusManager.SetPodStatus(pod, podStatus)
	//		}
	//	}
	//}

	return true, nil
}

const (
	PodInitializing   = "PodInitializing"
	ContainerCreating = "ContainerCreating"
)

func (pf *PodFn) convertToAPIContainerStatuses(pod *v1.Pod, podStatus *kubecontainer.PodStatus, previousStatus []v1.ContainerStatus, containers []v1.Container, hasInitContainers, isInitContainer bool) []v1.ContainerStatus {
	convertContainerStatus := func(cs *kubecontainer.Status, oldStatus *v1.ContainerStatus) *v1.ContainerStatus {
		cid := cs.ID.String()
		status := &v1.ContainerStatus{
			Name:         cs.Name,
			RestartCount: int32(cs.RestartCount),
			Image:        cs.Image,
			ImageID:      cs.ImageID,
			ContainerID:  cid,
		}

		switch {
		case cs.State == kubecontainer.ContainerStateRunning:
			status.State.Running = &v1.ContainerStateRunning{StartedAt: metav1.NewTime(cs.StartedAt)}
		case cs.State == kubecontainer.ContainerStateCreated:
			// containers that are created but not running are "waiting to be running"
			status.State.Waiting = &v1.ContainerStateWaiting{}
		case cs.State == kubecontainer.ContainerStateExited:
			status.State.Terminated = &v1.ContainerStateTerminated{
				ExitCode:    int32(cs.ExitCode),
				Reason:      cs.Reason,
				Message:     cs.Message,
				StartedAt:   metav1.NewTime(cs.StartedAt),
				FinishedAt:  metav1.NewTime(cs.FinishedAt),
				ContainerID: cid,
			}

		case cs.State == kubecontainer.ContainerStateUnknown &&
			oldStatus != nil && // we have an old status
			oldStatus.State.Running != nil: // our previous status was running
			// if this happens, then we know that this container was previously running and isn't anymore (assuming the CRI isn't failing to return running containers).
			// you can imagine this happening in cases where a container failed and the kubelet didn't ask about it in time to see the result.
			// in this case, the container should not to into waiting state immediately because that can make cases like runonce pods actually run
			// twice. "container never ran" is different than "container ran and failed".  This is handled differently in the kubelet
			// and it is handled differently in higher order logic like crashloop detection and handling
			status.State.Terminated = &v1.ContainerStateTerminated{
				Reason:   "ContainerStatusUnknown",
				Message:  "The container could not be located when the pod was terminated",
				ExitCode: 137, // this code indicates an error
			}
			// the restart count normally comes from the CRI (see near the top of this method), but since this is being added explicitly
			// for the case where the CRI did not return a status, we need to manually increment the restart count to be accurate.
			status.RestartCount = oldStatus.RestartCount + 1

		default:
			// this collapses any unknown state to container waiting.  If any container is waiting, then the pod status moves to pending even if it is running.
			// if I'm reading this correctly, then any failure to read status on any container results in the entire pod going pending even if the containers
			// are actually running.
			// see https://github.com/kubernetes/kubernetes/blob/5d1b3e26af73dde33ecb6a3e69fb5876ceab192f/pkg/kubelet/kuberuntime/kuberuntime_container.go#L497 to
			// https://github.com/kubernetes/kubernetes/blob/8976e3620f8963e72084971d9d4decbd026bf49f/pkg/kubelet/kuberuntime/helpers.go#L58-L71
			// and interpreted here https://github.com/kubernetes/kubernetes/blob/b27e78f590a0d43e4a23ca3b2bf1739ca4c6e109/pkg/kubelet/kubelet_pods.go#L1434-L1439
			status.State.Waiting = &v1.ContainerStateWaiting{}
		}
		return status
	}

	// Fetch old containers statuses from old pod status.
	oldStatuses := make(map[string]v1.ContainerStatus, len(containers))
	for _, status := range previousStatus {
		oldStatuses[status.Name] = status
	}

	// Set all container statuses to default waiting state
	statuses := make(map[string]*v1.ContainerStatus, len(containers))
	defaultWaitingState := v1.ContainerState{Waiting: &v1.ContainerStateWaiting{Reason: ContainerCreating}}
	if hasInitContainers {
		defaultWaitingState = v1.ContainerState{Waiting: &v1.ContainerStateWaiting{Reason: PodInitializing}}
	}

	for _, container := range containers {
		status := &v1.ContainerStatus{
			Name:  container.Name,
			Image: container.Image,
			State: defaultWaitingState,
		}
		oldStatus, found := oldStatuses[container.Name]
		if found {
			if oldStatus.State.Terminated != nil {
				status = &oldStatus
			} else {
				// Apply some values from the old statuses as the default values.
				status.RestartCount = oldStatus.RestartCount
				status.LastTerminationState = oldStatus.LastTerminationState
			}
		}
		statuses[container.Name] = status
	}

	for _, container := range containers {
		found := false
		for _, cStatus := range podStatus.ContainerStatuses {
			if container.Name == cStatus.Name {
				found = true
				break
			}
		}
		if found {
			continue
		}
		// if no container is found, then assuming it should be waiting seems plausible, but the status code requires
		// that a previous termination be present.  If we're offline long enough or something removed the container, then
		// the previous termination may not be present.  This next code block ensures that if the container was previously running
		// then when that container status disappears, we can infer that it terminated even if we don't know the status code.
		// By setting the lasttermination state we are able to leave the container status waiting and present more accurate
		// data via the API.

		oldStatus, ok := oldStatuses[container.Name]
		if !ok {
			continue
		}
		if oldStatus.State.Terminated != nil {
			// if the old container status was terminated, the lasttermination status is correct
			continue
		}
		if oldStatus.State.Running == nil {
			// if the old container status isn't running, then waiting is an appropriate status and we have nothing to do
			continue
		}

		// If we're here, we know the pod was previously running, but doesn't have a terminated status. We will check now to
		// see if it's in a pending state.
		status := statuses[container.Name]
		// If the status we're about to write indicates the default, the Waiting status will force this pod back into Pending.
		// That isn't true, we know the pod was previously running.
		isDefaultWaitingStatus := status.State.Waiting != nil && status.State.Waiting.Reason == ContainerCreating
		if hasInitContainers {
			isDefaultWaitingStatus = status.State.Waiting != nil && status.State.Waiting.Reason == PodInitializing
		}
		if !isDefaultWaitingStatus {
			// the status was written, don't override
			continue
		}
		if status.LastTerminationState.Terminated != nil {
			// if we already have a termination state, nothing to do
			continue
		}

		// setting this value ensures that we show as stopped here, not as waiting:
		// https://github.com/kubernetes/kubernetes/blob/90c9f7b3e198e82a756a68ffeac978a00d606e55/pkg/kubelet/kubelet_pods.go#L1440-L1445
		// This prevents the pod from becoming pending
		status.LastTerminationState.Terminated = &v1.ContainerStateTerminated{
			Reason:   "ContainerStatusUnknown",
			Message:  "The container could not be located when the pod was deleted.  The container used to be Running",
			ExitCode: 137,
		}

		// If the pod was not deleted, then it's been restarted. Increment restart count.
		if pod.DeletionTimestamp == nil {
			status.RestartCount += 1
		}

		statuses[container.Name] = status
	}

	// Copy the slice before sorting it
	containerStatusesCopy := make([]*kubecontainer.Status, len(podStatus.ContainerStatuses))
	copy(containerStatusesCopy, podStatus.ContainerStatuses)

	// Make the latest container status comes first.
	sort.Sort(sort.Reverse(kubecontainer.SortContainerStatusesByCreationTime(containerStatusesCopy)))
	// Set container statuses according to the statuses seen in pod status
	containerSeen := map[string]int{}
	for _, cStatus := range containerStatusesCopy {
		cName := cStatus.Name
		if _, ok := statuses[cName]; !ok {
			// This would also ignore the infra container.
			continue
		}
		if containerSeen[cName] >= 2 {
			continue
		}
		var oldStatusPtr *v1.ContainerStatus
		if oldStatus, ok := oldStatuses[cName]; ok {
			oldStatusPtr = &oldStatus
		}
		status := convertContainerStatus(cStatus, oldStatusPtr)
		if containerSeen[cName] == 0 {
			statuses[cName] = status
		} else {
			statuses[cName].LastTerminationState = status.State
		}
		containerSeen[cName] = containerSeen[cName] + 1
	}

	// Handle the containers failed to be started, which should be in Waiting state.
	for _, container := range containers {
		if isInitContainer {
			// If the init container is terminated with exit code 0, it won't be restarted.
			// TODO(random-liu): Handle this in a cleaner way.
			s := podStatus.FindContainerStatusByName(container.Name)
			if s != nil && s.State == kubecontainer.ContainerStateExited && s.ExitCode == 0 {
				continue
			}
		}
		// If a container should be restarted in next syncpod, it is *Waiting*.
		if !kubecontainer.ShouldContainerBeRestarted(&container, pod, podStatus) {
			continue
		}
		status := statuses[container.Name]
		reason, ok := pf.reasonCache.Get(pod.UID, container.Name)
		if !ok {
			// In fact, we could also apply Waiting state here, but it is less informative,
			// and the container will be restarted soon, so we prefer the original state here.
			// Note that with the current implementation of ShouldContainerBeRestarted the original state here
			// could be:
			//   * Waiting: There is no associated historical container and start failure reason record.
			//   * Terminated: The container is terminated.
			continue
		}
		if status.State.Terminated != nil {
			status.LastTerminationState = status.State
		}
		status.State = v1.ContainerState{
			Waiting: &v1.ContainerStateWaiting{
				Reason:  reason.Err.Error(),
				Message: reason.Message,
			},
		}
		statuses[container.Name] = status
	}

	// Sort the container statuses since clients of this interface expect the list
	// of containers in a pod has a deterministic order.
	if isInitContainer {
		return kubetypes.SortStatusesOfInitContainers(pod, statuses)
	}
	containerStatuses := make([]v1.ContainerStatus, 0, len(statuses))
	for _, status := range statuses {
		containerStatuses = append(containerStatuses, *status)
	}

	sort.Sort(kubetypes.SortedContainerStatuses(containerStatuses))
	return containerStatuses
}

func (pf *PodFn) convertStatusToAPIStatus(pod *v1.Pod, podStatus *kubecontainer.PodStatus, oldPodStatus v1.PodStatus) *v1.PodStatus {
	var apiPodStatus v1.PodStatus

	// copy pod status IPs to avoid race conditions with PodStatus #102806
	podIPs := make([]string, len(podStatus.IPs))
	for j, ip := range podStatus.IPs {
		podIPs[j] = ip
	}

	apiPodStatus.ContainerStatuses = pf.convertToAPIContainerStatuses(
		pod, podStatus,
		oldPodStatus.ContainerStatuses,
		pod.Spec.Containers,
		len(pod.Spec.InitContainers) > 0,
		false,
	)
	apiPodStatus.InitContainerStatuses = pf.convertToAPIContainerStatuses(
		pod, podStatus,
		oldPodStatus.InitContainerStatuses,
		pod.Spec.InitContainers,
		len(pod.Spec.InitContainers) > 0,
		true,
	)

	return &apiPodStatus
}

func (pf *PodFn) generateAPIPodStatus(pod *v1.Pod, podStatus *kubecontainer.PodStatus) v1.PodStatus {
	klog.V(3).InfoS("Generating pod status", "pod", klog.KObj(pod))

	// use the previous pod status, or the api status, as the basis for this pod
	oldPodStatus, found := pf.statusManager.GetPodStatus(pod.UID)
	if !found {
		oldPodStatus = pod.Status
	}
	s := pf.convertStatusToAPIStatus(pod, podStatus, oldPodStatus)

	// calculate the next phase and preserve reason
	allStatus := append(append([]v1.ContainerStatus{}, s.ContainerStatuses...), s.InitContainerStatuses...)
	s.Phase = getPhase(&pod.Spec, allStatus)
	klog.V(4).InfoS("Got phase for pod", "pod", klog.KObj(pod), "oldPhase", oldPodStatus.Phase, "phase", s.Phase)

	// Perform a three-way merge between the statuses from the status manager,
	// runtime, and generated status to ensure terminal status is correctly set.
	if s.Phase != v1.PodFailed && s.Phase != v1.PodSucceeded {
		switch {
		case oldPodStatus.Phase == v1.PodFailed || oldPodStatus.Phase == v1.PodSucceeded:
			klog.V(4).InfoS("Status manager phase was terminal, updating phase to match", "pod", klog.KObj(pod), "phase", oldPodStatus.Phase)
			s.Phase = oldPodStatus.Phase
		case pod.Status.Phase == v1.PodFailed || pod.Status.Phase == v1.PodSucceeded:
			klog.V(4).InfoS("API phase was terminal, updating phase to match", "pod", klog.KObj(pod), "phase", pod.Status.Phase)
			s.Phase = pod.Status.Phase
		}
	}

	if s.Phase == oldPodStatus.Phase {
		// preserve the reason and message which is associated with the phase
		s.Reason = oldPodStatus.Reason
		s.Message = oldPodStatus.Message
		if len(s.Reason) == 0 {
			s.Reason = pod.Status.Reason
		}
		if len(s.Message) == 0 {
			s.Message = pod.Status.Message
		}
	}

	// check if an internal module has requested the pod is evicted and override the reason and message

	// pods are not allowed to transition out of terminal phases
	if pod.Status.Phase == v1.PodFailed || pod.Status.Phase == v1.PodSucceeded {
		// API server shows terminal phase; transitions are not allowed
		if s.Phase != pod.Status.Phase {
			klog.ErrorS(nil, "Pod attempted illegal phase transition", "pod", klog.KObj(pod), "originalStatusPhase", pod.Status.Phase, "apiStatusPhase", s.Phase, "apiStatus", s)
			// Force back to phase from the API server
			s.Phase = pod.Status.Phase
		}
	}

	// ensure the probe managers have up to date status for containers
	pf.probeManager.UpdatePodStatus(pod.UID, s)

	// preserve all conditions not owned by the kubelet
	s.Conditions = make([]v1.PodCondition, 0, len(pod.Status.Conditions)+1)
	for _, c := range pod.Status.Conditions {
		if !kubetypes.PodConditionByKubelet(c.Type) {
			s.Conditions = append(s.Conditions, c)
		}
	}

	// set all Kubelet-owned conditions
	s.Conditions = append(s.Conditions, status.GeneratePodInitializedCondition(&pod.Spec, s.InitContainerStatuses, s.Phase))
	s.Conditions = append(s.Conditions, status.GeneratePodReadyCondition(&pod.Spec, s.Conditions, s.ContainerStatuses, s.Phase))
	s.Conditions = append(s.Conditions, status.GenerateContainersReadyCondition(&pod.Spec, s.ContainerStatuses, s.Phase))
	s.Conditions = append(s.Conditions, v1.PodCondition{
		Type:   v1.PodScheduled,
		Status: v1.ConditionTrue,
	})

	// set HostIP and initialize PodIP/PodIPs for host network pods

	return *s
}

type PodDeletionSafetyProviderStruct struct {
}

func (p *PodDeletionSafetyProviderStruct) PodResourcesAreReclaimed(pod *v1.Pod, status v1.PodStatus) bool {
	//TODO implement me
	return true
}

func (p *PodDeletionSafetyProviderStruct) PodCouldHaveRunningContainers(pod *v1.Pod) bool {
	//TODO implement me
	return true
}

var _ status.PodDeletionSafetyProvider = &PodDeletionSafetyProviderStruct{}

func getPhase(spec *v1.PodSpec, info []v1.ContainerStatus) v1.PodPhase {
	pendingInitialization := 0
	failedInitialization := 0
	for _, container := range spec.InitContainers {
		containerStatus, ok := podutil.GetContainerStatus(info, container.Name)
		if !ok {
			pendingInitialization++
			continue
		}

		switch {
		case containerStatus.State.Running != nil:
			pendingInitialization++
		case containerStatus.State.Terminated != nil:
			if containerStatus.State.Terminated.ExitCode != 0 {
				failedInitialization++
			}
		case containerStatus.State.Waiting != nil:
			if containerStatus.LastTerminationState.Terminated != nil {
				if containerStatus.LastTerminationState.Terminated.ExitCode != 0 {
					failedInitialization++
				}
			} else {
				pendingInitialization++
			}
		default:
			pendingInitialization++
		}
	}

	unknown := 0
	running := 0
	waiting := 0
	stopped := 0
	succeeded := 0
	for _, container := range spec.Containers {
		containerStatus, ok := podutil.GetContainerStatus(info, container.Name)
		if !ok {
			unknown++
			continue
		}

		switch {
		case containerStatus.State.Running != nil:
			running++
		case containerStatus.State.Terminated != nil:
			stopped++
			if containerStatus.State.Terminated.ExitCode == 0 {
				succeeded++
			}
		case containerStatus.State.Waiting != nil:
			if containerStatus.LastTerminationState.Terminated != nil {
				stopped++
			} else {
				waiting++
			}
		default:
			unknown++
		}
	}

	if failedInitialization > 0 && spec.RestartPolicy == v1.RestartPolicyNever {
		return v1.PodFailed
	}

	switch {
	case pendingInitialization > 0:
		fallthrough
	case waiting > 0:
		klog.V(5).InfoS("Pod waiting > 0, pending")
		// One or more containers has not been started
		return v1.PodPending
	case running > 0 && unknown == 0:
		// All containers have been started, and at least
		// one container is running
		return v1.PodRunning
	case running == 0 && stopped > 0 && unknown == 0:
		// All containers are terminated
		if spec.RestartPolicy == v1.RestartPolicyAlways {
			// All containers are in the process of restarting
			return v1.PodRunning
		}
		if stopped == succeeded {
			// RestartPolicy is not Always, and all
			// containers are terminated in success
			return v1.PodSucceeded
		}
		if spec.RestartPolicy == v1.RestartPolicyNever {
			// RestartPolicy is Never, and all containers are
			// terminated with at least one in failure
			return v1.PodFailed
		}
		// RestartPolicy is OnFailure, and at least one in failure
		// and in the process of restarting
		return v1.PodRunning
	default:
		klog.V(5).InfoS("Pod default case, pending")
		return v1.PodPending
	}
}

type CmdRunner struct {
}

func (c *CmdRunner) RunInContainer(id kubecontainer.ContainerID, cmd []string, timeout time.Duration) ([]byte, error) {
	//TODO implement me
	return nil, nil
}

var _ kubecontainer.CommandRunner = &CmdRunner{}

