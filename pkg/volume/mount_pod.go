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

package volume

import (
	"sync"

	"github.com/golang/glog"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/kubernetes/pkg/api/v1"
)

// MountPodManager is a cache of mount pods. Its AddPod and DeletePod should be
// called when a pod appears, changes state or disapears in API server.
// It then can return a mount pod for given plugin name.
type MountPodManager interface {
	// AddPod adds or updates a pod in cache of mount pods. This method should
	// be called for all created or updated pods.
	AddPod(pod *v1.Pod)
	// DeletePod removes a pod from cache of mount pods.
	DeletePod(pod *v1.Pod)
	// GetPod returns a random running mount pod that handles given plugin
	GetPod(pluginName string) *v1.Pod
}

func NewMountPodManager(pluginMgr *VolumePluginMgr, watchedNamespace string) MountPodManager {
	return &mountPodManager{
		pluginMgr: pluginMgr,
		namespace: watchedNamespace,
		pods:      map[string]map[types.UID]*v1.Pod{},
	}
}

// mountPodManager is implementation of MountPodManager that keeps track of
// mount pods in given namespace.
type mountPodManager struct {
	mutex     sync.Mutex
	pluginMgr *VolumePluginMgr
	namespace string
	// pods is a cache of mount pods for given plugin name. We index the pods by
	// UID for easier lookup and deletion.
	pods map[string]map[types.UID]*v1.Pod
}

var _ MountPodManager = &mountPodManager{}

func (m *mountPodManager) AddPod(pod *v1.Pod) {
	m.mutex.Lock()
	defer m.mutex.Unlock()

	if pod.Namespace != m.namespace {
		//  ignore pods from unknown namespaces
		return
	}
	// Remove the old version of the pod from cache just in case a label was
	// removed from the pod or the pod is not running.
	m.deletePodLocked(pod)

	if pod.Status.Phase != v1.PodRunning {
		glog.V(5).Infof("AddMountPod skipping pod %s/%s: it's %s", pod.Namespace, pod.Name, pod.Status.Phase)
		return
	}

	// Find all volume plugins that are handled by this pod
	pluginNames := m.pluginMgr.GetPluginNames()
	for _, pluginName := range pluginNames {
		if pod.Labels["mount."+pluginName] == "true" {
			pods := m.pods[pluginName]
			if pods == nil {
				pods = map[types.UID]*v1.Pod{}
			}
			pods[pod.UID] = pod
			m.pods[pluginName] = pods
			glog.V(5).Infof("AddMountPod: added %s/%s for plugin %s", pod.Namespace, pod.Name, pluginName)
		}
	}
}

func (m *mountPodManager) deletePodLocked(pod *v1.Pod) {
	for _, pods := range m.pods {
		delete(pods, pod.UID)
	}
}

func (m *mountPodManager) DeletePod(pod *v1.Pod) {
	m.mutex.Lock()
	defer m.mutex.Unlock()
	if pod.Namespace != m.namespace {
		//  ignore pods from unknown namespaces
		return
	}
	glog.V(5).Infof("DeleteMountPod: deleting %s/%s", pod.Namespace, pod.Name)
	m.deletePodLocked(pod)
}

func (m *mountPodManager) GetPod(pluginName string) *v1.Pod {
	m.mutex.Lock()
	defer m.mutex.Unlock()
	pods := m.pods[pluginName]
	if pods == nil {
		glog.V(5).Infof("GetPod: returned nil for %s", pluginName)
		return nil
	}
	for _, pod := range pods {
		// return a random pod
		glog.V(5).Infof("GetMountPod: returned %s/%s for %s", pod.Namespace, pod.Name, pluginName)
		return pod
	}
	glog.V(5).Infof("GetMountPod: returned nil for %s", pluginName)
	return nil
}
