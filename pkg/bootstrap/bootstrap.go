package bootstrap

import (
	"k8s.io/klog/v2"
	"k8s.io/kubernetes/pkg/bootstrap/csr"
	"k8s.io/kubernetes/pkg/common"
	"k8s.io/kubernetes/pkg/util"
)

/*
	// 暂时的kubeadm token
	// kubeadm token list
	// kubeadm token create

	使用kubelet批复的过程
	[root@VM-0-16-centos ~]# kubectl delete csr myk8s
	certificatesigningrequest.certificates.k8s.io "myk8s" deleted
	[root@VM-0-16-centos ~]# kubectl certificate approve myk8s
	certificatesigningrequest.certificates.k8s.io/myk8s approved
	[root@VM-0-16-centos ~]# kubectl certificate approve myk8s^C
	[root@VM-0-16-centos ~]# kubectl get csr
	NAME    AGE     SIGNERNAME                            REQUESTOR                 REQUESTEDDURATION   CONDITION
	myk8s   3m22s   kubernetes.io/kube-apiserver-client   system:bootstrap:wffenf   365d                Approved,Issued
	[root@VM-0-16-centos ~]#
*/

// BootStrap 处理证书相关的操作
func BootStrap(token, nodeName, masterUrl string) error {
	// 1. 启动节点时，先检查是否要重新创建csr
	if !util.NeedRequestCSR() {
		klog.Infoln("kubelet.config already exists. skip csr-boot")
		return nil
	}
	klog.Infoln("begin csr bootstrap...")
	// 2. 创建boot client and 创建 CSR Cert对象
	bootClient := common.NewForBootStrapToken(token, masterUrl)
	csrObj, err := csr.CreateCSRCert(bootClient, nodeName)
	if err != nil {
		klog.Errorf("create csr cert error: %s", err)
		return err
	}
	// 3. 等待批复，超时时间60秒，默认使用手工批复
	err = csr.WaitForCSRApprove(csrObj, csr.CsrWaitingTimeout, bootClient)
	if err != nil {
		klog.Errorf("wait for csr approve timeout: %s", err)
		return err
	}

	klog.Infoln("kubelet pem-files have been saved in .kube ")

	// 4. 生成kubelet config文件
	err = csr.GenKubeletConfig(masterUrl)
	if err != nil {
		klog.Errorf("gen kubeletConfig error: %s", err)
		return err
	}

	// 5. 测试客户端
	klog.Infoln("testing kube client")
	client, err := common.NewForKubeletConfig()
	if err != nil {
		klog.Errorf("new kubeletConfig error: %s", err)
		return err
	}

	info, err := client.ServerVersion()
	if err != nil {
		klog.Errorf("test client error: %s", err)
		return err
	}

	klog.Info(info.String())

	return nil

}
