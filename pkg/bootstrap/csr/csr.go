package csr

import (
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	cryptorand "crypto/rand"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"fmt"
	certificatesv1 "k8s.io/api/certificates/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes"
	apiv1 "k8s.io/client-go/tools/clientcmd/api/v1"
	"k8s.io/client-go/util/cert"
	"k8s.io/client-go/util/certificate/csr"
	"k8s.io/klog/v2"
	"k8s.io/kubernetes/pkg/util"
	"os"
	"sigs.k8s.io/yaml"
	"time"
)

// savePrivateKeyToFile 把私钥保存为文件
func savePrivateKeyToFile(key *ecdsa.PrivateKey) error {
	b, err := x509.MarshalECPrivateKey(key)
	if err != nil {
		return err
	}
	privkeyPem := pem.EncodeToMemory(
		&pem.Block{
			Type:  BootstrapPrivatekeyType,
			Bytes: b,
		},
	)
	_ = os.Remove(BootstrapPrivatekeyFile)
	err = os.WriteFile(BootstrapPrivatekeyFile, privkeyPem, 0600)
	if err != nil {
		return err
	}
	return nil
}

// GenCSRPEM 生成csr证书请求文件 用于 equest字段的填充
func GenCSRPEM(nodeName string) ([]byte, error) {
	cr := &x509.CertificateRequest{
		Subject: pkix.Name{
			CommonName:   fmt.Sprintf("system:node:%s", nodeName),
			Organization: []string{"system:nodes"},
		},
	}

	privateKey, err := ecdsa.GenerateKey(elliptic.P256(), cryptorand.Reader)
	if err != nil {
		return nil, err
	}
	// 保存私钥为 kubelet.key
	err = savePrivateKeyToFile(privateKey)
	if err != nil {
		return nil, err
	}
	csrPEM, err := cert.MakeCSRFromTemplate(privateKey, cr)
	if err != nil {
		return nil, err
	}

	return csrPEM, nil
}

// CreateCSRCert 创建certificates.k8s.io/v1  CertificateSigningRequest 对象
func CreateCSRCert(client *kubernetes.Clientset, nodeName string) (*certificatesv1.CertificateSigningRequest, error) {
	csrpem, err := GenCSRPEM(nodeName)
	if err != nil {
		fmt.Println("sss", err)
		return nil, err
	}
	csrObj := &certificatesv1.CertificateSigningRequest{
		// Username, UID, Groups will be injected by API server.
		TypeMeta: metav1.TypeMeta{Kind: "CertificateSigningRequest"},
		ObjectMeta: metav1.ObjectMeta{
			Name: nodeName,
		},
		Spec: certificatesv1.CertificateSigningRequestSpec{
			Request: csrpem,
			Usages: []certificatesv1.KeyUsage{
				certificatesv1.UsageClientAuth,
			},
			ExpirationSeconds: DurationToExpirationSeconds(CSR_DURATION),
			SignerName:        certificatesv1.KubeAPIServerClientSignerName,
		},
	}
	csrRet, err := client.CertificatesV1().CertificateSigningRequests().Create(context.Background(), csrObj, metav1.CreateOptions{})
	if err != nil {
		fmt.Println("ddd", err)
		return nil, err
	}
	return csrRet, nil
}

func WaitForCSRApprove(csrObj *certificatesv1.CertificateSigningRequest, timeout time.Duration, client *kubernetes.Clientset) error {
	ctx, cancel := context.WithTimeout(context.TODO(), timeout)
	defer cancel()
	klog.Info("waiting for csr is approved....")
	csrData, err := csr.WaitForCertificate(ctx, client, csrObj.Name, csrObj.UID)
	if err != nil {
		klog.V(3).ErrorS(err, "approved timeout")
		return err
	}
	err = os.WriteFile(BootstrapPemFile, csrData, 0600)
	return err
}

// GenKubeletConfig 生成 kubeconfig 文件， 生成到.kube/kubelet.config
func GenKubeletConfig(masterUrl string) error {
	contextName := "default-context"
	clusterName := "default-cluster"
	authName := "default-auth"
	cfg := apiv1.Config{}
	cfg.Clusters = []apiv1.NamedCluster{
		{
			Name: clusterName,
			Cluster: apiv1.Cluster{
				Server:                masterUrl,
				InsecureSkipTLSVerify: true, // 跳过证书
			},
		},
	}
	cfg.Kind = "Config"
	cfg.APIVersion = "v1"
	cfg.Contexts = []apiv1.NamedContext{
		{
			Name: contextName,
			Context: apiv1.Context{
				Cluster:  clusterName,
				AuthInfo: authName,
			},
		},
	}
	cfg.AuthInfos = []apiv1.NamedAuthInfo{
		{
			Name: authName,
			AuthInfo: apiv1.AuthInfo{
				ClientCertificate: PemFileName,
				ClientKey:         PrivateKeyFileName,
			},
		},
	}

	cfg.CurrentContext = contextName

	b, err := yaml.Marshal(cfg)
	if err != nil {
		klog.Fatalln(err)
	}
	klog.Infoln("writing kubelet-config to ", util.KubeletConfig)
	err = os.WriteFile(util.KubeletConfig, b, 0600)
	if err != nil {
		return err
	}
	return nil

}
