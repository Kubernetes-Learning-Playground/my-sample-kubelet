package app

import (
	"fmt"
	"github.com/spf13/cobra"
	"github.com/spf13/pflag"
	"k8s.io/klog/v2"
	"k8s.io/kubernetes/cmd/app/options"
	"k8s.io/kubernetes/pkg/bootstrap"
	client2 "k8s.io/kubernetes/pkg/client"
	"k8s.io/kubernetes/pkg/common"
	"k8s.io/kubernetes/pkg/mycore"
	"k8s.io/kubernetes/pkg/node"
	"k8s.io/kubernetes/pkg/node/lease"
	"os"
)

// NewKubeletCommand 启动kubelet
func NewKubeletCommand() *cobra.Command {
	// 配置文件
	s, err := options.NewKubeControllerManagerOptions()
	if err != nil {
		klog.Fatalf("unable to initialize command options: %v", err)
	}

	cmd := &cobra.Command{
		Use: "my sample kubelet",
		RunE: func(cmd *cobra.Command, args []string) error {
			klog.InitFlags(nil)
			// 1. 引入配置文件
			cfg := s.Config().Complete()

			// 2. 启动kubelet crs 批复流程
			err = bootstrap.BootStrap(cfg.Token, cfg.NodeName, cfg.ApiServerEndpoint)
			if err != nil {
				return err
			}

			// 3. 初始化客户端
			client := client2.InitClient()
			kubeClient, err := common.NewForKubeletConfig()
			if err != nil {
				return err
			}

			// 4. 注册node节点
			node.RegisterNode(cfg.NodeName, kubeClient)

			// 5. 启动租约控制器
			// 更新node的状态信息，如果没有，就会改成notReady
			lease.StartLeaseController(kubeClient, cfg.NodeName)

			// 6. 初始化kubelet
			// 启动kubelet Start() 此方法会阻塞
			k := mycore.NewSampleKubelet(client, cfg.NodeName)
			k.Start()

			return nil
		},
		Args: func(cmd *cobra.Command, args []string) error {
			for _, arg := range args {
				if len(arg) > 0 {
					return fmt.Errorf("%q does not take any arguments, got %q", cmd.CommandPath(), args)
				}
			}
			return nil
		},
	}

	flags := pflag.NewFlagSet(os.Args[0], pflag.ExitOnError)
	s.AddFlags(flags)
	flags.Parse(os.Args[1:])
	flags.VisitAll(func(f *pflag.Flag) {
		klog.Infof("Flag: %v=%v\n", f.Name, f.Value.String())
	})

	fs := cmd.Flags()
	fs.AddFlagSet(flags)

	return cmd
}
