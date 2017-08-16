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

package main

import (
	"bytes"
	"fmt"
	"strings"
	"time"

	"github.com/golang/glog"
	"golang.org/x/net/context"

	"k8s.io/kubernetes/pkg/kubelet/apis/cri/v1alpha1/runtime"
	"k8s.io/utils/exec"
)

// ExecService is service that implements gRPC ExecService
type ExecService struct {
}

var _ runtime.ExecServiceServer = &ExecService{}

func NewExecService() *ExecService {
	return &ExecService{}
}

func (svc *ExecService) ExecSync(ctx context.Context, req *runtime.ExecSyncRequest) (*runtime.ExecSyncResponse, error) {
	err, exitcode, stdout, stderr := svc.doExec(req.GetCmd(), req.GetTimeout())
	if err != nil {
		return nil, err
	}
	return &runtime.ExecSyncResponse{
		ExitCode: exitcode,
		Stdout:   stdout,
		Stderr:   stderr,
	}, nil
}

func (svc *ExecService) doExec(cmd []string, timeout int64) (error, int32, []byte, []byte) {
	glog.V(2).Infof("executing %s", strings.Join(cmd, " "))

	if len(cmd) == 0 {
		return fmt.Errorf("Missing command"), 0, nil, nil
	}
	c := exec.New().Command(cmd[0], cmd[1:]...)
	var stdout, stderr bytes.Buffer
	c.SetStdout(&stdout)
	c.SetStderr(&stderr)

	// Run the command in background.
	done := make(chan error, 1)
	go func() {
		done <- c.Run()
	}()

	// Start a timer to kill the command after timeout.
	var timer *time.Timer
	if timeout > 0 {
		timer = time.AfterFunc(time.Duration(timeout)*time.Second, func() {
			glog.V(2).Infof("%s timed out, stopping", cmd[0])
			c.Stop()
		})
	}
	// Wait until the command finishes - it either exits naturally or is killed
	// by the timer above.
	err := <-done
	if timer != nil {
		timer.Stop()
	}

	glog.V(2).Infof("got error %v", err)
	glog.V(3).Infof("stdout: %s", string(stdout.String()))
	glog.V(3).Infof("stderr: %s", string(stderr.String()))
	if err != nil {
		if exitErr, ok := err.(exec.ExitError); ok {
			return nil, int32(exitErr.ExitStatus()), stdout.Bytes(), stderr.Bytes()
		}
		return err, -1, stdout.Bytes(), stderr.Bytes()
	}
	return nil, 0, stdout.Bytes(), stderr.Bytes()
}
