package options

import (
	"flag"
	"fmt"
	"github.com/spf13/pflag"
	"k8s.io/klog/v2"
	"k8s.io/kubernetes/cmd/app/config"
	"os"
	"strings"
)

type SampleKubeletOptions struct {
	NodeName          string
	ApiServerEndpoint string
	Token             string
}

// NewKubeControllerManagerOptions creates a new KubeControllerManagerOptions with a default config.
func NewKubeControllerManagerOptions() (*SampleKubeletOptions, error) {
	s := SampleKubeletOptions{}
	return &s, nil
}

func (s SampleKubeletOptions) Config() *config.Config {
	c := &config.Config{
		NodeName:          s.NodeName,
		ApiServerEndpoint: fmt.Sprintf("https://%s", s.ApiServerEndpoint),
		Token:             s.Token,
	}
	return c
}

const (
	DefaultNodeName          = "my-sample-kubelet"
	DefaultApiServerEndpoint = "127.0.0.1:6443"
)

// AddFlags 加入命令行参数
func (s *SampleKubeletOptions) AddFlags(flags *pflag.FlagSet) {
	flags.StringVar(&s.NodeName, "nodeName", DefaultNodeName, "kubelet name")
	flags.StringVar(&s.ApiServerEndpoint, "apiserver-endpoint", DefaultApiServerEndpoint, "api-server-endpoint")
	flags.StringVar(&s.Token, "token", "", "kubeadm token")

	s.addKlogFlags(flags)
}

func (s *SampleKubeletOptions) addKlogFlags(flags *pflag.FlagSet) {
	klogFlags := flag.NewFlagSet(os.Args[0], flag.ExitOnError)
	klog.InitFlags(klogFlags)

	klogFlags.VisitAll(func(f *flag.Flag) {
		f.Name = fmt.Sprintf("klog-%s", strings.ReplaceAll(f.Name, "_", "-"))
	})
	flags.AddGoFlagSet(klogFlags)
}
