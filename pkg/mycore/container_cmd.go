package mycore

import (
	"os"
	"os/exec"
)

// ContainerCmd 针对每个容器的执行命令
type ContainerCmd struct {
	Cmd           *exec.Cmd `json:"cmd"`
	ContainerName string    `json:"container_name"`
	ExitCode      int       `json:"exit_code"`
	ExecError     error     `json:"exec_error"`
}

// Run 执行命令
func (cc *ContainerCmd) Run() {
	// 标准输出
	cc.Cmd.Stdout = os.Stdout
	cc.Cmd.Stderr = os.Stderr
	// 执行cmd
	err := cc.Cmd.Run()
	if err != nil {
		if exitError, ok := err.(*exec.ExitError); ok {
			exitCode := exitError.ExitCode()
			cc.ExitCode = exitCode
		} else {
			cc.ExitCode = -9999 //代表是其他错误
			cc.ExecError = err
		}
	}
}
