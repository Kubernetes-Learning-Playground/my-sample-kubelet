package node

import (
	v1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"runtime"
)

// setNodeStatus 设置node状态
func setNodeStatus(node *v1.Node) {
	node.Status.NodeInfo = nodeInfo()
	node.Status.DaemonEndpoints = nodeDaemonEndpoints(10250)
	node.Status.Addresses = nodeAddresses()
	node.Status.Conditions = nodeConditions()
	node.Status.Capacity = nodeCapacity()
}

// nodeDaemonEndpoints 节点端口
func nodeDaemonEndpoints(port int32) v1.NodeDaemonEndpoints {
	return v1.NodeDaemonEndpoints{
		KubeletEndpoint: v1.DaemonEndpoint{
			Port: port,
		},
	}
}

// nodeAddresses 节点内部IP
func nodeAddresses() []v1.NodeAddress {
	return []v1.NodeAddress{
		{
			Type:    "InternalIP",
			Address: "121.231.134.231",
		},
	}
}

// nodeConditions 节点状态集合
func nodeConditions() []v1.NodeCondition {
	// TODO: Make this configurable
	return []v1.NodeCondition{
		{
			Type:               "Ready",
			Status:             v1.ConditionTrue,
			LastHeartbeatTime:  metav1.Now(),
			LastTransitionTime: metav1.Now(),
			Reason:             "KubeletReady",
			Message:            "kubelet is ready.",
		},
		{
			Type:               "OutOfDisk",
			Status:             v1.ConditionFalse,
			LastHeartbeatTime:  metav1.Now(),
			LastTransitionTime: metav1.Now(),
			Reason:             "KubeletHasSufficientDisk",
			Message:            "kubelet has sufficient disk space available",
		},
		{
			Type:               "MemoryPressure",
			Status:             v1.ConditionFalse,
			LastHeartbeatTime:  metav1.Now(),
			LastTransitionTime: metav1.Now(),
			Reason:             "KubeletHasSufficientMemory",
			Message:            "kubelet has sufficient memory available",
		},
		{
			Type:               "DiskPressure",
			Status:             v1.ConditionFalse,
			LastHeartbeatTime:  metav1.Now(),
			LastTransitionTime: metav1.Now(),
			Reason:             "KubeletHasNoDiskPressure",
			Message:            "kubelet has no disk pressure",
		},
		{
			Type:               "NetworkUnavailable",
			Status:             v1.ConditionFalse,
			LastHeartbeatTime:  metav1.Now(),
			LastTransitionTime: metav1.Now(),
			Reason:             "RouteCreated",
			Message:            "RouteController created a route",
		},
	}

}

// nodeInfo 节点信息
func nodeInfo() v1.NodeSystemInfo {
	return v1.NodeSystemInfo{
		KubeletVersion: "v1.22.99",
	}
}

// nodeCapacity 节点资源信息
func nodeCapacity() v1.ResourceList {
	var cpuQ resource.Quantity
	cpuQ.Set(int64(runtime.NumCPU()))

	var memQ resource.Quantity
	memQ.Set(int64(1024 * 1024 * 1024 * 32)) //好比 32G内存。 假的。别纠结
	memQ.Format = resource.BinarySI
	return v1.ResourceList{
		"cpu":    cpuQ,
		"memory": memQ,
		"pods":   resource.MustParse("200"), //最多创建 多少个pod

	}
}
