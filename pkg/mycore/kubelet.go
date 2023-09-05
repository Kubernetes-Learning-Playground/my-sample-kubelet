package mycore

import (
	v1 "k8s.io/api/core/v1"
	"k8s.io/client-go/kubernetes"
	"k8s.io/klog/v2"
	kubetypes "k8s.io/kubernetes/pkg/kubelet/types"
)

// SampleKubelet 简易kubelet
type SampleKubelet struct {
	// podCache pod缓存，
	podCache *PodCache
	// onAdd 新增事件回调
	onAdd CallBackFunc
	// onUpdate 更新事件回调
	onUpdate CallBackFunc
	// onDelete 删除事件回调
	onDelete CallBackFunc
	// onRemove 删除事件回调
	onRemove CallBackFunc
}

func (k *SampleKubelet) SetOnPreAdd(onAdd func(pod *v1.Pod) error) {
	k.podCache.PodWorkers.(*podWorkers).OnPreAdd = onAdd
}

// Start 启动kubelet，主要是不断从podCache.PodConfig.Updates()中chan
// 获取包装过的pod对象，并区分不同事件，进入相应的handler
func (k *SampleKubelet) Start() {
	klog.Info("sample kubelet start...")
	for item := range k.podCache.PodConfig.Updates() {
		pods := item.Pods
		switch item.Op {
		case kubetypes.ADD:
			HandlerPodAdd(pods, k.podCache, k.onAdd)
		case kubetypes.UPDATE:
			klog.Info("进入update")
			HandlePodUpdate(pods, k.podCache, k.onUpdate)
			break
		case kubetypes.DELETE:
			klog.Info("进入delete")
			HandlePodUpdate(pods, k.podCache, k.onDelete)
			break
		case kubetypes.REMOVE:
			klog.Info("进入remove")
			HandlePodRemove(pods, k.podCache, k.onRemove)
			break
		}
	}
}

func NewSampleKubelet(client *kubernetes.Clientset, nodeName string) *SampleKubelet {
	pc := NewPodCache(client, nodeName)
	return &SampleKubelet{
		podCache: pc,
		onAdd:    OnAdd,
		onUpdate: OnUpdate,
		onDelete: OnDelete,
		onRemove: OnRemove,
	}
}
