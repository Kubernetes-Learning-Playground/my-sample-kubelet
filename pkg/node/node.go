package node

import (
	"context"
	v1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/kubernetes"
	"k8s.io/klog/v2"
	"k8s.io/kubernetes/pkg/util"
	"runtime"
)

// RegisterNode 注册node
func RegisterNode(nodeName string, client *kubernetes.Clientset) {
	// node对象
	node := &v1.Node{
		ObjectMeta: metav1.ObjectMeta{
			Name: nodeName,
			Labels: map[string]string{
				v1.LabelHostname:   nodeName,
				v1.LabelOSStable:   runtime.GOOS,
				v1.LabelArchStable: runtime.GOARCH,
			},
		},
		// TODO: 没填写node spec
		Spec: v1.NodeSpec{},
	}

	// 先获取，如果 err为 not found，则需要创建，
	var nodeInstance *v1.Node
	var err error
	nodeInstance, err = client.CoreV1().Nodes().Get(context.Background(), nodeName, metav1.GetOptions{})
	if err != nil && errors.IsNotFound(err) {
		if errors.IsNotFound(err) {
			// 创建
			nodeInstance, err = client.CoreV1().Nodes().Create(context.Background(), node, metav1.CreateOptions{})
			if err != nil {
				klog.Fatalln(err)
			}
			klog.Infof("create node %s success \n", nodeName)
		} else {
			klog.Errorf("get and create node by client error: ", err)
			return
		}

	}
	newNode := nodeInstance.DeepCopy()
	setNodeStatus(newNode)
	// patch node的状态与其他信息
	patchBytes, err := util.PreparePatchBytesforNodeStatus(types.NodeName(nodeName), node, newNode)
	if err != nil {
		klog.Fatalln(err)
	}
	// 执行patch操作
	patchNode, err := client.CoreV1().Nodes().Patch(context.TODO(),
		nodeName, types.StrategicMergePatchType, patchBytes, metav1.PatchOptions{}, "status")
	if err != nil {
		klog.Fatalln(err)
	}
	klog.Infoln("node status update success \n")
	node = patchNode

}
