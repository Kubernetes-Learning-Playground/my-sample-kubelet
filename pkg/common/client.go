package common

import (
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/rest"
	"k8s.io/client-go/tools/clientcmd"
	"k8s.io/klog/v2"
	"k8s.io/kubernetes/pkg/util"
	"net/url"
)

// NewForKubeletConfig 依赖kubelet配置文件生成客户端
func NewForKubeletConfig() (*kubernetes.Clientset, error) {
	restCfg, err := clientcmd.BuildConfigFromFlags("", util.KubeletConfig)
	if err != nil {
		return nil, err
	}
	client, err := kubernetes.NewForConfig(restCfg)

	if err != nil {
		return nil, err
	}
	return client, nil
}

// NewForBootStrapToken 根据token创建低权限的client
func NewForBootStrapToken(token string, masterUrl string) *kubernetes.Clientset {
	urlObj, err := url.Parse(masterUrl)
	if err != nil || token == "" {
		klog.Fatalln("parse url error or token empty: ", err)
	}
	restConfig := rest.Config{
		BearerToken: token,
		Host:        urlObj.Host,
		APIPath:     urlObj.Path,
	}

	// 跳过证书
	restConfig.Insecure = true
	client, err := kubernetes.NewForConfig(&restConfig)

	if err != nil {
		klog.Fatalln(err)
	}
	klog.V(3).Info("create clientset by bootstrap token")
	return client
}
