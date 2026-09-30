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

import "testing"

func TestParseFSCheckpointPaths(t *testing.T) {
	for _, tc := range []struct {
		name    string
		in      string
		wantErr bool
		wantLen int
	}{
		{
			name:    "empty",
			in:      "",
			wantErr: false,
			wantLen: 0,
		},
		{
			name:    "all-tmpfs",
			in:      "all-tmpfs",
			wantErr: false,
			wantLen: 1,
		},
		{
			name:    "clean absolute path",
			in:      "/data",
			wantErr: false,
			wantLen: 1,
		},
		{
			name:    "container and clean absolute path",
			in:      "c1:/data",
			wantErr: false,
			wantLen: 1,
		},
		{
			name:    "multiple clean paths",
			in:      "c1:/data, c2:/tmp, all-tmpfs",
			wantErr: false,
			wantLen: 3,
		},
		{
			name:    "uncleaned trailing slash",
			in:      "/data/",
			wantErr: true,
		},
		{
			name:    "uncleaned redundant slash",
			in:      "/data//dir",
			wantErr: true,
		},
		{
			name:    "uncleaned root slashes",
			in:      "//",
			wantErr: true,
		},
		{
			name:    "relative path",
			in:      "data",
			wantErr: true,
		},
		{
			name:    "container with empty path",
			in:      "c1:",
			wantErr: true,
		},
		{
			name:    "uncleaned dot",
			in:      "/data/./sub",
			wantErr: true,
		},
		{
			name:    "uncleaned dot dot",
			in:      "/data/../sub",
			wantErr: true,
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			paths, err := ParseFSCheckpointPaths(tc.in)
			if (err != nil) != tc.wantErr {
				t.Errorf("ParseFSCheckpointPaths(%q) error = %v, wantErr %v", tc.in, err, tc.wantErr)
			}
			if err == nil && len(paths) != tc.wantLen {
				t.Errorf("ParseFSCheckpointPaths(%q) len = %d, want %d", tc.in, len(paths), tc.wantLen)
			}
		})
	}
}
