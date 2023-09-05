package mycore

import (
	"k8s.io/klog/v2"
	"time"
)

func OnRemove(ctx *CallBackContext) error {
	klog.Infof("Remove: %s", ctx.Pod.Name)
	ctx.AddNormalEvent("pod event", "remove pod")
	return nil
}

func OnDelete(ctx *CallBackContext) error {
	klog.Infof("Delete: %s", ctx.Pod.Name)
	ctx.AddNormalEvent("pod event", "delete pod")
	return nil
}

func OnUpdate(ctx *CallBackContext) error {
	klog.Infof("Update: %s", ctx.Pod.Name)
	ctx.AddNormalEvent("pod event", "update pod")
	return nil
}

func OnAdd(ctx *CallBackContext) error {
	cmds := ctx.ExecPodCommands()
	for _, cmd := range cmds {
		cmd.Run()
		cmd.Cmd.Output()
		ctx.SetContainerExit(cmd.ContainerName, cmd.ExitCode)
	}

	klog.Infof("Add: %s", ctx.Pod.Name)
	ctx.AddNormalEvent("pod event", "add pod")
	time.Sleep(time.Second * 3)
	// TODO: 这里需要修改，改为完成container的所有任务才改变pod状态
	ctx.SetPodCompleted()
	return nil
}
