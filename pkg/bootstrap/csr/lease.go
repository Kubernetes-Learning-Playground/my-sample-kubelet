package csr

import (
	"context"
	"fmt"
	coordinationv1 "k8s.io/api/coordination/v1"
	v1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/apimachinery/pkg/util/json"
	clientset "k8s.io/client-go/kubernetes"
	"time"
)

// 租约，用于续租

type Value struct {
	Type   string `json:"type"`
	Status string `json:"status"`
}
type Cond struct {
	Op    string `json:"op"`
	Path  string `json:"path"`
	Value Value  `json:"value"`
}

const (
	LeaseNameSpace = "kube-node-lease"
	LeaseName      = "myjtthink"
)

// 全局的lease
var lease *coordinationv1.Lease

// 模拟续期
func renewLease(client *clientset.Clientset) error {
	now := v1.NewMicroTime(time.Now())
	lease.Spec.RenewTime = &now
	newLease, err := client.CoordinationV1().Leases(LeaseNameSpace).Update(context.TODO(), lease, v1.UpdateOptions{})
	if err != nil {
		return nil
	}
	lease = newLease
	return nil
}

// 循环 续租。 都是模拟的。 别纠结什么代码模式
func renew(client *clientset.Clientset) {
	//得到 lease
	getLease, err := client.CoordinationV1().
		Leases(LeaseNameSpace).Get(context.TODO(), LeaseName, v1.GetOptions{})
	checkError(err)
	lease = getLease
	leaseDuration := time.Duration(40) * time.Second
	renewInterval := time.Duration(float64(leaseDuration) * 0.25)
	go func() {
		for {
			err := renewLease(client)
			if err != nil {
				fmt.Println("renew出错:", err)
				break
			}
			time.Sleep(renewInterval)
		}
	}()
	time.Sleep(time.Second * 2) ///为了确保上面的协程执行
}

// 假的 让Node Ready的函数
func StartNode(client *clientset.Clientset) {
	renew(client)
	setNodeReady(client)

}

// 节点状态相关的 模拟代码。都是模拟的。 别纠结什么代码模式
func setNodeReady(client *clientset.Clientset) {
	payload := []Cond{
		{
			Op:   "replace",
			Path: "/status/conditions/3",
			Value: Value{
				Type:   "Ready",
				Status: "True",
			},
		},
	}
	playloadBytes, _ := json.Marshal(payload)
	_, err := client.CoreV1().Nodes().Patch(context.TODO(), "myjtthink",
		types.JSONPatchType, playloadBytes, v1.PatchOptions{}, "status",
	)
	checkError(err)
}
