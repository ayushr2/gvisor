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

package cmd

import (
	"context"
	"fmt"
	"os"
	"strings"

	"github.com/google/subcommands"
	"gvisor.dev/gvisor/runsc/cmd/nvproxy"
	"gvisor.dev/gvisor/runsc/cmd/sentry/forwardcmd"
	"gvisor.dev/gvisor/runsc/cmd/sentry/sentrycmd"
	"gvisor.dev/gvisor/runsc/cmd/util"
	"gvisor.dev/gvisor/runsc/flag"
	"gvisor.dev/gvisor/runsc/gvisorbinaries"
)

// execSentry replaces the current process with the Sentry binary, running the
// same command line that runsc was invoked with. It is used by commands that
// report on code linked into the Sentry binary.
//
// If the Sentry binary is missing and the sidecar usage policy allows embedded
// fallbacks, execSentry runs the command with inProcess instead.
//
// TODO(gvisor.dev/issue/13718): Remove inProcess once runsc no longer links
// the Sentry.
func execSentry(inProcess func() subcommands.ExitStatus) subcommands.ExitStatus {
	sentry := &gvisorbinaries.GvisorSentry
	if _, err := sentry.Path(); err != nil && gvisorbinaries.UsagePolicy.AllowEmbeddedFallback() {
		sentry.WarnUnavailable(fmt.Sprintf("Sidecar %q not usable (%v): running command in runsc itself", sentry.Name, err))
		return inProcess()
	}
	err := sentry.Exec(gvisorbinaries.Options{Argv: os.Args, Envv: os.Environ()})
	// Unreachable unless `sentry.Exec` fails.
	return util.Errorf("Failed to run %q in sidecar %q: %v", strings.Join(flag.CommandLine.Args(), " "), sentry.Name, err)
}

// MetricMetadata implements subcommands.Command for the "metric-metadata"
// command. It runs in the Sentry binary, which registers the metrics.
type MetricMetadata struct {
	forwardcmd.MetricMetadata
}

// Execute implements subcommands.Command.Execute.
func (m *MetricMetadata) Execute(ctx context.Context, f *flag.FlagSet, args ...any) subcommands.ExitStatus {
	return execSentry(func() subcommands.ExitStatus {
		return m.MetricMetadata.Execute(ctx, f, args...)
	})
}

// Nvproxy implements subcommands.Command for the "nvproxy" command. It runs
// in the Sentry binary, which links nvproxy.
type Nvproxy struct {
	nvproxy.Nvproxy
}

// Execute implements subcommands.Command.Execute.
func (n *Nvproxy) Execute(ctx context.Context, f *flag.FlagSet, args ...any) subcommands.ExitStatus {
	return execSentry(func() subcommands.ExitStatus {
		n.Nvproxy.SupportedDrivers = sentrycmd.NvproxySupportedDrivers
		return n.Nvproxy.Execute(ctx, f, args...)
	})
}

// Symbolize implements subcommands.Command for the "symbolize" command. It
// runs in the Sentry binary, whose coverage metadata it uses.
type Symbolize struct {
	forwardcmd.Symbolize
}

// Execute implements subcommands.Command.Execute.
func (c *Symbolize) Execute(ctx context.Context, f *flag.FlagSet, args ...any) subcommands.ExitStatus {
	return execSentry(func() subcommands.ExitStatus {
		return c.Symbolize.Execute(ctx, f, args...)
	})
}

// Syscalls implements subcommands.Command for the "syscalls" help topic. It
// runs in the Sentry binary, which has the syscall tables.
type Syscalls struct {
	forwardcmd.Syscalls
}

// Execute implements subcommands.Command.Execute.
func (s *Syscalls) Execute(ctx context.Context, f *flag.FlagSet, args ...any) subcommands.ExitStatus {
	return execSentry(func() subcommands.ExitStatus {
		return (&sentrycmd.Syscalls{Syscalls: s.Syscalls}).Execute(ctx, f, args...)
	})
}
