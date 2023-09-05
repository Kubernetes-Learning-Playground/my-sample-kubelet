package main

import (
	"k8s.io/component-base/cli"
	"k8s.io/kubernetes/cmd/app"
	"os"
)

func main() {
	command := app.NewKubeletCommand()
	code := cli.Run(command)
	os.Exit(code)
}
