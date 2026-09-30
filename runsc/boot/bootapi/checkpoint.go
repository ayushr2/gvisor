// Copyright 2026 The gVisor Authors.
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//     http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.

package bootapi

import (
	"fmt"
	"path"
	"strings"

	specs "github.com/opencontainers/runtime-spec/specs-go"

	"gvisor.dev/gvisor/pkg/sentry/checkpoint"
	"gvisor.dev/gvisor/pkg/sentry/fscheckpoint"
	"gvisor.dev/gvisor/pkg/state/statefile"
	"gvisor.dev/gvisor/runsc/config"
	"gvisor.dev/gvisor/runsc/specutils"
)

const (
	annotationCheckpointPrefix = "dev.gvisor.internal.checkpoint."

	// AnnotationCheckpointPath is the path to the directory where the
	// checkpoint files will be created. When present, it allows for the
	// workload running inside to trigger a checkpoint without having to use
	// the runsc CLI.
	AnnotationCheckpointPath = annotationCheckpointPrefix + "path"

	// AnnotationCheckpointResume indicates whether the sandbox should continue
	// running after the checkpoint. Optional, defaults to false.
	AnnotationCheckpointResume = annotationCheckpointPrefix + "resume"

	// AnnotationCheckpointCompression is the compression to use for the
	// checkpoint file. Optional, defaults to best speed compression.
	AnnotationCheckpointCompression = annotationCheckpointPrefix + "compression"

	// AnnotationCheckpointDirect indicates whether the checkpoint IOs should
	// use O_DIRECT. Optional, defaults to false.
	AnnotationCheckpointDirect = annotationCheckpointPrefix + "direct"

	// AnnotationCheckpointExcludeCommittedZeroPages indicates whether the
	// checkpoint should exclude committed zero pages. Optional, defaults to
	// false.
	AnnotationCheckpointExcludeCommittedZeroPages = annotationCheckpointPrefix + "exclude-committed-zero-pages"

	// AnnotationCheckpointCudaCheckpointPath is the path to the
	// cuda-checkpoint binary. It's required if the workload has CUDA
	// processes.
	AnnotationCheckpointCudaCheckpointPath = annotationCheckpointPrefix + "cuda-checkpoint-path"

	// AnnotationCheckpointCudaCheckpointSequential indicates whether
	// cuda-checkpoint should be run sequentially. Optional, defaults to false.
	AnnotationCheckpointCudaCheckpointSequential = annotationCheckpointPrefix + "cuda-checkpoint-sequential"

	// AnnotationCheckpointEnable indicates whether files under /proc/gvisor
	// should be present in the container to allow the workload to trigger a
	// checkpoint.
	AnnotationCheckpointEnable = annotationCheckpointPrefix + "enable"

	// AnnotationSaveRestoreExecArgv is the argv to use for the save/restore
	// exec binary.
	AnnotationSaveRestoreExecArgv = annotationCheckpointPrefix + "save-restore-exec-argv"

	// AnnotationSaveRestoreExecTimeout is the timeout to use for the
	// save/restore exec binary.
	AnnotationSaveRestoreExecTimeout = annotationCheckpointPrefix + "save-restore-exec-timeout"
)

const (
	annotationFSCheckpointPrefix = "dev.gvisor.internal.fscheckpoint."

	// AnnotationFSCheckpointEnable indicates whether files under /proc/gvisor
	// should be present in the container to allow the workload to trigger a
	// filesystem checkpoint.
	AnnotationFSCheckpointEnable = annotationFSCheckpointPrefix + "enable"

	// AnnotationFSCheckpointPath is the path to the directory where the
	// filesystem checkpoint files will be created. When present, it allows for
	// the workload running inside to trigger a filesystem checkpoint without
	// having to use the runsc CLI.
	AnnotationFSCheckpointPath = annotationFSCheckpointPrefix + "path"

	// AnnotationFSCheckpointResume indicates whether the sandbox should
	// continue running after filesystem checkpoint saving triggered via
	// /proc/gvisor. Optional, defaults to false.
	AnnotationFSCheckpointResume = annotationFSCheckpointPrefix + "resume"

	// AnnotationFSCheckpointDirect indicates whether filesystem checkpoint
	// I/Os triggered via /proc/gvisor should use O_DIRECT. Optional, defaults
	// to false.
	AnnotationFSCheckpointDirect = annotationFSCheckpointPrefix + "direct"

	// AnnotationFSCheckpointPaths is a comma-separated list of paths inside the
	// containers to save. Optional.
	AnnotationFSCheckpointPaths = annotationFSCheckpointPrefix + "paths"
)

// GetAnnotationCheckpointPath returns the checkpoint path specified in the
// container annotation. Return empty string if no annotation is specified.
func GetAnnotationCheckpointPath(conf *config.Config, spec *specs.Spec) (string, error) {
	path := spec.Annotations[AnnotationCheckpointPath]
	if len(path) != 0 {
		if len(conf.TestOnlyAutosaveImagePath) != 0 {
			return "", fmt.Errorf("autosave is not supported with %q annotation", AnnotationCheckpointPath)
		}
	}
	return path, nil
}

// GetAnnotationCheckpointCompression returns the checkpoint compression level
// specified in the container annotation.
func GetAnnotationCheckpointCompression(spec *specs.Spec) (statefile.CompressionLevel, error) {
	return statefile.CompressionLevelFromString(spec.Annotations[AnnotationCheckpointCompression])
}

// GetAnnotationCheckpointDirect returns true if the checkpoint is direct.
func GetAnnotationCheckpointDirect(spec *specs.Spec) bool {
	return specutils.AnnotationToBool(spec, AnnotationCheckpointDirect)
}

// GetAnnotationFSCheckpointPath returns the filesystem checkpoint path
// specified in the container annotation. Return empty string if no annotation
// is specified.
func GetAnnotationFSCheckpointPath(spec *specs.Spec) string {
	return spec.Annotations[AnnotationFSCheckpointPath]
}

// GetAnnotationFSCheckpointDirect returns true if filesystem checkpoint I/O
// controlled by the containing annotation should use O_DIRECT.
func GetAnnotationFSCheckpointDirect(spec *specs.Spec) bool {
	return specutils.AnnotationToBool(spec, AnnotationFSCheckpointDirect)
}

// ParseFSCheckpointPaths parses a comma-separated list of container:path
// checkpoint targets.
func ParseFSCheckpointPaths(val string) ([]checkpoint.ResourceID, error) {
	val = strings.TrimSpace(val)
	if val == "" {
		return nil, nil
	}
	var paths []checkpoint.ResourceID
	for _, part := range strings.Split(val, ",") {
		part = strings.TrimSpace(part)
		if part == "" {
			continue
		}
		var c, p string
		subparts := strings.SplitN(part, ":", 2)
		if len(subparts) == 1 {
			p = strings.TrimSpace(subparts[0])
		} else {
			c = strings.TrimSpace(subparts[0])
			p = strings.TrimSpace(subparts[1])
		}
		if p == "" {
			return nil, fmt.Errorf("empty path in fscheckpoint paths: %q", val)
		}
		if p != fscheckpoint.AllTmpfsPath && (!path.IsAbs(p) || path.Clean(p) != p) {
			return nil, fmt.Errorf("checkpoint path must be an absolute, clean path or %q, got: %q", fscheckpoint.AllTmpfsPath, p)
		}
		paths = append(paths, checkpoint.ResourceID{ContainerName: c, Path: p})
	}
	return paths, nil
}
