package csr

import "time"

const (
	CSR_DURATION            = time.Second * 3600 * 24 * 365 //CSR的过期时间
	PrivateKeyFileName      = "kubelet.key"
	PemFileName             = "kubelet.pem"
	BootstrapPrivatekeyFile = "./cert/" + PrivateKeyFileName
	BootstrapPemFile        = "./cert/" + PemFileName
	BootstrapPrivatekeyType = "RSA PRIVATE KEY"
	CsrWaitingTimeout       = time.Second * 60 //默认60秒 超时

)
