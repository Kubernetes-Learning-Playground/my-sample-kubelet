package mycore

import (
	v1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/informers"
	"k8s.io/client-go/kubernetes"
	typedcorev1 "k8s.io/client-go/kubernetes/typed/core/v1"
	"k8s.io/client-go/tools/cache"
	"k8s.io/client-go/tools/record"
	"k8s.io/kubernetes/pkg/api/legacyscheme"
	corev1 "k8s.io/kubernetes/pkg/apis/core/v1"
	"k8s.io/kubernetes/pkg/kubelet/config"
	"k8s.io/kubernetes/pkg/kubelet/configmap"
	kubecontainer "k8s.io/kubernetes/pkg/kubelet/container"
	kubepod "k8s.io/kubernetes/pkg/kubelet/pod"
	"k8s.io/kubernetes/pkg/kubelet/secret"
	"k8s.io/kubernetes/pkg/kubelet/status"
	kubetypes "k8s.io/kubernetes/pkg/kubelet/types"
	"k8s.io/utils/clock"
)

// 就是官方的 PodManager  做一些改造
type PodCache struct {
	client     *kubernetes.Clientset
	PodManager kubepod.Manager
	PodWorkers PodWorkers
	PodConfig  *config.PodConfig //  configCh file http  apiserver (重点是apiserver)

	Clock         clock.RealClock     //时钟对象
	InnerPodCache kubecontainer.Cache //内部 POD 对象 。存的是 POD 和 状态之间的对应关系

}

// 所谓的构造函数
func NewPodCache(client *kubernetes.Clientset, nodeName string) *PodCache {
	ch := make(chan struct{})
	fact := informers.NewSharedInformerFactory(client, 0)
	fact.Core().V1().Nodes().Informer().AddEventHandler(cache.ResourceEventHandlerFuncs{})
	fact.Start(ch)

	nodeLister := fact.Core().V1().Nodes().Lister()
	mirrorPodClient := kubepod.NewBasicMirrorClient(client, nodeName, nodeLister)
	secretManager := secret.NewSimpleSecretManager(client)
	configMapManager := configmap.NewSimpleConfigMapManager(client)
	podManager := kubepod.NewBasicPodManager(mirrorPodClient, secretManager, configMapManager)

	cl := clock.RealClock{}
	//下面是创建PodWorker 对象 。 注意：使用的是自己的。 源码里是私有没法调用
	eventBroadcaster := record.NewBroadcaster() // 事件分发器 广播
	eventRecorder := eventBroadcaster.NewRecorder(legacyscheme.Scheme, v1.EventSource{Component: "kubelet", Host: nodeName})

	//下面是创建PodWorker 对象 。 注意：使用的是自己的。 源码里是私有没法调用
	_ = corev1.AddToScheme(legacyscheme.Scheme)
	eventBroadcaster.StartRecordingToSink(&typedcorev1.EventSinkImpl{Interface: client.CoreV1().Events("")})

	innerPodCache := kubecontainer.NewCache() // 内部podcache 用于记录pod和状态 对应关心

	// 创建 status_manager
	statusManager := status.NewManager(client, podManager, &PodDeletionSafetyProviderStruct{})
	statusManager.Start()
	pw := NewPodWorkers(innerPodCache, eventRecorder, cl, client, statusManager, podManager)

	return &PodCache{
		Clock:         cl,
		client:        client,
		PodManager:    podManager,
		PodConfig:     newPodConfig(nodeName, client, fact, eventRecorder),
		PodWorkers:    pw,
		InnerPodCache: innerPodCache,
	}
}

// 创建PodConfig
func newPodConfig(nodeName string, client *kubernetes.Clientset,
	fact informers.SharedInformerFactory, recorder record.EventRecorder) *config.PodConfig {

	cfg := config.NewPodConfig(config.PodConfigNotificationIncremental, recorder)

	config.NewSourceApiserver(client, types.NodeName(nodeName),
		func() bool {
			return fact.Core().V1().Nodes().Informer().HasSynced()
		}, cfg.Channel(kubetypes.ApiserverSource))
	return cfg
}
