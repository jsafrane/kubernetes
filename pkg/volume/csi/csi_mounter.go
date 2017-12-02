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

package csi

import (
	"os"

	"github.com/golang/glog"
	api "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/kubernetes"
	kstrings "k8s.io/kubernetes/pkg/util/strings"
	"k8s.io/kubernetes/pkg/volume"
	"k8s.io/kubernetes/pkg/volume/util"
)

type csiMountMgr struct {
	k8s        kubernetes.Interface
	csiClient  csiClient
	plugin     *csiPlugin
	driverName string
	volumeID   string
	readOnly   bool
	spec       *volume.Spec
	pod        *api.Pod
	podUID     types.UID
	options    volume.VolumeOptions
	volumeInfo map[string]string
	volume.MetricsNil
	volumeName string
}

// volume.Volume methods
var _ volume.Volume = &csiMountMgr{}

func (c *csiMountMgr) GetPath() string {
	ret := c.plugin.host.GetPodVolumeDir(c.podUID, kstrings.EscapeQualifiedNameForDisk(csiPluginName), c.volumeName)
	return ret
}

/*
func getTargetPath(uid types.UID, volName string) string {
	// driverName validated at Mounter creation
	// sanitize (replace / with ~) in volumeID before it's appended to path:w
	driverPath := fmt.Sprintf("%s/%s", driverName, kstrings.EscapeQualifiedNameForDisk(volID))
	ret := host.GetPodVolumeDir(uid, kstrings.EscapeQualifiedNameForDisk(csiPluginName), driverPath)
	glog.Infof("JSAF: getTargetPath: uid %s, driver %s, volid %s, host %s, ret %s", uid, driverName, volID, host, ret)
	return ret
}
*/

// volume.Mounter methods
var _ volume.Mounter = &csiMountMgr{}

func (c *csiMountMgr) CanMount() error {
	//TODO (vladimirvivien) use this method to probe controller using CSI.NodeProbe() call
	// to ensure Node service is ready in the CSI plugin
	return nil
}

func (c *csiMountMgr) SetUp(fsGroup *int64) error {
	return c.SetUpAt(c.GetPath(), fsGroup)
}

func (c *csiMountMgr) SetUpAt(dir string, fsGroup *int64) error {
	glog.V(4).Infof(log("JSAF Mounter.SetUpAt(%s)", dir))

	csiSource, err := getCSISourceFromSpec(c.spec)
	if err != nil {
		glog.Error(log("attacher.MountDevice failed to get CSI persistent source: %v", err))
		return err
	}

	mounter := c.plugin.host.GetMounter(csiPluginName)
	notMnt, err := mounter.IsLikelyNotMountPoint(dir)
	glog.V(4).Infof("CSI set up: Dir (%s) name (%q) Mounted (%t) Error (%v), ReadOnly (%t)", dir, c.spec.Name(), !notMnt, err, c.readOnly)
	if err != nil && !os.IsNotExist(err) {
		glog.Errorf("cannot validate mount point: %s %v", dir, err)
		return err
	}
	if !notMnt {
		return nil
	}

	if err := os.MkdirAll(dir, 0750); err != nil {
		glog.Errorf("mkdir failed on disk %s (%v)", dir, err)
		return err
	}

	// Perform a bind mount to the full path to allow duplicate mounts of the same PD.
	options := []string{"bind"}
	if c.readOnly {
		options = append(options, "ro")
	}

	globalPath := getGlobalDeviceMountPath(c.plugin.host, csiSource)
	glog.V(4).Infof("attempting to mount %s", dir)

	err = mounter.Mount(globalPath, dir, "", options)
	if err != nil {
		notMnt, mntErr := mounter.IsLikelyNotMountPoint(dir)
		if mntErr != nil {
			glog.Errorf("IsLikelyNotMountPoint check failed: %v", mntErr)
			return err
		}
		if !notMnt {
			if mntErr = mounter.Unmount(dir); mntErr != nil {
				glog.Errorf("Failed to unmount: %v", mntErr)
				return err
			}
			notMnt, mntErr := mounter.IsLikelyNotMountPoint(dir)
			if mntErr != nil {
				glog.Errorf("IsLikelyNotMountPoint check failed: %v", mntErr)
				return err
			}
			if !notMnt {
				// This is very odd, we don't expect it.  We'll try again next sync loop.
				glog.Errorf("%s is still mounted, despite call to unmount().  Will try again next sync loop.", dir)
				return err
			}
		}
		os.Remove(dir)
		glog.Errorf("Mount of disk %s failed: %v", dir, err)
		return err
	}

	if !c.readOnly {
		volume.SetVolumeOwnership(c, fsGroup)
	}

	glog.V(4).Infof("Successfully mounted %s", dir)
	return nil
}

func (c *csiMountMgr) GetAttributes() volume.Attributes {
	return volume.Attributes{
		ReadOnly:        c.readOnly,
		Managed:         !c.readOnly,
		SupportsSELinux: false,
	}
}

// volume.Unmounter methods
var _ volume.Unmounter = &csiMountMgr{}

func (c *csiMountMgr) TearDown() error {
	return c.TearDownAt(c.GetPath())
}

func (c *csiMountMgr) TearDownAt(dir string) error {
	glog.V(4).Infof(log("JSAF Unmounter.TearDown(%s)", dir))
	mounter := c.plugin.host.GetMounter(csiPluginName)
	return util.UnmountMountPoint(dir, mounter, true /* check for bind mounts */)
}
