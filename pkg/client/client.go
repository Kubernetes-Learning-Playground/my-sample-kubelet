package client

import (
	"k8s.io/client-go/kubernetes"
	clientset "k8s.io/client-go/kubernetes"
	"k8s.io/client-go/tools/clientcmd"
	"log"
)

// InitClient 初始化客户端
func InitClient() *kubernetes.Clientset {
	restConfig, err := clientcmd.BuildConfigFromFlags("", "./resources/config1")
	if err != nil {
		log.Fatal(err)
	}
	restConfig.Insecure = true

	client, err := clientset.NewForConfig(restConfig)
	if err != nil {
		log.Fatal(err)
	}
	return client
}
