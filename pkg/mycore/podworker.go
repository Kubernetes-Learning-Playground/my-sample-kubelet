package mycore

import (
	"context"
	"fmt"
	v1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/apimachinery/pkg/util/runtime"
	"k8s.io/apimachinery/pkg/util/wait"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/tools/record"
	runtimeapi "k8s.io/cri-api/pkg/apis/runtime/v1alpha2"
	"k8s.io/klog/v2"
	kubecontainer "k8s.io/kubernetes/pkg/kubelet/container"
	"k8s.io/utils/clock"

	kubepod "k8s.io/kubernetes/pkg/kubelet/pod"
	"k8s.io/kubernetes/pkg/kubelet/status"
	kubetypes "k8s.io/kubernetes/pkg/kubelet/types"
	"k8s.io/kubernetes/pkg/kubelet/util/queue"
	"strings"
	"sync"
	"time"
)

const (
	// NetworkNotReadyErrorMsg is used to describe the error that network is not ready
	NetworkNotReadyErrorMsg = "network is not ready"
)

// OnCompleteFunc is a function that is invoked when an operation completes.
// If err is non-nil, the operation did not complete successfully.
type OnCompleteFunc func(err error)

// PodStatusFunc is a function that is invoked to override the pod status when a pod is killed.
type PodStatusFunc func(podStatus *v1.PodStatus)

// KillPodOptions are options when performing a pod update whose update type is kill.
type KillPodOptions struct {
	// CompletedCh is closed when the kill request completes (syncTerminatingPod has completed
	// without error) or if the pod does not exist, or if the pod has already terminated. This
	// could take an arbitrary amount of time to be closed, but is never left open once
	// CouldHaveRunningContainers() returns false.
	CompletedCh chan<- struct{}
	// Evict is true if this is a pod triggered eviction - once a pod is evicted some resources are
	// more aggressively reaped than during normal pod operation (stopped containers).
	Evict bool
	// PodStatusFunc is invoked (if set) and overrides the status of the pod at the time the pod is killed.
	// The provided status is populated from the latest state.
	PodStatusFunc PodStatusFunc
	// PodTerminationGracePeriodSecondsOverride is optional override to use if a pod is being killed as part of kill operation.
	PodTerminationGracePeriodSecondsOverride *int64
}

// UpdatePodOptions is an options struct to pass to a UpdatePod operation.
type UpdatePodOptions struct {
	// The type of update (create, update, sync, kill).
	UpdateType kubetypes.SyncPodType
	// StartTime is an optional timestamp for when this update was created. If set,
	// when this update is fully realized by the pod worker it will be recorded in
	// the PodWorkerDuration metric.
	StartTime time.Time
	// Pod to update. Required.
	Pod *v1.Pod
	// MirrorPod is the mirror pod if Pod is a static pod. Optional when UpdateType
	// is kill or terminated.
	MirrorPod *v1.Pod
	// RunningPod is a runtime pod that is no longer present in config. Required
	// if Pod is nil, ignored if Pod is set.
	RunningPod *kubecontainer.Pod
	// KillPodOptions is used to override the default termination behavior of the
	// pod or to update the pod status after an operation is completed. Since a
	// pod can be killed for multiple reasons, PodStatusFunc is invoked in order
	// and later kills have an opportunity to override the status (i.e. a preemption
	// may be later turned into an eviction).
	KillPodOptions *KillPodOptions
}

// PodWorkType classifies the three phases of pod lifecycle - setup (sync),
// teardown of containers (terminating), cleanup (terminated).
type PodWorkType int

const (
	// SyncPodWork is when the pod is expected to be started and running.
	SyncPodWork PodWorkType = iota
	// TerminatingPodWork is when the pod is no longer being set up, but some
	// containers may be running and are being torn down.
	TerminatingPodWork
	// TerminatedPodWork indicates the pod is stopped, can have no more running
	// containers, and any foreground cleanup can be executed.
	TerminatedPodWork
)

// PodWorkType classifies the status of pod as seen by the pod worker - setup (sync),
// teardown of containers (terminating), cleanup (terminated), or recreated with the
// same UID (kill -> create while terminating)
type PodWorkerState int

const (
	// SyncPod is when the pod is expected to be started and running.
	SyncPod PodWorkerState = iota
	// TerminatingPod is when the pod is no longer being set up, but some
	// containers may be running and are being torn down.
	TerminatingPod
	// TerminatedPod indicates the pod is stopped, can have no more running
	// containers, and any foreground cleanup can be executed.
	TerminatedPod
	// TerminatedAndRecreatedPod indicates that after the pod was terminating a
	// request to recreate the pod was received. The pod is terminated and can
	// now be restarted by sending a create event to the pod worker.
	TerminatedAndRecreatedPod
)

// podWork is the internal changes
type podWork struct {
	// WorkType is the type of sync to perform - sync (create), terminating (stop
	// containers), terminated (clean up and write status).
	WorkType PodWorkType

	// Options contains the data to sync.
	Options UpdatePodOptions
}

// the function to invoke to cleanup a pod that is terminated
type syncTerminatedPodFnType func(ctx context.Context, pod *v1.Pod, podStatus *kubecontainer.PodStatus) error

const (
	// jitter factor for resyncInterval
	workerResyncIntervalJitterFactor = 0.5

	// jitter factor for backOffPeriod and backOffOnTransientErrorPeriod
	workerBackOffPeriodJitterFactor = 0.5

	// backoff period when transient error occurred.
	backOffOnTransientErrorPeriod = time.Second
)

// podSyncStatus tracks per-pod transitions through the three phases of pod

// PodWorkers is an abstract interface for testability.
type PodWorkers interface {
	// UpdatePod notifies the pod worker of a change to a pod, which will then
	// be processed in FIFO order by a goroutine per pod UID. The state of the
	// pod will be passed to the syncPod method until either the pod is marked
	// as deleted, it reaches a terminal phase (Succeeded/Failed), or the pod
	// is evicted by the kubelet. Once that occurs the syncTerminatingPod method
	// will be called until it exits successfully, and after that all further
	// UpdatePod() calls will be ignored for that pod until it has been forgotten
	// due to significant time passing. A pod that is terminated will never be
	// restarted.
	UpdatePod(options UpdatePodOptions)
	// SyncKnownPods removes workers for pods that are not in the desiredPods set
	// and have been terminated for a significant period of time. Once this method
	// has been called once, the workers are assumed to be fully initialized and
	// subsequent calls to ShouldPodContentBeRemoved on unknown pods will return
	// true. It returns a map describing the state of each known pod worker.
	SyncKnownPods(desiredPods []*v1.Pod) map[types.UID]PodWorkerState

	// IsPodKnownTerminated returns true if the provided pod UID is known by the pod
	// worker to be terminated. If the pod has been force deleted and the pod worker
	// has completed termination this method will return false, so this method should
	// only be used to filter out pods from the desired set such as in admission.
	//
	// Intended for use by the kubelet config loops, but not subsystems, which should
	// use ShouldPod*().
	IsPodKnownTerminated(uid types.UID) bool
	// CouldHaveRunningContainers returns true before the pod workers have synced,
	// once the pod workers see the pod (syncPod could be called), and returns false
	// after the pod has been terminated (running containers guaranteed stopped).
	//
	// Intended for use by the kubelet config loops, but not subsystems, which should
	// use ShouldPod*().
	CouldHaveRunningContainers(uid types.UID) bool
	// IsPodTerminationRequested returns true when pod termination has been requested
	// until the termination completes and the pod is removed from config. This should
	// not be used in cleanup loops because it will return false if the pod has already
	// been cleaned up - use ShouldPodContainersBeTerminating instead. Also, this method
	// may return true while containers are still being initialized by the pod worker.
	//
	// Intended for use by the kubelet sync* methods, but not subsystems, which should
	// use ShouldPod*().
	IsPodTerminationRequested(uid types.UID) bool

	// ShouldPodContainersBeTerminating returns false before pod workers have synced,
	// or once a pod has started terminating. This check is similar to
	// ShouldPodRuntimeBeRemoved but is also true after pod termination is requested.
	//
	// Intended for use by subsystem sync loops to avoid performing background setup
	// after termination has been requested for a pod. Callers must ensure that the
	// syncPod method is non-blocking when their data is absent.
	ShouldPodContainersBeTerminating(uid types.UID) bool
	// ShouldPodRuntimeBeRemoved returns true if runtime managers within the Kubelet
	// should aggressively cleanup pod resources that are not containers or on disk
	// content, like attached volumes. This is true when a pod is not yet observed
	// by a worker after the first sync (meaning it can't be running yet) or after
	// all running containers are stopped.
	// TODO: Once pod logs are separated from running containers, this method should
	// be used to gate whether containers are kept.
	//
	// Intended for use by subsystem sync loops to know when to start tearing down
	// resources that are used by running containers. Callers should ensure that
	// runtime content they own is not required for post-termination - for instance
	// containers are required in docker to preserve pod logs until after the pod
	// is deleted.
	ShouldPodRuntimeBeRemoved(uid types.UID) bool
	// ShouldPodContentBeRemoved returns true if resource managers within the Kubelet
	// should aggressively cleanup all content related to the pod. This is true
	// during pod eviction (when we wish to remove that content to free resources)
	// as well as after the request to delete a pod has resulted in containers being
	// stopped (which is a more graceful action). Note that a deleting pod can still
	// be evicted.
	//
	// Intended for use by subsystem sync loops to know when to start tearing down
	// resources that are used by non-deleted pods. Content is generally preserved
	// until deletion+removal_from_etcd or eviction, although garbage collection
	// can free content when this method returns false.
	ShouldPodContentBeRemoved(uid types.UID) bool
	// IsPodForMirrorPodTerminatingByFullName returns true if a static pod with the
	// provided pod name is currently terminating and has yet to complete. It is
	// intended to be used only during orphan mirror pod cleanup to prevent us from
	// deleting a terminating static pod from the apiserver before the pod is shut
	// down.
	IsPodForMirrorPodTerminatingByFullName(podFullname string) bool
	GetPodUpdates() map[types.UID]chan podWork
}

// the function to invoke to perform a sync (reconcile the kubelet state to the desired shape of the pod)
type syncPodFnType func(ctx context.Context, updateType kubetypes.SyncPodType, pod *v1.Pod, mirrorPod *v1.Pod, podStatus *kubecontainer.PodStatus) (bool, error)

// the function to invoke to terminate a pod (ensure no running processes are present)
type syncTerminatingPodFnType func(ctx context.Context, pod *v1.Pod, podStatus *kubecontainer.PodStatus, runningPod *kubecontainer.Pod, gracePeriod *int64, podStatusFn func(*v1.PodStatus)) error

// worker sync (setup, terminating, terminated).
type podSyncStatus struct {
	// ctx is the context that is associated with the current pod sync.
	ctx context.Context
	// cancelFn if set is expected to cancel the current sync*Pod operation.
	cancelFn context.CancelFunc
	// working is true if a pod worker is currently in a sync method.
	working bool
	// fullname of the pod
	fullname string

	// syncedAt is the time at which the pod worker first observed this pod.
	syncedAt time.Time
	// terminatingAt is set once the pod is requested to be killed - note that
	// this can be set before the pod worker starts terminating the pod, see
	// terminating.
	terminatingAt time.Time
	// startedTerminating is true once the pod worker has observed the request to
	// stop a pod (exited syncPod and observed a podWork with WorkType
	// TerminatingPodWork). Once this is set, it is safe for other components
	// of the kubelet to assume that no other containers may be started.
	startedTerminating bool
	// deleted is true if the pod has been marked for deletion on the apiserver
	// or has no configuration represented (was deleted before).
	deleted bool
	// gracePeriod is the requested gracePeriod once terminatingAt is nonzero.
	gracePeriod int64
	// evicted is true if the kill indicated this was an eviction (an evicted
	// pod can be more aggressively cleaned up).
	evicted bool
	// terminatedAt is set once the pod worker has completed a successful
	// syncTerminatingPod call and means all running containers are stopped.
	terminatedAt time.Time
	// finished is true once the pod worker completes for a pod
	// (syncTerminatedPod exited with no errors) until SyncKnownPods is invoked
	// to remove the pod. A terminal pod (Succeeded/Failed) will have
	// termination status until the pod is deleted.
	finished bool
	// restartRequested is true if the pod worker was informed the pod is
	// expected to exist (update type of create, update, or sync) after
	// it has been killed. When known pods are synced, any pod that is
	// terminated and has restartRequested will have its history cleared.
	restartRequested bool
	// notifyPostTerminating will be closed once the pod transitions to
	// terminated. After the pod is in terminated state, nothing should be
	// added to this list.
	notifyPostTerminating []chan<- struct{}
	// statusPostTerminating is a list of the status changes associated
	// with kill pod requests. After the pod is in terminated state, nothing
	// should be added to this list. The worker will execute the last function
	// in this list on each termination attempt.
	statusPostTerminating []PodStatusFunc
}

func (s *podSyncStatus) IsWorking() bool              { return s.working }
func (s *podSyncStatus) IsTerminationRequested() bool { return !s.terminatingAt.IsZero() }
func (s *podSyncStatus) IsTerminationStarted() bool   { return s.startedTerminating }
func (s *podSyncStatus) IsTerminated() bool           { return !s.terminatedAt.IsZero() }
func (s *podSyncStatus) IsFinished() bool             { return s.finished }
func (s *podSyncStatus) IsEvicted() bool              { return s.evicted }
func (s *podSyncStatus) IsDeleted() bool              { return s.deleted }

type podWorkers struct {
	// Protects all per worker fields.
	podLock sync.Mutex
	// podsSynced is true once the pod worker has been synced at least once,
	// which means that all working pods have been started via UpdatePod().
	podsSynced bool
	// Tracks all running per-pod goroutines - per-pod goroutine will be
	// processing updates received through its corresponding channel.
	podUpdates map[types.UID]chan podWork
	// Tracks the last undelivered work item for this pod - a work item is
	// undelivered if it comes in while the worker is working.
	lastUndeliveredWorkUpdate map[types.UID]podWork
	// Tracks by UID the termination status of a pod - syncing, terminating,
	// terminated, and evicted.
	podSyncStatuses map[types.UID]*podSyncStatus
	// Tracks all uids for started static pods by full name
	startedStaticPodsByFullname map[string]types.UID
	// Tracks all uids for static pods that are waiting to start by full name
	waitingToStartStaticPodsByFullname map[string][]types.UID

	workQueue queue.WorkQueue

	// This function is run to sync the desired state of pod.
	// NOTE: This function has to be thread-safe - it can be called for
	// different pods at the same time.

	syncPodFn            syncPodFnType
	syncTerminatingPodFn syncTerminatingPodFnType
	syncTerminatedPodFn  syncTerminatedPodFnType

	// workerChannelFn is exposed for testing to allow unit tests to impose delays
	// in channel communication. The function is invoked once each time a new worker
	// goroutine starts.
	workerChannelFn func(uid types.UID, in chan podWork) (out <-chan podWork)

	// The EventRecorder to use
	recorder record.EventRecorder

	// backOffPeriod is the duration to back off when there is a sync error.
	backOffPeriod time.Duration

	// resyncInterval is the duration to wait until the next sync.
	resyncInterval time.Duration

	// podCache stores kubecontainer.PodStatus for all pods.
	podCache kubecontainer.Cache

	// 自行加入，podManager管理器
	podManager kubepod.Manager

	// OnPreAdd
	OnPreAdd func(pod *v1.Pod) error
}

func NewPodWorkers(cache kubecontainer.Cache, recorder record.EventRecorder, cl clock.RealClock,
	client *kubernetes.Clientset, statusManager status.Manager, pm kubepod.Manager) PodWorkers {
	wque := queue.NewBasicWorkQueue(cl)
	pn := NewPodFn(client, statusManager, recorder)
	return &podWorkers{
		podSyncStatuses:                    map[types.UID]*podSyncStatus{},
		podUpdates:                         map[types.UID]chan podWork{},
		lastUndeliveredWorkUpdate:          map[types.UID]podWork{},
		startedStaticPodsByFullname:        map[string]types.UID{},
		waitingToStartStaticPodsByFullname: map[string][]types.UID{},
		syncPodFn:                          pn.SyncPodFn,
		syncTerminatingPodFn:               pn.SyncTerminatingFn,
		syncTerminatedPodFn:                pn.SyncTerminatedFn,
		recorder:                           recorder,
		workQueue:                          wque,
		resyncInterval:                     time.Second * 1, // 写死1秒
		backOffPeriod:                      time.Second * 10,
		podCache:                           cache,
		podManager:                         pm,
	}
}

func (p *podWorkers) GetPodUpdates() map[types.UID]chan podWork {
	return p.podUpdates
}

func (p *podWorkers) IsPodKnownTerminated(uid types.UID) bool {
	p.podLock.Lock()
	defer p.podLock.Unlock()
	if status, ok := p.podSyncStatuses[uid]; ok {
		return status.IsTerminated()
	}
	// if the pod is not known, we return false (pod worker is not aware of it)
	return false
}

func (p *podWorkers) CouldHaveRunningContainers(uid types.UID) bool {
	p.podLock.Lock()
	defer p.podLock.Unlock()
	if status, ok := p.podSyncStatuses[uid]; ok {
		return !status.IsTerminated()
	}
	// once all pods are synced, any pod without sync status is known to not be running.
	return !p.podsSynced
}

func (p *podWorkers) IsPodTerminationRequested(uid types.UID) bool {
	p.podLock.Lock()
	defer p.podLock.Unlock()
	if status, ok := p.podSyncStatuses[uid]; ok {
		// the pod may still be setting up at this point.
		return status.IsTerminationRequested()
	}
	// an unknown pod is considered not to be terminating (use ShouldPodContainersBeTerminating in
	// cleanup loops to avoid failing to cleanup pods that have already been removed from config)
	return false
}

func (p *podWorkers) ShouldPodContainersBeTerminating(uid types.UID) bool {
	p.podLock.Lock()
	defer p.podLock.Unlock()
	if status, ok := p.podSyncStatuses[uid]; ok {
		// we wait until the pod worker goroutine observes the termination, which means syncPod will not
		// be executed again, which means no new containers can be started
		return status.IsTerminationStarted()
	}
	// once we've synced, if the pod isn't known to the workers we should be tearing them
	// down
	return p.podsSynced
}

func (p *podWorkers) ShouldPodRuntimeBeRemoved(uid types.UID) bool {
	p.podLock.Lock()
	defer p.podLock.Unlock()
	if status, ok := p.podSyncStatuses[uid]; ok {
		return status.IsTerminated()
	}
	// a pod that hasn't been sent to the pod worker yet should have no runtime components once we have
	// synced all content.
	return p.podsSynced
}

func (p *podWorkers) ShouldPodContentBeRemoved(uid types.UID) bool {
	p.podLock.Lock()
	defer p.podLock.Unlock()
	if status, ok := p.podSyncStatuses[uid]; ok {
		return status.IsEvicted() || (status.IsDeleted() && status.IsTerminated())
	}
	// a pod that hasn't been sent to the pod worker yet should have no content on disk once we have
	// synced all content.
	return p.podsSynced
}

func (p *podWorkers) IsPodForMirrorPodTerminatingByFullName(podFullName string) bool {
	p.podLock.Lock()
	defer p.podLock.Unlock()
	uid, started := p.startedStaticPodsByFullname[podFullName]
	if !started {
		return false
	}
	status, exists := p.podSyncStatuses[uid]
	if !exists {
		return false
	}
	if !status.IsTerminationRequested() || status.IsTerminated() {
		return false
	}

	return true
}

func isPodStatusCacheTerminal(status *kubecontainer.PodStatus) bool {
	runningContainers := 0
	runningSandboxes := 0
	for _, container := range status.ContainerStatuses {
		if container.State == kubecontainer.ContainerStateRunning {
			runningContainers++
		}
	}
	for _, sb := range status.SandboxStatuses {
		if sb.State == runtimeapi.PodSandboxState_SANDBOX_READY {
			runningSandboxes++
		}
	}
	return runningContainers == 0 && runningSandboxes == 0
}

// UpdatePod carries a configuration change or termination state to a pod. A pod is either runnable,
// terminating, or terminated, and will transition to terminating if deleted on the apiserver, it is
// discovered to have a terminal phase (Succeeded or Failed), or if it is evicted by the kubelet.
func (p *podWorkers) UpdatePod(options UpdatePodOptions) {
	// handle when the pod is an orphan (no config) and we only have runtime status by running only
	// the terminating part of the lifecycle
	pod := options.Pod
	var isRuntimePod bool
	if options.RunningPod != nil {
		if options.Pod == nil {
			pod = options.RunningPod.ToAPIPod()
			if options.UpdateType != kubetypes.SyncPodKill {
				klog.InfoS("Pod update is ignored, runtime pods can only be killed", "pod", klog.KObj(pod), "podUID", pod.UID)
				return
			}
			options.Pod = pod
			isRuntimePod = true
		} else {
			options.RunningPod = nil
			klog.InfoS("Pod update included RunningPod which is only valid when Pod is not specified", "pod", klog.KObj(options.Pod), "podUID", options.Pod.UID)
		}
	}
	uid := pod.UID

	p.podLock.Lock()
	defer p.podLock.Unlock()

	// decide what to do with this pod - we are either setting it up, tearing it down, or ignoring it
	now := time.Now()
	status, ok := p.podSyncStatuses[uid]
	if !ok {
		klog.V(4).InfoS("Pod is being synced for the first time", "pod", klog.KObj(pod), "podUID", pod.UID)
		status = &podSyncStatus{
			syncedAt: now,
			fullname: kubecontainer.GetPodFullName(pod),
		}
		// if this pod is being synced for the first time, we need to make sure it is an active pod
		if !isRuntimePod && (pod.Status.Phase == v1.PodFailed || pod.Status.Phase == v1.PodSucceeded) {
			// check to see if the pod is not running and the pod is terminal.
			// If this succeeds then record in the podWorker that it is terminated.
			if statusCache, err := p.podCache.Get(pod.UID); err == nil {
				if isPodStatusCacheTerminal(statusCache) {
					status = &podSyncStatus{
						terminatedAt:       now,
						terminatingAt:      now,
						syncedAt:           now,
						startedTerminating: true,
						finished:           true,
						fullname:           kubecontainer.GetPodFullName(pod),
					}
				}
			}
		}
		p.podSyncStatuses[uid] = status
	}

	// if an update is received that implies the pod should be running, but we are already terminating a pod by
	// that UID, assume that two pods with the same UID were created in close temporal proximity (usually static
	// pod but it's possible for an apiserver to extremely rarely do something similar) - flag the sync status
	// to indicate that after the pod terminates it should be reset to "not running" to allow a subsequent add/update
	// to start the pod worker again
	if status.IsTerminationRequested() {
		if options.UpdateType == kubetypes.SyncPodCreate {
			status.restartRequested = true
			klog.V(4).InfoS("Pod is terminating but has been requested to restart with same UID, will be reconciled later", "pod", klog.KObj(pod), "podUID", pod.UID)
			return
		}
	}

	// once a pod is terminated by UID, it cannot reenter the pod worker (until the UID is purged by housekeeping)
	if status.IsFinished() {
		klog.V(4).InfoS("Pod is finished processing, no further updates", "pod", klog.KObj(pod), "podUID", pod.UID)
		return
	}

	// check for a transition to terminating
	var becameTerminating bool
	if !status.IsTerminationRequested() {
		switch {
		case isRuntimePod:
			klog.V(4).InfoS("Pod is orphaned and must be torn down", "pod", klog.KObj(pod), "podUID", pod.UID)
			status.deleted = true
			status.terminatingAt = now
			becameTerminating = true
		case pod.DeletionTimestamp != nil:
			klog.V(4).InfoS("Pod is marked for graceful deletion, begin teardown", "pod", klog.KObj(pod), "podUID", pod.UID)
			status.deleted = true
			status.terminatingAt = now
			becameTerminating = true
		case pod.Status.Phase == v1.PodFailed, pod.Status.Phase == v1.PodSucceeded:
			klog.V(4).InfoS("Pod is in a terminal phase (success/failed), begin teardown", "pod", klog.KObj(pod), "podUID", pod.UID)
			status.terminatingAt = now
			becameTerminating = true
		case options.UpdateType == kubetypes.SyncPodKill:
			if options.KillPodOptions != nil && options.KillPodOptions.Evict {
				klog.V(4).InfoS("Pod is being evicted by the kubelet, begin teardown", "pod", klog.KObj(pod), "podUID", pod.UID)
				status.evicted = true
			} else {
				klog.V(4).InfoS("Pod is being removed by the kubelet, begin teardown", "pod", klog.KObj(pod), "podUID", pod.UID)
			}
			status.terminatingAt = now
			becameTerminating = true
		}
	}

	// once a pod is terminating, all updates are kills and the grace period can only decrease
	var workType PodWorkType
	var wasGracePeriodShortened bool
	switch {
	case status.IsTerminated():
		// A terminated pod may still be waiting for cleanup - if we receive a runtime pod kill request
		// due to housekeeping seeing an older cached version of the runtime pod simply ignore it until
		// after the pod worker completes.
		if isRuntimePod {
			klog.V(3).InfoS("Pod is waiting for termination, ignoring runtime-only kill until after pod worker is fully terminated", "pod", klog.KObj(pod), "podUID", pod.UID)
			return
		}

		workType = TerminatedPodWork

		if options.KillPodOptions != nil {
			if ch := options.KillPodOptions.CompletedCh; ch != nil {
				close(ch)
			}
		}
		options.KillPodOptions = nil

	case status.IsTerminationRequested():
		workType = TerminatingPodWork
		if options.KillPodOptions == nil {
			options.KillPodOptions = &KillPodOptions{}
		}

		if ch := options.KillPodOptions.CompletedCh; ch != nil {
			status.notifyPostTerminating = append(status.notifyPostTerminating, ch)
		}
		if fn := options.KillPodOptions.PodStatusFunc; fn != nil {
			status.statusPostTerminating = append(status.statusPostTerminating, fn)
		}

		gracePeriod, gracePeriodShortened := calculateEffectiveGracePeriod(status, pod, options.KillPodOptions)

		wasGracePeriodShortened = gracePeriodShortened
		status.gracePeriod = gracePeriod
		// always set the grace period for syncTerminatingPod so we don't have to recalculate,
		// will never be zero.
		options.KillPodOptions.PodTerminationGracePeriodSecondsOverride = &gracePeriod

	default:
		workType = SyncPodWork

		// KillPodOptions is not valid for sync actions outside of the terminating phase
		if options.KillPodOptions != nil {
			if ch := options.KillPodOptions.CompletedCh; ch != nil {
				close(ch)
			}
			options.KillPodOptions = nil
		}
	}

	// the desired work we want to be performing
	work := podWork{
		WorkType: workType,
		Options:  options,
	}

	// start the pod worker goroutine if it doesn't exist
	podUpdates, exists := p.podUpdates[uid]

	if !exists {
		// We need to have a buffer here, because checkForUpdates() method that
		// puts an update into channel is called from the same goroutine where
		// the channel is consumed. However, it is guaranteed that in such case
		// the channel is empty, so buffer of size 1 is enough.
		podUpdates = make(chan podWork, 1)
		p.podUpdates[uid] = podUpdates

		// ensure that static pods start in the order they are received by UpdatePod
		if kubetypes.IsStaticPod(pod) {
			p.waitingToStartStaticPodsByFullname[status.fullname] =
				append(p.waitingToStartStaticPodsByFullname[status.fullname], uid)
		}

		// allow testing of delays in the pod update channel
		var outCh <-chan podWork
		if p.workerChannelFn != nil {
			outCh = p.workerChannelFn(uid, podUpdates)
		} else {
			outCh = podUpdates
		}

		// Creating a new pod worker either means this is a new pod, or that the
		// kubelet just restarted. In either case the kubelet is willing to believe
		// the status of the pod for the first pod worker sync. See corresponding
		// comment in syncPod.
		go func() {
			defer runtime.HandleCrash()
			p.managePodLoop(outCh)
		}()
	}

	// dispatch a request to the pod worker if none are running
	if !status.IsWorking() {
		status.working = true
		podUpdates <- work
		return
	}

	// capture the maximum latency between a requested update and when the pod
	// worker observes it
	if undelivered, ok := p.lastUndeliveredWorkUpdate[pod.UID]; ok {
		// track the max latency between when a config change is requested and when it is realized
		// NOTE: this undercounts the latency when multiple requests are queued, but captures max latency
		if !undelivered.Options.StartTime.IsZero() && undelivered.Options.StartTime.Before(work.Options.StartTime) {
			work.Options.StartTime = undelivered.Options.StartTime
		}
	}

	// always sync the most recent data
	p.lastUndeliveredWorkUpdate[pod.UID] = work

	if (becameTerminating || wasGracePeriodShortened) && status.cancelFn != nil {
		klog.V(3).InfoS("Cancelling current pod sync", "pod", klog.KObj(pod), "podUID", pod.UID, "updateType", work.WorkType)
		status.cancelFn()
		return
	}
}

// calculateEffectiveGracePeriod sets the initial grace period for a newly terminating pod or allows a
// shorter grace period to be provided, returning the desired value.
func calculateEffectiveGracePeriod(status *podSyncStatus, pod *v1.Pod, options *KillPodOptions) (int64, bool) {
	// enforce the restriction that a grace period can only decrease and track whatever our value is,
	// then ensure a calculated value is passed down to lower levels
	gracePeriod := status.gracePeriod
	// this value is bedrock truth - the apiserver owns telling us this value calculated by apiserver
	if override := pod.DeletionGracePeriodSeconds; override != nil {
		if gracePeriod == 0 || *override < gracePeriod {
			gracePeriod = *override
		}
	}
	// we allow other parts of the kubelet (namely eviction) to request this pod be terminated faster
	if options != nil {
		if override := options.PodTerminationGracePeriodSecondsOverride; override != nil {
			if gracePeriod == 0 || *override < gracePeriod {
				gracePeriod = *override
			}
		}
	}
	// make a best effort to default this value to the pod's desired intent, in the event
	// the kubelet provided no requested value (graceful termination?)
	if gracePeriod == 0 && pod.Spec.TerminationGracePeriodSeconds != nil {
		gracePeriod = *pod.Spec.TerminationGracePeriodSeconds
	}
	// no matter what, we always supply a grace period of 1
	if gracePeriod < 1 {
		gracePeriod = 1
	}
	return gracePeriod, status.gracePeriod != 0 && status.gracePeriod != gracePeriod
}

// allowPodStart tries to start the pod and returns true if allowed, otherwise
// it requeues the pod and returns false. If the pod will never be able to start
// because data is missing, or the pod was terminated before start, canEverStart
// is false.
func (p *podWorkers) allowPodStart(pod *v1.Pod) (canStart bool, canEverStart bool) {
	if !kubetypes.IsStaticPod(pod) {
		// TODO: Do we want to allow non-static pods with the same full name?
		// Note that it may disable the force deletion of pods.
		return true, true
	}
	p.podLock.Lock()
	defer p.podLock.Unlock()
	status, ok := p.podSyncStatuses[pod.UID]
	if !ok {
		klog.ErrorS(nil, "Pod sync status does not exist, the worker should not be running", "pod", klog.KObj(pod), "podUID", pod.UID)
		return false, false
	}
	if status.IsTerminationRequested() {
		return false, false
	}
	if !p.allowStaticPodStart(status.fullname, pod.UID) {
		p.workQueue.Enqueue(pod.UID, wait.Jitter(p.backOffPeriod, workerBackOffPeriodJitterFactor))
		status.working = false
		return false, true
	}
	return true, true
}

// allowStaticPodStart tries to start the static pod and returns true if
// 1. there are no other started static pods with the same fullname
// 2. the uid matches that of the first valid static pod waiting to start
func (p *podWorkers) allowStaticPodStart(fullname string, uid types.UID) bool {
	startedUID, started := p.startedStaticPodsByFullname[fullname]
	if started {
		return startedUID == uid
	}

	waitingPods := p.waitingToStartStaticPodsByFullname[fullname]
	// TODO: This is O(N) with respect to the number of updates to static pods
	// with overlapping full names, and ideally would be O(1).
	for i, waitingUID := range waitingPods {
		// has pod already terminated or been deleted?
		status, ok := p.podSyncStatuses[waitingUID]
		if !ok || status.IsTerminationRequested() || status.IsTerminated() {
			continue
		}
		// another pod is next in line
		if waitingUID != uid {
			p.waitingToStartStaticPodsByFullname[fullname] = waitingPods[i:]
			return false
		}
		// we are up next, remove ourselves
		waitingPods = waitingPods[i+1:]
		break
	}
	if len(waitingPods) != 0 {
		p.waitingToStartStaticPodsByFullname[fullname] = waitingPods
	} else {
		delete(p.waitingToStartStaticPodsByFullname, fullname)
	}
	p.startedStaticPodsByFullname[fullname] = uid
	return true
}

func insertPodCache(podid types.UID, pm kubepod.Manager, podCache kubecontainer.Cache) error {
	getPod, found := pm.GetPodByUID(types.UID(podid))
	if !found {
		return fmt.Errorf("pod not found")
	}
	podStatus := SetPodStatus(getPod, kubecontainer.ContainerStateRunning)
	podCache.Set(podid, podStatus, nil, time.Now())
	return nil
}

// managePodLoop 方法
func (p *podWorkers) managePodLoop(podUpdates <-chan podWork) {

	var lastSyncTime time.Time
	var podStarted bool
	for update := range podUpdates {
		pod := update.Options.Pod
		//fmt.Printf("要处理的POD名称是:%s,ID是:%s,phase是:%s\n", pod.Name, pod.UID, pod.Status.Phase)
		//insertErr := insertPodCache(pod.UID, p.podManager, p.podCache)
		//if insertErr != nil {
		//	fmt.Printf("插入缓存失败:%s\n", insertErr)
		//} else {
		//	if p.OnPreAdd != nil {
		//		onaddErr := p.OnPreAdd(pod)
		//		if onaddErr != nil {
		//			fmt.Printf("执行onadd失败:%s\n", onaddErr)
		//		}
		//	}
		//}
		if !podStarted {
			fmt.Printf("要处理的POD名称是:%s,ID是:%s,phase是:%s\n", pod.Name, pod.UID, pod.Status.Phase)
			insertErr := insertPodCache(pod.UID, p.podManager, p.podCache)
			if insertErr != nil {
				fmt.Printf("插入缓存失败:%s\n", insertErr)
			} else {
				if p.OnPreAdd != nil {
					onaddErr := p.OnPreAdd(pod)
					if onaddErr != nil {
						fmt.Printf("执行onadd失败:%s\n", onaddErr)
					}
				}
			}
		}
		// Decide whether to start the pod. If the pod was terminated prior to the pod being allowed
		// to start, we have to clean it up and then exit the pod worker loop.
		if !podStarted {
			canStart, canEverStart := p.allowPodStart(pod)
			if !canEverStart {
				p.completeUnstartedTerminated(pod)
				if start := update.Options.StartTime; !start.IsZero() {
				}
				klog.V(4).InfoS("Processing pod event done", "pod", klog.KObj(pod), "podUID", pod.UID, "updateType", update.WorkType)
				return
			}
			if !canStart {
				klog.V(4).InfoS("Pod cannot start yet", "pod", klog.KObj(pod), "podUID", pod.UID)
				continue
			}
			podStarted = true
		}

		klog.V(4).InfoS("Processing pod event", "pod", klog.KObj(pod), "podUID", pod.UID, "updateType", update.WorkType)
		var isTerminal bool
		err := func() error {

			// The worker is responsible for ensuring the sync method sees the appropriate
			// status updates on resyncs (the result of the last sync), transitions to
			// terminating (no wait), or on terminated (whatever the most recent state is).
			// Only syncing and terminating can generate pod status changes, while terminated
			// pods ensure the most recent status makes it to the api server.
			var status *kubecontainer.PodStatus
			var err error

			switch {
			case update.Options.RunningPod != nil:

				// when we receive a running pod, we don't need status at all
			default:
				// wait until we see the next refresh from the PLEG via the cache (max 2s)
				// 等待，直到我们通过缓存看到来自 PLEG 的下一次刷新（最多 2 秒）
				// TODO: this adds ~1s of latency on all transitions from sync to terminating
				//  to terminated, and on all termination retries (including evictions). We should
				//  improve latency by making the the pleg continuous and by allowing pod status
				//  changes to be refreshed when key events happen (killPod, sync->terminating).
				//  Improving this latency also reduces the possibility that a terminated
				//  container's status is garbage collected before we have a chance to update the
				//  API server (thus losing the exit code).
				// 会等待pod cache是否有值
				status, err = p.podCache.GetNewerThan(pod.UID, lastSyncTime)

			}
			if err != nil {
				// This is the legacy event thrown by manage pod loop all other events are now dispatched
				// from syncPodFn
				return err
			}

			ctx := p.contextForWorker(pod.UID)
			// Take the appropriate action (illegal phases are prevented by UpdatePod)
			switch {
			case update.WorkType == TerminatedPodWork:
				err = p.syncTerminatedPodFn(ctx, pod, status)

			case update.WorkType == TerminatingPodWork:
				var gracePeriod *int64
				if opt := update.Options.KillPodOptions; opt != nil {
					gracePeriod = opt.PodTerminationGracePeriodSecondsOverride
				}
				podStatusFn := p.acknowledgeTerminating(pod)

				err = p.syncTerminatingPodFn(ctx, pod, status, update.Options.RunningPod, gracePeriod, podStatusFn)

			default:
				isTerminal, err = p.syncPodFn(ctx, update.Options.UpdateType, pod, update.Options.MirrorPod, status)
			}

			lastSyncTime = time.Now()
			return err
		}()

		var phaseTransition bool
		switch {
		case err == context.Canceled:
			// when the context is cancelled we expect an update to already be queued
			klog.V(2).InfoS("Sync exited with context cancellation error", "pod", klog.KObj(pod), "podUID", pod.UID, "updateType", update.WorkType)

		case err != nil:
			// we will queue a retry
			klog.ErrorS(err, "Error syncing pod, skipping", "pod", klog.KObj(pod), "podUID", pod.UID)

		case update.WorkType == TerminatedPodWork:
			// we can shut down the worker
			p.completeTerminated(pod)
			if start := update.Options.StartTime; !start.IsZero() {
			}
			klog.V(4).InfoS("Processing pod event done", "pod", klog.KObj(pod), "podUID", pod.UID, "updateType", update.WorkType)
			return

		case update.WorkType == TerminatingPodWork:
			// pods that don't exist in config don't need to be terminated, garbage collection will cover them
			if update.Options.RunningPod != nil {
				p.completeTerminatingRuntimePod(pod)

				klog.V(4).InfoS("Processing pod event done", "pod", klog.KObj(pod), "podUID", pod.UID, "updateType", update.WorkType)
				return
			}
			// otherwise we move to the terminating phase
			p.completeTerminating(pod)
			phaseTransition = true

		case isTerminal:
			// if syncPod indicated we are now terminal, set the appropriate pod status to move to terminating
			klog.V(4).InfoS("Pod is terminal", "pod", klog.KObj(pod), "podUID", pod.UID, "updateType", update.WorkType)
			p.completeSync(pod)
			phaseTransition = true
		}

		// queue a retry if necessary, then put the next event in the channel if any
		p.completeWork(pod, phaseTransition, err)
		if start := update.Options.StartTime; !start.IsZero() {
		}
		klog.V(4).InfoS("Processing pod event done", "pod", klog.KObj(pod), "podUID", pod.UID, "updateType", update.WorkType)
	}
}

// acknowledgeTerminating sets the terminating flag on the pod status once the pod worker sees
// the termination state so that other components know no new containers will be started in this
// pod. It then returns the status function, if any, that applies to this pod.
func (p *podWorkers) acknowledgeTerminating(pod *v1.Pod) PodStatusFunc {
	p.podLock.Lock()
	defer p.podLock.Unlock()

	status, ok := p.podSyncStatuses[pod.UID]
	if !ok {
		return nil
	}

	if !status.terminatingAt.IsZero() && !status.startedTerminating {
		klog.V(4).InfoS("Pod worker has observed request to terminate", "pod", klog.KObj(pod), "podUID", pod.UID)
		status.startedTerminating = true
	}

	if l := len(status.statusPostTerminating); l > 0 {
		return status.statusPostTerminating[l-1]
	}
	return nil
}

// completeSync is invoked when syncPod completes successfully and indicates the pod is now terminal and should
// be terminated. This happens when the natural pod lifecycle completes - any pod which is not RestartAlways
// exits. Unnatural completions, such as evictions, API driven deletion or phase transition, are handled by
// UpdatePod.
func (p *podWorkers) completeSync(pod *v1.Pod) {
	p.podLock.Lock()
	defer p.podLock.Unlock()

	klog.V(4).InfoS("Pod indicated lifecycle completed naturally and should now terminate", "pod", klog.KObj(pod), "podUID", pod.UID)

	if status, ok := p.podSyncStatuses[pod.UID]; ok {
		if status.terminatingAt.IsZero() {
			status.terminatingAt = time.Now()
		} else {
			klog.V(4).InfoS("Pod worker attempted to set terminatingAt twice, likely programmer error", "pod", klog.KObj(pod), "podUID", pod.UID)
		}
		status.startedTerminating = true
	}

	p.lastUndeliveredWorkUpdate[pod.UID] = podWork{
		WorkType: TerminatingPodWork,
		Options: UpdatePodOptions{
			Pod: pod,
		},
	}
}

// completeTerminating is invoked when syncTerminatingPod completes successfully, which means
// no container is running, no container will be started in the future, and we are ready for
// cleanup.  This updates the termination state which prevents future syncs and will ensure
// other kubelet loops know this pod is not running any containers.
func (p *podWorkers) completeTerminating(pod *v1.Pod) {
	p.podLock.Lock()
	defer p.podLock.Unlock()

	klog.V(4).InfoS("Pod terminated all containers successfully", "pod", klog.KObj(pod), "podUID", pod.UID)

	if status, ok := p.podSyncStatuses[pod.UID]; ok {
		if status.terminatingAt.IsZero() {
			klog.V(4).InfoS("Pod worker was terminated but did not have terminatingAt set, likely programmer error", "pod", klog.KObj(pod), "podUID", pod.UID)
		}
		status.terminatedAt = time.Now()
		for _, ch := range status.notifyPostTerminating {
			close(ch)
		}
		status.notifyPostTerminating = nil
		status.statusPostTerminating = nil
	}

	p.lastUndeliveredWorkUpdate[pod.UID] = podWork{
		WorkType: TerminatedPodWork,
		Options: UpdatePodOptions{
			Pod: pod,
		},
	}
}

// completeTerminatingRuntimePod is invoked when syncTerminatingPod completes successfully,
// which means an orphaned pod (no config) is terminated and we can exit. Since orphaned
// pods have no API representation, we want to exit the loop at this point
// cleanup.  This updates the termination state which prevents future syncs and will ensure
// other kubelet loops know this pod is not running any containers.
func (p *podWorkers) completeTerminatingRuntimePod(pod *v1.Pod) {
	p.podLock.Lock()
	defer p.podLock.Unlock()

	klog.V(4).InfoS("Pod terminated all orphaned containers successfully and worker can now stop", "pod", klog.KObj(pod), "podUID", pod.UID)

	if status, ok := p.podSyncStatuses[pod.UID]; ok {
		if status.terminatingAt.IsZero() {
			klog.V(4).InfoS("Pod worker was terminated but did not have terminatingAt set, likely programmer error", "pod", klog.KObj(pod), "podUID", pod.UID)
		}
		status.terminatedAt = time.Now()
		status.finished = true
		status.working = false

		if p.startedStaticPodsByFullname[status.fullname] == pod.UID {
			delete(p.startedStaticPodsByFullname, status.fullname)
		}
	}

	p.cleanupPodUpdates(pod.UID)
}

// completeTerminated is invoked after syncTerminatedPod completes successfully and means we
// can stop the pod worker. The pod is finalized at this point.
func (p *podWorkers) completeTerminated(pod *v1.Pod) {
	p.podLock.Lock()
	defer p.podLock.Unlock()

	klog.V(4).InfoS("Pod is complete and the worker can now stop", "pod", klog.KObj(pod), "podUID", pod.UID)

	p.cleanupPodUpdates(pod.UID)

	if status, ok := p.podSyncStatuses[pod.UID]; ok {
		if status.terminatingAt.IsZero() {
			klog.V(4).InfoS("Pod worker is complete but did not have terminatingAt set, likely programmer error", "pod", klog.KObj(pod), "podUID", pod.UID)
		}
		if status.terminatedAt.IsZero() {
			klog.V(4).InfoS("Pod worker is complete but did not have terminatedAt set, likely programmer error", "pod", klog.KObj(pod), "podUID", pod.UID)
		}
		status.finished = true
		status.working = false

		if p.startedStaticPodsByFullname[status.fullname] == pod.UID {
			delete(p.startedStaticPodsByFullname, status.fullname)
		}
	}
}

// completeUnstartedTerminated is invoked if a pod that has never been started receives a termination
// signal before it can be started.
func (p *podWorkers) completeUnstartedTerminated(pod *v1.Pod) {
	p.podLock.Lock()
	defer p.podLock.Unlock()

	klog.V(4).InfoS("Pod never started and the worker can now stop", "pod", klog.KObj(pod), "podUID", pod.UID)

	p.cleanupPodUpdates(pod.UID)

	if status, ok := p.podSyncStatuses[pod.UID]; ok {
		if status.terminatingAt.IsZero() {
			klog.V(4).InfoS("Pod worker is complete but did not have terminatingAt set, likely programmer error", "pod", klog.KObj(pod), "podUID", pod.UID)
		}
		if !status.terminatedAt.IsZero() {
			klog.V(4).InfoS("Pod worker is complete and had terminatedAt set, likely programmer error", "pod", klog.KObj(pod), "podUID", pod.UID)
		}
		status.finished = true
		status.working = false
		status.terminatedAt = time.Now()

		if p.startedStaticPodsByFullname[status.fullname] == pod.UID {
			delete(p.startedStaticPodsByFullname, status.fullname)
		}
	}
}

// completeWork requeues on error or the next sync interval and then immediately executes any pending
// work.
func (p *podWorkers) completeWork(pod *v1.Pod, phaseTransition bool, syncErr error) {
	// Requeue the last update if the last sync returned error.
	switch {
	case phaseTransition:
		p.workQueue.Enqueue(pod.UID, 0)
	case syncErr == nil:
		// No error; requeue at the regular resync interval.
		p.workQueue.Enqueue(pod.UID, wait.Jitter(p.resyncInterval, workerResyncIntervalJitterFactor))
	case strings.Contains(syncErr.Error(), NetworkNotReadyErrorMsg):
		// Network is not ready; back off for short period of time and retry as network might be ready soon.
		p.workQueue.Enqueue(pod.UID, wait.Jitter(backOffOnTransientErrorPeriod, workerBackOffPeriodJitterFactor))
	default:
		// Error occurred during the sync; back off and then retry.
		p.workQueue.Enqueue(pod.UID, wait.Jitter(p.backOffPeriod, workerBackOffPeriodJitterFactor))
	}
	p.completeWorkQueueNext(pod.UID)
}

// completeWorkQueueNext holds the lock and either queues the next work item for the worker or
// clears the working status.
func (p *podWorkers) completeWorkQueueNext(uid types.UID) {
	p.podLock.Lock()
	defer p.podLock.Unlock()
	if workUpdate, exists := p.lastUndeliveredWorkUpdate[uid]; exists {
		p.podUpdates[uid] <- workUpdate
		delete(p.lastUndeliveredWorkUpdate, uid)
	} else {
		p.podSyncStatuses[uid].working = false
	}
}

// contextForWorker returns or initializes the appropriate context for a known
// worker. If the current context is expired, it is reset. If no worker is
// present, no context is returned.
func (p *podWorkers) contextForWorker(uid types.UID) context.Context {
	p.podLock.Lock()
	defer p.podLock.Unlock()

	status, ok := p.podSyncStatuses[uid]
	if !ok {
		return nil
	}
	if status.ctx == nil || status.ctx.Err() == context.Canceled {
		status.ctx, status.cancelFn = context.WithCancel(context.Background())
	}
	return status.ctx
}

// SyncKnownPods will purge any fully terminated pods that are not in the desiredPods
// list, which means SyncKnownPods must be called in a threadsafe manner from calls
// to UpdatePods for new pods. It returns a map of known workers that are not finished
// with a value of SyncPodTerminated, SyncPodKill, or SyncPodSync depending on whether
// the pod is terminated, terminating, or syncing.
func (p *podWorkers) SyncKnownPods(desiredPods []*v1.Pod) map[types.UID]PodWorkerState {
	workers := make(map[types.UID]PodWorkerState)
	known := make(map[types.UID]struct{})
	for _, pod := range desiredPods {
		known[pod.UID] = struct{}{}
	}

	p.podLock.Lock()
	defer p.podLock.Unlock()

	p.podsSynced = true
	for uid, status := range p.podSyncStatuses {
		if _, exists := known[uid]; !exists || status.restartRequested {
			p.removeTerminatedWorker(uid)
		}
		switch {
		case !status.terminatedAt.IsZero():
			if status.restartRequested {
				workers[uid] = TerminatedAndRecreatedPod
			} else {
				workers[uid] = TerminatedPod
			}
		case !status.terminatingAt.IsZero():
			workers[uid] = TerminatingPod
		default:
			workers[uid] = SyncPod
		}
	}
	return workers
}

// removeTerminatedWorker cleans up and removes the worker status for a worker
// that has reached a terminal state of "finished" - has successfully exited
// syncTerminatedPod. This "forgets" a pod by UID and allows another pod to be
// recreated with the same UID.
func (p *podWorkers) removeTerminatedWorker(uid types.UID) {
	status, ok := p.podSyncStatuses[uid]
	if !ok {
		// already forgotten, or forgotten too early
		klog.V(4).InfoS("Pod worker has been requested for removal but is not a known pod", "podUID", uid)
		return
	}

	if !status.finished {
		klog.V(4).InfoS("Pod worker has been requested for removal but is still not fully terminated", "podUID", uid)
		return
	}

	if status.restartRequested {
		klog.V(4).InfoS("Pod has been terminated but another pod with the same UID was created, remove history to allow restart", "podUID", uid)
	} else {
		klog.V(4).InfoS("Pod has been terminated and is no longer known to the kubelet, remove all history", "podUID", uid)
	}
	delete(p.podSyncStatuses, uid)
	p.cleanupPodUpdates(uid)

	if p.startedStaticPodsByFullname[status.fullname] == uid {
		delete(p.startedStaticPodsByFullname, status.fullname)
	}
}

// cleanupPodUpdates closes the podUpdates channel and removes it from
// podUpdates map so that the corresponding pod worker can stop. It also
// removes any undelivered work. This method must be called holding the
// pod lock.
func (p *podWorkers) cleanupPodUpdates(uid types.UID) {
	if ch, ok := p.podUpdates[uid]; ok {
		close(ch)
	}
	delete(p.podUpdates, uid)
	delete(p.lastUndeliveredWorkUpdate, uid)
}