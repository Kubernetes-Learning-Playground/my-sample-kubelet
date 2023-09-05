package mycore

import (
	v1 "k8s.io/api/core/v1"
	"k8s.io/client-go/tools/record"
	"k8s.io/klog/v2"
	"os/exec"
	"time"
)

// CallBackContext 回调函数中传递的ctx
type CallBackContext struct {
	// Pod 回调时的Pod资源
	Pod *v1.Pod
	// recorder 事件通知器
	recorder record.EventRecorder
	// podCache pod缓存，当事件回调时，可以修改pod状态
	podCache *PodCache
}

// ExecPodCommands 当发生Pod新增事件时，执行此方法
// 遍历容器内所有cmd，并执行
func (c *CallBackContext) ExecPodCommands() []*ContainerCmd {
	res := make([]*ContainerCmd, 0)
	for _, c := range c.Pod.Spec.Containers {
		if len(c.Command) == 0 {
			continue
		}
		args := make([]string, 0)
		if len(c.Command) > 1 {
			args = append(args, c.Command[1:]...)
		}
		args = append(args, c.Args...)
		cmd := exec.Command(c.Command[0], args...)
		res = append(res, &ContainerCmd{
			Cmd:           cmd,
			ContainerName: c.Name,
		})
	}
	return res
}

// Deprecated: 使用ExecPodCommands方法，用ContainerCmd装一层，此方法以废弃
func (c *CallBackContext) GetCommandsAndArgs() []*exec.Cmd {
	ret := make([]*exec.Cmd, 0)
	for _, c := range c.Pod.Spec.Containers {
		if len(c.Command) == 0 {
			continue
		}
		args := make([]string, 0)
		if len(c.Command) > 1 {
			args = append(args, c.Command[1:]...)
		}
		args = append(args, c.Args...)
		cmd := exec.Command(c.Command[0], args...)
		ret = append(ret, cmd)
	}
	return ret

}

func (c *CallBackContext) SetContainerRunning(containerName string) {
	ps, err := c.podCache.InnerPodCache.Get(c.Pod.UID)
	if err != nil {
		klog.Error(err)
		return
	}
	status := SetContainerRunning(ps, containerName)
	c.podCache.InnerPodCache.Set(c.Pod.UID, status, nil, time.Now())
}

func (c *CallBackContext) SetContainerExit(containerName string, exitCode int) {
	ps, err := c.podCache.InnerPodCache.Get(c.Pod.UID)
	if err != nil {
		klog.Error(err)
		return
	}

	status := SetContainerExit(ps, c.Pod, containerName, exitCode)
	c.podCache.InnerPodCache.Set(c.Pod.UID, status, nil, time.Now())
}

// 设置POD结束完成状态
func (c *CallBackContext) SetPodCompleted() {
	status := SetPodCompleted(c.Pod)
	c.podCache.InnerPodCache.Set(c.Pod.UID, status, nil, time.Now())
}

// AddNormalEvent 发送正常事件
func (c *CallBackContext) AddNormalEvent(reason, messae string) {
	c.recorder.Event(c.Pod, v1.EventTypeNormal, reason, messae)
}

// AddWarningEvent 发送警告事件
func (c *CallBackContext) AddWarningEvent(reason, messae string) {
	c.recorder.Event(c.Pod, v1.EventTypeWarning, reason, messae)
}

// CallBackFunc 回调func
type CallBackFunc func(ctx *CallBackContext) error

// type CallBackFunc func(pod *v1.Pod) error
