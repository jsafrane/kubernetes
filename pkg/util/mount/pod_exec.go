/*
Copyright 2017 The Kubernetes Authors.

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

package mount

import (
	"bytes"
	"strings"

	"github.com/golang/glog"
	remotecommandconsts "k8s.io/apimachinery/pkg/util/remotecommand"
	restclient "k8s.io/client-go/rest"
	"k8s.io/client-go/tools/remotecommand"
	"k8s.io/kubernetes/pkg/api"
	"k8s.io/kubernetes/pkg/api/v1"
	"k8s.io/kubernetes/pkg/client/clientset_generated/clientset"
)

// NewPodExec returns Exec interface that executes commands in a remote
// pod, just like "kubectl exec <pod> <cmd>".
func NewPodExec(pod *v1.Pod, kubeClient clientset.Interface, restConfig *restclient.Config) Exec {
	return &podExec{
		pod:        pod,
		kubeClient: kubeClient,
		restConfig: restConfig,
	}
}

type podExec struct {
	pod        *v1.Pod
	kubeClient clientset.Interface
	restConfig *restclient.Config
}

var _ Exec = &podExec{}

func (e *podExec) Run(cmd string, args ...string) ([]byte, error) {
	containerName := e.pod.Spec.Containers[0].Name
	cmdline := append([]string{cmd}, args...)
	glog.V(5).Infof("Running %q in pod %s/%s", strings.Join(cmdline, " "), e.pod.Namespace, e.pod.Name)

	client := e.kubeClient.Core().RESTClient()
	req := client.Post().
		Resource("pods").
		Name(e.pod.Name).
		Namespace(e.pod.Namespace).
		SubResource("exec").
		Param("container", containerName)
	req.VersionedParams(&api.PodExecOptions{
		Container: containerName,
		Command:   cmdline,
		Stdin:     false,
		Stdout:    true,
		Stderr:    true,
		TTY:       false,
	}, api.ParameterCodec)

	exec, err := remotecommand.NewExecutor(e.restConfig, "POST", req.URL())
	if err != nil {
		return nil, err
	}
	var out bytes.Buffer
	err = exec.Stream(remotecommand.StreamOptions{
		SupportedProtocols: remotecommandconsts.SupportedStreamingProtocols,
		Stdin:              nil,
		Stdout:             &out,
		Stderr:             &out,
		Tty:                false,
		TerminalSizeQueue:  nil,
	})
	return out.Bytes(), err
}
