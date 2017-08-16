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
	"flag"
	"fmt"
	"net"
	"os"
	"path"
	"syscall"

	"github.com/golang/glog"
	"google.golang.org/grpc"

	"k8s.io/kubernetes/pkg/kubelet"
	"k8s.io/kubernetes/pkg/kubelet/apis/cri/v1alpha1/runtime"
	"k8s.io/kubernetes/pkg/util/interrupt"
)

// ExecServer is the grpc server of ExecService API. Each volume plugin
// has its own ExecServer as it serves different socket.
type ExecServer struct {
	// pluginName is name of the volume plugin
	pluginName string
	// socketName is path to UNIX socket where the server should listen.
	socketName string
	// server is the grpc server.
	server *grpc.Server
	// execService is implementation of the volumeexec API
	execService runtime.ExecServiceServer
	// finishedCh is a channel that the server closes when has finished
	finishedCh chan struct{}
}

// NewVolumeExecDaemon creates the volumeexec grpc server.
func NewExecServer(pluginName string, socketName string, finishedCh chan struct{}) *ExecServer {
	return &ExecServer{
		pluginName:  pluginName,
		socketName:  socketName,
		execService: NewExecService(),
		finishedCh:  finishedCh,
	}
}

// Start starts the volumeexec grpc server.
func (srv *ExecServer) Start() error {
	glog.V(2).Infof("%s: staring grpc server", srv.pluginName)

	listener, err := srv.Listen()
	if err != nil {
		return fmt.Errorf("%s: failed to listen on %q: %v", srv.pluginName, srv.socketName, err)
	}

	// Create the grpc server and register runtime and image services.
	srv.server = grpc.NewServer()
	runtime.RegisterExecServiceServer(srv.server, srv.execService)
	go func() {
		// Use interrupt handler to make sure the servers are stopped properly.
		handler := interrupt.New(func(_ os.Signal) {
			glog.Infof("%s: server interrupted, closing", srv.pluginName)
			close(srv.finishedCh)
		})
		err := handler.Run(func() error { return srv.server.Serve(listener) })
		if err != nil {
			glog.Errorf("%s: failed to serve connections: %v", srv.pluginName, err)
		}
		close(srv.finishedCh)
	}()
	return nil
}

func (srv *ExecServer) Listen() (net.Listener, error) {

	// Create directory for the socket if it does not exist yet
	dir := path.Dir(srv.socketName)
	if err := os.MkdirAll(dir, 0700); err != nil {
		return nil, fmt.Errorf("failed to create directory %q: %v", dir, err)
	}

	// Unlink to cleanup the previous socket file.
	err := syscall.Unlink(srv.socketName)
	if err != nil && !os.IsNotExist(err) {
		return nil, fmt.Errorf("failed to unlink socket file %q: %v", srv.socketName, err)
	}

	return net.Listen("unix", srv.socketName)
}

// Stop stops the volumeexec grpc server.
func (srv *ExecServer) Stop() {
	glog.V(2).Infof("%s: stop server", srv.pluginName)
	srv.server.Stop()
	// Clean up the socket file
	syscall.Unlink(srv.socketName)
}

func usage() {
	fmt.Fprintf(os.Stderr, "Usage of %s:\n", os.Args[0])
	fmt.Fprintf(os.Stderr, "%s [options] <volume plugin> ...\n", os.Args[0])
	fmt.Fprintf(os.Stderr, "\n")
	fmt.Fprintf(os.Stderr, "Options:\n")
	flag.PrintDefaults()
}

func main() {
	var (
		rootDir = flag.String("root-dir", "/var/lib/kubelet", "Kubelet root directory.")
	)
	flag.Usage = usage
	flag.Parse()

	args := flag.Args()
	if len(args) < 1 {
		fmt.Fprintf(os.Stderr, "At least one volume plugin must be specified\n")
		usage()
		os.Exit(1)
	}

	stopCh := make(chan struct{})

	servers := make([]*ExecServer, 0, len(args))
	for _, pluginName := range args {
		socketName := kubelet.GetVolumePluginSocketPath(*rootDir, pluginName)
		srv := NewExecServer(pluginName, socketName, stopCh)
		servers = append(servers, srv)
		err := srv.Start()
		if err != nil {
			fmt.Fprintf(os.Stderr, "%v\n", err)
			os.Exit(2)
		}
	}

	// Wait for any volume plugin to finish.
	<-stopCh
	// Stop all the plugins when one of them finished.
	for _, srv := range servers {
		srv.Stop()
	}
}
