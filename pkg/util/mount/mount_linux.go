// +build linux

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

package mount

import (
	"bufio"
	"fmt"
	"hash/fnv"
	"io"
	"os"
	"os/exec"
	"reflect"
	"strconv"
	"strings"
	"syscall"

	"github.com/golang/glog"
	"k8s.io/apimachinery/pkg/util/sets"
	utilexec "k8s.io/kubernetes/pkg/util/exec"
)

const (
	// How many times to retry for a consistent read of /proc/mounts.
	maxListTries = 3
	// Number of fields per line in /proc/mounts as per the fstab man page.
	expectedNumFieldsPerLine = 6
	// Location of the mount file to use
	procMountsPath = "/proc/mounts"
	// Location of the mountinfo file
	procMountInfoPath = "/proc/self/mountinfo"
)

const (
	// 'fsck' found errors and corrected them
	fsckErrorsCorrected = 1
	// 'fsck' found errors but exited without correcting them
	fsckErrorsUncorrected = 4
)

// Mounter provides the default implementation of mount.Interface
// for the linux platform.  This implementation assumes that the
// kubelet is running in the host's root mount namespace.
type Mounter struct {
	mounterPath string
}

// Mount mounts source to target as fstype with given options. 'source' and 'fstype' must
// be an emtpy string in case it's not required, e.g. for remount, or for auto filesystem
// type, where kernel handles fs type for you. The mount 'options' is a list of options,
// currently come from mount(8), e.g. "ro", "remount", "bind", etc. If no more option is
// required, call Mount with an empty string list or nil.
func (mounter *Mounter) Mount(source string, target string, fstype string, options []string) error {
	// Path to mounter binary if containerized mounter is needed. Otherwise, it is set to empty.
	// All Linux distros are expected to be shipped with a mount utility that an support bind mounts.
	mounterPath := ""
	bind, bindRemountOpts := isBind(options)
	if bind {
		err := doMount(mounterPath, defaultMountCommand, source, target, fstype, []string{"bind"})
		if err != nil {
			return err
		}
		return doMount(mounterPath, defaultMountCommand, source, target, fstype, bindRemountOpts)
	}
	// The list of filesystems that require containerized mounter on GCI image cluster
	fsTypesNeedMounter := sets.NewString("nfs", "glusterfs", "ceph", "cifs")
	if fsTypesNeedMounter.Has(fstype) {
		mounterPath = mounter.mounterPath
	}
	return doMount(mounterPath, defaultMountCommand, source, target, fstype, options)
}

// isBind detects whether a bind mount is being requested and makes the remount options to
// use in case of bind mount, due to the fact that bind mount doesn't respect mount options.
// The list equals:
//   options - 'bind' + 'remount' (no duplicate)
func isBind(options []string) (bool, []string) {
	bindRemountOpts := []string{"remount"}
	bind := false

	if len(options) != 0 {
		for _, option := range options {
			switch option {
			case "bind":
				bind = true
				break
			case "remount":
				break
			default:
				bindRemountOpts = append(bindRemountOpts, option)
			}
		}
	}

	return bind, bindRemountOpts
}

// doMount runs the mount command. mounterPath is the path to mounter binary if containerized mounter is used.
func doMount(mounterPath string, mountCmd string, source string, target string, fstype string, options []string) error {
	mountArgs := makeMountArgs(source, target, fstype, options)
	if len(mounterPath) > 0 {
		mountArgs = append([]string{mountCmd}, mountArgs...)
		mountCmd = mounterPath
	}

	glog.V(4).Infof("Mounting cmd (%s) with arguments (%s)", mountCmd, mountArgs)
	command := exec.Command(mountCmd, mountArgs...)
	output, err := command.CombinedOutput()
	if err != nil {
		glog.Errorf("Mount failed: %v\nMounting command: %s\nMounting arguments: %s %s %s %v\nOutput: %s\n", err, mountCmd, source, target, fstype, options, string(output))
		return fmt.Errorf("mount failed: %v\nMounting command: %s\nMounting arguments: %s %s %s %v\nOutput: %s\n",
			err, mountCmd, source, target, fstype, options, string(output))
	}
	return err
}

// makeMountArgs makes the arguments to the mount(8) command.
func makeMountArgs(source, target, fstype string, options []string) []string {
	// Build mount command as follows:
	//   mount [-t $fstype] [-o $options] [$source] $target
	mountArgs := []string{}
	if len(fstype) > 0 {
		mountArgs = append(mountArgs, "-t", fstype)
	}
	if len(options) > 0 {
		mountArgs = append(mountArgs, "-o", strings.Join(options, ","))
	}
	if len(source) > 0 {
		mountArgs = append(mountArgs, source)
	}
	mountArgs = append(mountArgs, target)

	return mountArgs
}

// Unmount unmounts the target.
func (mounter *Mounter) Unmount(target string) error {
	glog.V(4).Infof("Unmounting %s", target)
	command := exec.Command("umount", target)
	output, err := command.CombinedOutput()
	if err != nil {
		return fmt.Errorf("Unmount failed: %v\nUnmounting arguments: %s\nOutput: %s\n", err, target, string(output))
	}
	return nil
}

// List returns a list of all mounted filesystems.
func (*Mounter) List() ([]MountPoint, error) {
	return listProcMounts(procMountsPath)
}

// IsLikelyNotMountPoint determines if a directory is not a mountpoint.
// It is fast but not necessarily ALWAYS correct. If the path is in fact
// a bind mount from one part of a mount to another it will not be detected.
// mkdir /tmp/a /tmp/b; mount --bin /tmp/a /tmp/b; IsLikelyNotMountPoint("/tmp/b")
// will return true. When in fact /tmp/b is a mount point. If this situation
// if of interest to you, don't use this function...
func (mounter *Mounter) IsLikelyNotMountPoint(file string) (bool, error) {
	return IsNotMountPoint(file)
}

func IsNotMountPoint(file string) (bool, error) {
	stat, err := os.Stat(file)
	if err != nil {
		return true, err
	}
	rootStat, err := os.Lstat(file + "/..")
	if err != nil {
		return true, err
	}
	// If the directory has a different device as parent, then it is a mountpoint.
	if stat.Sys().(*syscall.Stat_t).Dev != rootStat.Sys().(*syscall.Stat_t).Dev {
		return false, nil
	}

	return true, nil
}

// DeviceOpened checks if block device in use by calling Open with O_EXCL flag.
// If pathname is not a device, log and return false with nil error.
// If open returns errno EBUSY, return true with nil error.
// If open returns nil, return false with nil error.
// Otherwise, return false with error
func (mounter *Mounter) DeviceOpened(pathname string) (bool, error) {
	return exclusiveOpenFailsOnDevice(pathname)
}

// PathIsDevice uses FileInfo returned from os.Stat to check if path refers
// to a device.
func (mounter *Mounter) PathIsDevice(pathname string) (bool, error) {
	return pathIsDevice(pathname)
}

func exclusiveOpenFailsOnDevice(pathname string) (bool, error) {
	isDevice, err := pathIsDevice(pathname)
	if err != nil {
		return false, fmt.Errorf(
			"PathIsDevice failed for path %q: %v",
			pathname,
			err)
	}
	if !isDevice {
		glog.Errorf("Path %q is not refering to a device.", pathname)
		return false, nil
	}
	fd, errno := syscall.Open(pathname, syscall.O_RDONLY|syscall.O_EXCL, 0)
	// If the device is in use, open will return an invalid fd.
	// When this happens, it is expected that Close will fail and throw an error.
	defer syscall.Close(fd)
	if errno == nil {
		// device not in use
		return false, nil
	} else if errno == syscall.EBUSY {
		// device is in use
		return true, nil
	}
	// error during call to Open
	return false, errno
}

func pathIsDevice(pathname string) (bool, error) {
	finfo, err := os.Stat(pathname)
	if os.IsNotExist(err) {
		return false, nil
	}
	// err in call to os.Stat
	if err != nil {
		return false, err
	}
	// path refers to a device
	if finfo.Mode()&os.ModeDevice != 0 {
		return true, nil
	}
	// path does not refer to device
	return false, nil
}

//GetDeviceNameFromMount: given a mount point, find the device name from its global mount point
func (mounter *Mounter) GetDeviceNameFromMount(mountPath, pluginDir string) (string, error) {
	return getDeviceNameFromMount(mounter, mountPath, pluginDir)
}

func listProcMounts(mountFilePath string) ([]MountPoint, error) {
	hash1, err := readProcMounts(mountFilePath, nil)
	if err != nil {
		return nil, err
	}

	for i := 0; i < maxListTries; i++ {
		mps := []MountPoint{}
		hash2, err := readProcMounts(mountFilePath, &mps)
		if err != nil {
			return nil, err
		}
		if hash1 == hash2 {
			// Success
			return mps, nil
		}
		hash1 = hash2
	}
	return nil, fmt.Errorf("failed to get a consistent snapshot of %v after %d tries", mountFilePath, maxListTries)
}

// readProcMounts reads the given mountFilePath (normally /proc/mounts) and produces a hash
// of the contents.  If the out argument is not nil, this fills it with MountPoint structs.
func readProcMounts(mountFilePath string, out *[]MountPoint) (uint32, error) {
	file, err := os.Open(mountFilePath)
	if err != nil {
		return 0, err
	}
	defer file.Close()
	return readProcMountsFrom(file, out)
}

func readProcMountsFrom(file io.Reader, out *[]MountPoint) (uint32, error) {
	hash := fnv.New32a()
	scanner := bufio.NewReader(file)
	for {
		line, err := scanner.ReadString('\n')
		if err == io.EOF {
			break
		}
		fields := strings.Fields(line)
		if len(fields) != expectedNumFieldsPerLine {
			return 0, fmt.Errorf("wrong number of fields (expected %d, got %d): %s", expectedNumFieldsPerLine, len(fields), line)
		}

		fmt.Fprintf(hash, "%s", line)

		if out != nil {
			mp := MountPoint{
				Device: fields[0],
				Path:   fields[1],
				Type:   fields[2],
				Opts:   strings.Split(fields[3], ","),
			}

			freq, err := strconv.Atoi(fields[4])
			if err != nil {
				return 0, err
			}
			mp.Freq = freq

			pass, err := strconv.Atoi(fields[5])
			if err != nil {
				return 0, err
			}
			mp.Pass = pass

			*out = append(*out, mp)
		}
	}
	return hash.Sum32(), nil
}

func (mounter *Mounter) MakeShared(path string) error {
	mountCmd := defaultMountCommand
	mountArgs := []string{}
	return doMakeShared(path, procMountInfoPath, mountCmd, mountArgs)
}

// formatAndMount uses unix utils to format and mount the given disk
func (mounter *SafeFormatAndMount) formatAndMount(source string, target string, fstype string, options []string) error {
	options = append(options, "defaults")

	// Run fsck on the disk to fix repairable issues
	glog.V(4).Infof("Checking for issues with fsck on disk: %s", source)
	args := []string{"-a", source}
	out, err := mounter.Exec.Run("fsck", args)
	if err != nil {
		ee, isExitError := err.(utilexec.ExitError)
		switch {
		case err == utilexec.ErrExecutableNotFound:
			glog.Warningf("'fsck' not found on system; continuing mount without running 'fsck'.")
		case isExitError && ee.ExitStatus() == fsckErrorsCorrected:
			glog.Infof("Device %s has errors which were corrected by fsck.", source)
		case isExitError && ee.ExitStatus() == fsckErrorsUncorrected:
			return fmt.Errorf("'fsck' found errors on device %s but could not correct them: %s.", source, string(out))
		case isExitError && ee.ExitStatus() > fsckErrorsUncorrected:
			glog.Infof("`fsck` error %s", string(out))
		}
	}

	// Try to mount the disk
	glog.V(4).Infof("Attempting to mount disk: %s %s %s", fstype, source, target)
	mountErr := mounter.Interface.Mount(source, target, fstype, options)
	if mountErr != nil {
		// Mount failed. This indicates either that the disk is unformatted or
		// it contains an unexpected filesystem.
		existingFormat, err := mounter.getDiskFormat(source)
		if err != nil {
			return err
		}
		if existingFormat == "" {
			// Disk is unformatted so format it.
			args = []string{source}
			// Use 'ext4' as the default
			if len(fstype) == 0 {
				fstype = "ext4"
			}

			if fstype == "ext4" || fstype == "ext3" {
				args = []string{"-F", source}
			}
			glog.Infof("Disk %q appears to be unformatted, attempting to format as type: %q with options: %v", source, fstype, args)
			_, err := mounter.Exec.Run("mkfs."+fstype, args)
			if err == nil {
				// the disk has been formatted successfully try to mount it again.
				glog.Infof("Disk successfully formatted (mkfs): %s - %s %s", fstype, source, target)
				return mounter.Interface.Mount(source, target, fstype, options)
			}
			glog.Errorf("format of disk %q failed: type:(%q) target:(%q) options:(%q)error:(%v)", source, fstype, target, options, err)
			return err
		} else {
			// Disk is already formatted and failed to mount
			if len(fstype) == 0 || fstype == existingFormat {
				// This is mount error
				return mountErr
			} else {
				// Block device is formatted with unexpected filesystem, let the user know
				return fmt.Errorf("failed to mount the volume as %q, it already contains %s. Mount error: %v", fstype, existingFormat, mountErr)
			}
		}
	}
	return mountErr
}

// diskLooksUnformatted uses 'lsblk' to see if the given disk is unformated
func (mounter *SafeFormatAndMount) getDiskFormat(disk string) (string, error) {
	args := []string{"-n", "-o", "FSTYPE", disk}
	glog.V(4).Infof("Attempting to determine if disk %q is formatted using lsblk with args: (%v)", disk, args)
	dataOut, err := mounter.Exec.Run("lsblk", args)
	output := string(dataOut)
	glog.V(4).Infof("Output: %q", output)

	if err != nil {
		glog.Errorf("Could not determine if disk %q is formatted (%v)", disk, err)
		return "", err
	}

	// Split lsblk output into lines. Unformatted devices should contain only
	// "\n". Beware of "\n\n", that's a device with one empty partition.
	output = strings.TrimSuffix(output, "\n") // Avoid last empty line
	lines := strings.Split(output, "\n")
	if lines[0] != "" {
		// The device is formatted
		return lines[0], nil
	}

	if len(lines) == 1 {
		// The device is unformatted and has no dependent devices
		return "", nil
	}

	// The device has dependent devices, most probably partitions (LVM, LUKS
	// and MD RAID are reported as FSTYPE and caught above).
	return "unknown data, probably partitions", nil
}

// isShared returns true, if given path is on a mount point that has shared
// mount propagation.
func isShared(path string, filename string) (bool, error) {
	infos, err := getMountInfo(filename)
	if err != nil {
		return false, err
	}

	// process /proc/xxx/mountinfo in backward order and find the first mount
	// point that is prefix of 'path' - that's the mount where path resides
	var info *mountInfo
	for i := len(infos) - 1; i >= 0; i-- {
		if strings.HasPrefix(path, infos[i].mountPoint) {
			info = &infos[i]
			break
		}
	}
	if info == nil {
		return false, fmt.Errorf("cannot find mount point for %q", path)
	}

	// parse optional parameters
	for _, opt := range info.optional {
		if strings.HasPrefix(opt, "shared:") {
			return true, nil
		}
	}
	return false, nil
}

type mountInfo struct {
	root, mountPoint string
	// list of "optional parameters", mount propagation is one of them
	optional []string
}

// getMountInfo reads /proc/xxx/mountinfo and makes sure it did not change
// between reads. This protects us from kernel adding/removing lines there
// between individual read() syscalls.
func getMountInfo(filename string) ([]mountInfo, error) {
	// Read the file until we get the same content twice
	oldInfo, err := parseMountInfo(filename)
	if err != nil {
		return []mountInfo{}, err
	}
	for {
		newInfo, err := parseMountInfo(filename)
		if err != nil {
			return []mountInfo{}, err
		}
		if reflect.DeepEqual(oldInfo, newInfo) {
			// Content is the same, finish
			return oldInfo, nil
		}
		// Content is different, continue in the loop and try again
		oldInfo = newInfo
	}
}

// parseMountInfo parses /proc/xxx/mountinfo.
func parseMountInfo(filename string) ([]mountInfo, error) {
	file, err := os.Open(filename)
	if err != nil {
		return []mountInfo{}, err
	}
	scanner := bufio.NewReader(file)
	infos := []mountInfo{}

	for {
		line, err := scanner.ReadString('\n')
		if err == io.EOF {
			break
		}
		fields := strings.Fields(line)
		if len(fields) < 7 {
			return nil, fmt.Errorf("wrong number of fields in (expected %d, got %d): %s", 8, len(fields), line)
		}
		info := mountInfo{
			root:       fields[3],
			mountPoint: fields[4],
			optional:   []string{},
		}
		for i := 6; i < len(fields) && fields[i] != "-"; i++ {
			info.optional = append(info.optional, fields[i])
		}
		infos = append(infos, info)
	}
	return infos, nil
}

// doMakeShared is common implementation of MakeShared on Linux. It checks if
// path is shared and bind-mounts it as shared if needed. mountCmd and mountArgs
// are expected to contain mount-like command, doMakeShared will add '--bind
// <path> <path>' and '--make-shared <path>' to mountArgs.
func doMakeShared(path string, mountInfoFilename string, mountCmd string, mountArgs []string) error {
	shared, err := isShared(path, mountInfoFilename)
	if err != nil {
		return err
	}
	if shared {
		glog.V(4).Infof("Directory %s is already on a shared mount", path)
		return nil
	}

	glog.V(2).Infof("Bind-mounting %q with shared mount propagation", path)
	// mount --bind /var/lib/kubelet /var/lib/kubelet
	bindArgs := append(mountArgs, "--bind", path, path)
	command := exec.Command(mountCmd, bindArgs...)
	output, err := command.CombinedOutput()
	if err != nil {
		glog.Errorf("Failed to bind-mount %s: %v\nExecuted: %s %v, Output: %s", path, err, mountCmd, mountArgs, string(output))
		return fmt.Errorf("failed to bind-mount %s: %v", path, err)
	}

	// mount --make-rshared /var/lib/kubelet
	makeSharedArgs := append(mountArgs, "--make-rshared", path)
	command = exec.Command(mountCmd, makeSharedArgs...)
	output, err = command.CombinedOutput()
	if err != nil {
		glog.Errorf("Failed to make %s shared: %v\nExecuted: %s %v, Output: %s", path, err, mountCmd, mountArgs, string(output))
		return fmt.Errorf("failed to make %s shared: %v", path, err)
	}

	return nil
}
