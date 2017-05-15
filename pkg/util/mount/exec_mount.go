// +build linux

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
	"fmt"
	"time"

	"github.com/golang/glog"
)

// ExecMounter is a mounter that uses provided Exec interface to mount and
// unmount a filesystem. For all other calls it uses a wrapped mounter.
type execMounter struct {
	wrappedMounter Interface
	exec           Exec
}

type Exec interface {
	Run(cmd string, args []string, timeout time.Duration) ([]byte, error)
}

func NewExecMounter(exec Exec, wrapped Interface) Interface {
	return &execMounter{
		wrappedMounter: wrapped,
		exec:           exec,
	}
}

// NsenterMounter implements mount.Interface
var _ Interface = &execMounter{}

const defaultExecTimeout = 5 * time.Minute

// Mount runs mount(8) in the host's root mount namespace.  Aside from this
// aspect, Mount has the same semantics as the mounter returned by mount.New()
func (m *execMounter) Mount(source string, target string, fstype string, options []string) error {
	bind, bindRemountOpts := isBind(options)

	if bind {
		err := m.doExecMount(source, target, fstype, []string{"bind"})
		if err != nil {
			return err
		}
		return m.doExecMount(source, target, fstype, bindRemountOpts)
	}

	return m.doExecMount(source, target, fstype, options)
}

// doNsenterMount nsenters the host's mount namespace and performs the
// requested mount.
func (m *execMounter) doExecMount(source, target, fstype string, options []string) error {
	glog.V(5).Infof("Exec Mounting %s %s %s %v", source, target, fstype, options)
	mountArgs := makeMountArgs(source, target, fstype, options)
	output, err := m.exec.Run("mount", mountArgs, defaultExecTimeout)
	glog.V(5).Infof("Exec mounted %v: %v: %s", mountArgs, err, string(output))
	if err != nil {
		glog.Errorf("Mount failed: %v\nMounting command: %s\nMounting arguments: %s %s %s %v\nOutput: %s\n", err, "mount", source, target, fstype, options, string(output))
		return fmt.Errorf("mount failed: %v\nMounting command: %s\nMounting arguments: %s %s %s %v\nOutput: %s\n",
			err, "mount", source, target, fstype, options, string(output))
	}

	return err
}

// Unmount runs umount(8) in the host's mount namespace.
func (m *execMounter) Unmount(target string) error {
	outputBytes, err := m.exec.Run("umount", []string{target}, defaultExecTimeout)
	glog.V(5).Infof("Exec unmounted %v: %s", err, string(outputBytes))
	if len(outputBytes) != 0 {
		glog.V(5).Infof("Output of unmounting %s: %v", target, string(outputBytes))
	}

	return err
}

// List returns a list of all mounted filesystems in the host's mount namespace.
func (m *execMounter) List() ([]MountPoint, error) {
	return m.wrappedMounter.List()
}

// IsLikelyNotMountPoint determines whether a path is a mountpoint by calling findmnt
// in the host's root mount namespace.
func (m *execMounter) IsLikelyNotMountPoint(file string) (bool, error) {
	return m.wrappedMounter.IsLikelyNotMountPoint(file)
}

// DeviceOpened checks if block device in use by calling Open with O_EXCL flag.
// Returns true if open returns errno EBUSY, and false if errno is nil.
// Returns an error if errno is any error other than EBUSY.
// Returns with error if pathname is not a device.
func (m *execMounter) DeviceOpened(pathname string) (bool, error) {
	return exclusiveOpenFailsOnDevice(pathname)
}

// PathIsDevice uses FileInfo returned from os.Stat to check if path refers
// to a device.
func (m *execMounter) PathIsDevice(pathname string) (bool, error) {
	return pathIsDevice(pathname)
}

//GetDeviceNameFromMount given a mount point, find the volume id from checking /proc/mounts
func (m *execMounter) GetDeviceNameFromMount(mountPath, pluginDir string) (string, error) {
	return m.wrappedMounter.GetDeviceNameFromMount(mountPath, pluginDir)
}

func (m *execMounter) MakeShared(path string) error {
	return m.wrappedMounter.MakeShared(path)
}

func (m *execMounter) Exec(cmd string, args []string) ([]byte, error) {
	return m.exec.Run(cmd, args, defaultExecTimeout)
}
