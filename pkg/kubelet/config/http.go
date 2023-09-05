/*
Copyright 2014 The Kubernetes Authors.

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

package config

import (
	"net/http"
	"time"

	"k8s.io/apimachinery/pkg/types"
	"k8s.io/apimachinery/pkg/util/wait"
	"k8s.io/klog/v2"
	api "k8s.io/kubernetes/pkg/apis/core"
)

type sourceURL struct {
	url         string
	header      http.Header
	nodeName    types.NodeName
	updates     chan<- interface{}
	data        []byte
	failureLogs int
	client      *http.Client
}

// NewSourceURL specifies the URL where to read the Pod configuration from, then watches it for changes.
func NewSourceURL(url string, header http.Header, nodeName types.NodeName, period time.Duration, updates chan<- interface{}) {
	config := &sourceURL{
		url:      url,
		header:   header,
		nodeName: nodeName,
		updates:  updates,
		data:     nil,
		// Timing out requests leads to retries. This client is only used to
		// read the manifest URL passed to kubelet.
		client: &http.Client{Timeout: 10 * time.Second},
	}
	klog.V(1).InfoS("Watching URL", "URL", url)
	go wait.Until(config.run, period, wait.NeverStop)
}

func (s *sourceURL) run() {
	if err := s.extractFromURL(); err != nil {
		// Don't log this multiple times per minute. The first few entries should be
		// enough to get the point across.
		if s.failureLogs < 3 {
			klog.InfoS("Failed to read pods from URL", "err", err)
		} else if s.failureLogs == 3 {
			klog.InfoS("Failed to read pods from URL. Dropping verbosity of this message to V(4)", "err", err)
		} else {
			klog.V(4).InfoS("Failed to read pods from URL", "err", err)
		}
		s.failureLogs++
	} else {
		if s.failureLogs > 0 {
			klog.InfoS("Successfully read pods from URL")
			s.failureLogs = 0
		}
	}
}

func (s *sourceURL) applyDefaults(pod *api.Pod) error {
	return applyDefaults(pod, s.url, false, s.nodeName)
}

func (s *sourceURL) extractFromURL() error {

	return nil
}
