package mycore

import (
	v1 "k8s.io/api/core/v1"
	"k8s.io/klog/v2"
	kubetypes "k8s.io/kubernetes/pkg/kubelet/types"
)

// HandlePodRemove 当pod有删除事件时，处理的handler
func HandlePodRemove(pods []*v1.Pod, pc *PodCache, f CallBackFunc) {
	for _, p := range pods {
		// 加入PodManager缓存
		pc.PodManager.DeletePod(p)
		// 加入PodWorkers队列
		pc.PodWorkers.UpdatePod(UpdatePodOptions{
			UpdateType: kubetypes.SyncPodKill,
			Pod:        p,
			MirrorPod:  nil,
		})
		// 执行回调方法
		if f != nil {
			ctx := &CallBackContext{
				Pod:      p,
				recorder: pc.PodWorkers.(*podWorkers).recorder,
				podCache: pc,
			}
			err := f(ctx)
			if err != nil {
				ctx.AddWarningEvent("remove error", "remove pod error")
				klog.Error(err)
			}
		}
	}
}

// HandlePodUpdate 当pod有更新事件时，处理的handler
func HandlePodUpdate(pods []*v1.Pod, pc *PodCache, f CallBackFunc) {
	for _, p := range pods {
		// 加入PodManager缓存
		pc.PodManager.UpdatePod(p)
		// 加入PodWorkers队列
		pc.PodWorkers.UpdatePod(UpdatePodOptions{
			UpdateType: kubetypes.SyncPodUpdate,
			StartTime:  pc.Clock.Now(),
			Pod:        p,
			MirrorPod:  nil,
		})
		// 执行回调方法
		if f != nil {
			ctx := &CallBackContext{
				Pod:      p,
				recorder: pc.PodWorkers.(*podWorkers).recorder,
				podCache: pc,
			}
			err := f(ctx)
			if err != nil {
				ctx.AddWarningEvent("update error", "update pod error")
				klog.Error(err)
			}
		}
	}
}

// HandlerPodAdd 当pod有新增事件时，处理的handler
func HandlerPodAdd(pods []*v1.Pod, pc *PodCache, f CallBackFunc) {
	for _, p := range pods {
		// 加入PodManager缓存
		pc.PodManager.AddPod(p)
		// 加入PodWorkers队列
		pc.PodWorkers.UpdatePod(UpdatePodOptions{
			UpdateType: kubetypes.SyncPodCreate,
			StartTime:  pc.Clock.Now(),
			Pod:        p,
			MirrorPod:  nil,
		})
		// 执行回调方法
		if f != nil {
			ctx := &CallBackContext{
				Pod:      p,
				recorder: pc.PodWorkers.(*podWorkers).recorder,
				podCache: pc,
			}
			err := f(ctx)
			if err != nil {
				ctx.AddWarningEvent("add error", "add pod error")
				klog.Error(err)
			}
		}
	}
}
