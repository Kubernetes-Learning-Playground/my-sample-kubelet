/*
Copyright 2015 The Kubernetes Authors.

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

    http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software
distributed under the License is distributed on an "AS IS" BASIS,
WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
See the License for the specific language governing permissions and
limitations under the License.
*/

package exec

import (
	"k8s.io/kubernetes/pkg/probe"

	"k8s.io/utils/exec"
)

const (
	maxReadLength = 10 * 1 << 10 // 10KB
)

// New creates a Prober.
func New() Prober {
	return execProber{}
}

// Prober is an interface defining the Probe object for container readiness/liveness checks.
type Prober interface {
	Probe(e exec.Cmd) (probe.Result, string, error)
}

type execProber struct{}

// 不支持
func (pr execProber) Probe(e exec.Cmd) (probe.Result, string, error) {

	return probe.Success, string(""), nil
}
