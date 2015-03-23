/*
Copyright 2014 Google Inc. All rights reserved.

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

// Reads the pod configuration from file or a directory of files.
package config

import (
	"fmt"
	"io/ioutil"
	"os"
	"path/filepath"
	"sort"
	"time"

	"github.com/GoogleCloudPlatform/kubernetes/pkg/api"
	"github.com/GoogleCloudPlatform/kubernetes/pkg/kubelet"
	"github.com/GoogleCloudPlatform/kubernetes/pkg/util"

	"github.com/golang/glog"
)

type sourceFile struct {
	path    string
	updates chan<- interface{}
}

func NewSourceFile(path string, period time.Duration, updates chan<- interface{}) {
	config := &sourceFile{
		path:    path,
		updates: updates,
	}
	glog.V(1).Infof("Watching path %q", path)
	go util.Forever(config.run, period)
}

func (s *sourceFile) run() {
	if err := s.extractFromPath(); err != nil {
		glog.Errorf("Unable to read config path %q: %v", s.path, err)
	}
}

func (s *sourceFile) extractFromPath() error {
	path := s.path
	statInfo, err := os.Stat(path)
	if err != nil {
		if !os.IsNotExist(err) {
			return err
		}
		// Emit an update with an empty PodList to allow FileSource to be marked as seen
		s.updates <- kubelet.PodUpdate{[]api.Pod{}, kubelet.SET, kubelet.FileSource}
		return fmt.Errorf("path does not exist, ignoring")
	}

	switch {
	case statInfo.Mode().IsDir():
		pods, err := extractFromDir(path)
		if err != nil {
			return err
		}
		s.updates <- kubelet.PodUpdate{pods, kubelet.SET, kubelet.FileSource}

	case statInfo.Mode().IsRegular():
		pod, err := extractFromFile(path)
		if err != nil {
			return err
		}
		s.updates <- kubelet.PodUpdate{[]api.Pod{pod}, kubelet.SET, kubelet.FileSource}

	default:
		return fmt.Errorf("path is not a directory or file")
	}

	return nil
}

// Get as many pod configs as we can from a directory.  Return an error iff something
// prevented us from reading anything at all.  Do not return an error if only some files
// were problematic.
func extractFromDir(name string) ([]api.Pod, error) {
	dirents, err := filepath.Glob(filepath.Join(name, "[^.]*"))
	if err != nil {
		return nil, fmt.Errorf("glob failed: %v", err)
	}

	pods := make([]api.Pod, 0)
	if len(dirents) == 0 {
		return pods, nil
	}

	sort.Strings(dirents)
	for _, path := range dirents {
		statInfo, err := os.Stat(path)
		if err != nil {
			glog.V(1).Infof("Can't get metadata for %q: %v", path, err)
			continue
		}

		switch {
		case statInfo.Mode().IsDir():
			glog.V(1).Infof("Not recursing into config path %q", path)
		case statInfo.Mode().IsRegular():
			pod, err := extractFromFile(path)
			if err != nil {
				glog.V(1).Infof("Can't process config file %q: %v", path, err)
			} else {
				pods = append(pods, pod)
			}
		default:
			glog.V(1).Infof("Config path %q is not a directory or file: %v", path, statInfo.Mode())
		}
	}
	return pods, nil
}

func extractFromFile(filename string) (pod api.Pod, err error) {
	glog.V(3).Infof("Reading config file %q", filename)
	file, err := os.Open(filename)
	if err != nil {
		return pod, err
	}
	defer file.Close()

	data, err := ioutil.ReadAll(file)
	if err != nil {
		return pod, err
	}

	parsed, _, pod, manifestErr := tryDecodeSingleManifest(data, filename, true)
	if parsed {
		if manifestErr != nil {
			// It parsed but could not be used.
			return pod, manifestErr
		}
		return pod, nil
	}

	parsed, pod, podErr := tryDecodeSinglePod(data, filename, true)
	if parsed {
		if podErr != nil {
			return pod, podErr
		}
		return pod, nil
	}

	return pod, fmt.Errorf("%v: read '%v', but couldn't parse as neither "+
		"manifest (%v) nor pod (%v).\n",
		filename, string(data), manifestErr, podErr)
}
