package util

import (
	"errors"
	"os"
)

const (
	// config文件存放位置，并且会读取该目录的key pem文件
	KubeletConfig = "./cert/kubelet.config"
)

// NeedRequestCSR 是否要请求csr证书
// 判断kubelet.config是否存在
func NeedRequestCSR() bool {
	if _, err := os.Stat(KubeletConfig); errors.Is(err, os.ErrNotExist) {
		return true
	}
	return false
}
