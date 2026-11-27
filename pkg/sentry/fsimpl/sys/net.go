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

package sys

import (
	"bytes"
	"cmp"
	"fmt"
	"slices"

	"gvisor.dev/gvisor/pkg/abi/linux"
	"gvisor.dev/gvisor/pkg/context"
	"gvisor.dev/gvisor/pkg/errors/linuxerr"
	"gvisor.dev/gvisor/pkg/sentry/fsimpl/kernfs"
	"gvisor.dev/gvisor/pkg/sentry/inet"
	"gvisor.dev/gvisor/pkg/sentry/kernel/auth"
	"gvisor.dev/gvisor/pkg/sentry/vfs"
)

// netDevAttrs are the files in /sys/devices/virtual/net/<name>.
var netDevAttrs = []string{
	"addr_len",
	"address",
	"broadcast",
	"carrier",
	"dev_id",
	"dev_port",
	"dormant",
	"flags",
	"ifindex",
	"iflink",
	"link_mode",
	"mtu",
	"operstate",
	"type",
	"uevent",
}

// netIface identifies a network interface.
//
// +stateify savable
type netIface struct {
	index int32
	name  string
}

// netIfaces returns the network interfaces of fs.netns, sorted by index. It
// leaves out the interfaces whose names are static children of
// /sys/class/net.
func (fs *filesystem) netIfaces() []netIface {
	if fs.netns == nil {
		return nil
	}
	stack := fs.netns.Stack()
	if stack == nil {
		return nil
	}
	var ifaces []netIface
	for index, iface := range stack.Interfaces() {
		if _, ok := fs.staticNetDevs[iface.Name]; !ok {
			ifaces = append(ifaces, netIface{index: index, name: iface.Name})
		}
	}
	slices.SortFunc(ifaces, func(a, b netIface) int { return cmp.Compare(a.index, b.index) })
	return ifaces
}

// netIfaceByName returns the network interface of fs.netns named name.
func (fs *filesystem) netIfaceByName(name string) (netIface, bool) {
	for _, iface := range fs.netIfaces() {
		if iface.name == name {
			return iface, true
		}
	}
	return netIface{}, false
}

// netIfaceValid returns true if the network interface of fs.netns with index
// iface.index is still named iface.name.
func (fs *filesystem) netIfaceValid(iface netIface) bool {
	cur, ok := fs.netIfaceByName(iface.name)
	return ok && cur.index == iface.index
}

// addNetDirs adds /sys/class/net and /sys/devices/virtual/net. classNet
// holds the static children of /sys/class/net.
func (fs *filesystem) addNetDirs(ctx context.Context, creds *auth.Credentials, classNet, classSub, virtualSub map[string]kernfs.Inode) {
	fs.staticNetDevs = make(map[string]struct{}, len(classNet))
	for name := range classNet {
		fs.staticNetDevs[name] = struct{}{}
	}
	classSub["net"] = fs.newNetDir(ctx, creds, true /* class */, classNet)
	virtualSub["net"] = fs.newNetDir(ctx, creds, false /* class */, nil)
}

// netDir is /sys/class/net or /sys/devices/virtual/net. Besides its static
// children, it has an entry for each network interface returned by
// filesystem.netIfaces.
//
// +stateify savable
type netDir struct {
	dir

	fs    *filesystem
	creds *auth.Credentials

	// class is true for /sys/class/net, where the entry of each interface
	// returned by filesystem.netIfaces is a symlink to its directory in
	// /sys/devices/virtual/net. Immutable.
	class bool
}

var _ kernfs.Inode = (*netDir)(nil)

func (fs *filesystem) newNetDir(ctx context.Context, creds *auth.Credentials, class bool, contents map[string]kernfs.Inode) *netDir {
	d := &netDir{
		fs:    fs,
		creds: creds,
		class: class,
	}
	d.InodeAttrs.Init(ctx, creds, linux.UNNAMED_MAJOR, fs.devMinor, fs.NextIno(), linux.ModeDirectory|defaultSysDirMode)
	d.OrderedChildren.Init(kernfs.OrderedChildrenOptions{})
	d.InitRefs()
	d.IncLinks(d.OrderedChildren.Populate(contents))
	return d
}

// Lookup implements kernfs.Inode.Lookup.
func (d *netDir) Lookup(ctx context.Context, name string) (kernfs.Inode, error) {
	if inode, err := d.dir.Lookup(ctx, name); !linuxerr.Equals(linuxerr.ENOENT, err) {
		return inode, err
	}
	iface, ok := d.fs.netIfaceByName(name)
	if !ok {
		return nil, linuxerr.ENOENT
	}
	if d.class {
		return d.fs.newNetLink(ctx, d.creds, iface), nil
	}
	return d.fs.newNetDevDir(ctx, d.creds, iface), nil
}

// Open implements kernfs.Inode.Open.
func (d *netDir) Open(ctx context.Context, rp *vfs.ResolvingPath, kd *kernfs.Dentry, opts vfs.OpenOptions) (*vfs.FileDescription, error) {
	opts.Flags &= linux.O_ACCMODE | linux.O_CREAT | linux.O_EXCL | linux.O_TRUNC |
		linux.O_DIRECTORY | linux.O_NOFOLLOW | linux.O_NONBLOCK | linux.O_NOCTTY
	fd, err := kernfs.NewGenericDirectoryFD(rp.Mount(), kd, rp.Credentials(), &d.OrderedChildren, &d.locks, &opts, kernfs.GenericDirectoryFDOptions{
		SeekEnd: kernfs.SeekEndZero,
	})
	if err != nil {
		return nil, err
	}
	return fd.VFSFileDescription(), nil
}

// IterDirents implements kernfs.Inode.IterDirents.
func (d *netDir) IterDirents(ctx context.Context, mnt *vfs.Mount, cb vfs.IterDirentsCallback, offset, relOffset int64) (int64, error) {
	typ := uint8(linux.DT_DIR)
	if d.class {
		typ = linux.DT_LNK
	}
	// The offset of an interface entry is derived from its index, so that
	// iteration resumes at the right entry when interfaces are added or
	// removed between calls.
	base := offset - relOffset
	for _, iface := range d.fs.netIfaces() {
		if int64(iface.index) < relOffset {
			continue
		}
		dirent := vfs.Dirent{
			Name:    iface.name,
			Type:    typ,
			Ino:     d.fs.NextIno(),
			NextOff: base + int64(iface.index) + 1,
		}
		if err := cb.Handle(dirent); err != nil {
			return base + int64(iface.index), err
		}
		offset = dirent.NextOff
	}
	return offset, nil
}

// netLink is the /sys/class/net/<name> symlink.
//
// +stateify savable
type netLink struct {
	kernfs.StaticSymlink

	fs    *filesystem
	iface netIface
}

var _ kernfs.Inode = (*netLink)(nil)

func (fs *filesystem) newNetLink(ctx context.Context, creds *auth.Credentials, iface netIface) *netLink {
	l := &netLink{fs: fs, iface: iface}
	l.Init(ctx, creds, linux.UNNAMED_MAJOR, fs.devMinor, fs.NextIno(), "../../devices/virtual/net/"+iface.name)
	return l
}

// Valid implements kernfs.Inode.Valid.
func (l *netLink) Valid(ctx context.Context, parent *kernfs.Dentry, name string) bool {
	return l.fs.netIfaceValid(l.iface)
}

// netDevDir is the /sys/devices/virtual/net/<name> directory.
//
// +stateify savable
type netDevDir struct {
	dir

	fs    *filesystem
	iface netIface
}

var _ kernfs.Inode = (*netDevDir)(nil)

func (fs *filesystem) newNetDevDir(ctx context.Context, creds *auth.Credentials, iface netIface) *netDevDir {
	contents := map[string]kernfs.Inode{
		"subsystem": kernfs.NewStaticSymlink(ctx, creds, linux.UNNAMED_MAJOR, fs.devMinor, fs.NextIno(), "../../../../class/net"),
	}
	for _, attr := range netDevAttrs {
		f := &netAttrFile{fs: fs, index: iface.index, attr: attr}
		f.Init(ctx, creds, linux.UNNAMED_MAJOR, fs.devMinor, fs.NextIno(), f, defaultSysMode)
		contents[attr] = f
	}
	d := &netDevDir{fs: fs, iface: iface}
	d.InodeAttrs.Init(ctx, creds, linux.UNNAMED_MAJOR, fs.devMinor, fs.NextIno(), linux.ModeDirectory|defaultSysDirMode)
	d.OrderedChildren.Init(kernfs.OrderedChildrenOptions{})
	d.InitRefs()
	d.IncLinks(d.OrderedChildren.Populate(contents))
	return d
}

// Valid implements kernfs.Inode.Valid.
func (d *netDevDir) Valid(ctx context.Context, parent *kernfs.Dentry, name string) bool {
	return d.fs.netIfaceValid(d.iface)
}

// netAttrFile is a file in /sys/devices/virtual/net/<name>.
//
// +stateify savable
type netAttrFile struct {
	implStatFS
	kernfs.DynamicBytesFile

	fs    *filesystem
	index int32
	attr  string
}

// Generate implements vfs.DynamicBytesSource.Generate.
func (f *netAttrFile) Generate(ctx context.Context, buf *bytes.Buffer) error {
	var stack inet.Stack
	if f.fs.netns != nil {
		stack = f.fs.netns.Stack()
	}
	if stack == nil {
		return linuxerr.ENODEV
	}
	iface, ok := stack.Interfaces()[f.index]
	if !ok {
		return linuxerr.ENODEV
	}
	addr := iface.Addr
	if len(addr) == 0 && (iface.DeviceType == linux.ARPHRD_ETHER || iface.DeviceType == linux.ARPHRD_LOOPBACK) {
		addr = make([]byte, 6)
	}
	brd := make([]byte, len(addr))
	if iface.DeviceType == linux.ARPHRD_ETHER {
		for i := range brd {
			brd[i] = 0xff
		}
	}
	up := iface.Flags&linux.IFF_UP != 0
	switch f.attr {
	case "addr_len":
		fmt.Fprintf(buf, "%d\n", len(addr))
	case "address":
		buf.WriteString(hardwareAddr(addr))
	case "broadcast":
		buf.WriteString(hardwareAddr(brd))
	case "carrier":
		if !up {
			return linuxerr.EINVAL
		}
		buf.WriteString(boolAttr(iface.Flags&linux.IFF_LOWER_UP != 0))
	case "dev_id":
		buf.WriteString("0x0\n")
	case "dev_port", "link_mode":
		buf.WriteString("0\n")
	case "dormant":
		if !up {
			return linuxerr.EINVAL
		}
		buf.WriteString(boolAttr(iface.Flags&linux.IFF_DORMANT != 0))
	case "flags":
		// These flags describe the operational state, which the flags file
		// does not report.
		const operFlags = linux.IFF_RUNNING | linux.IFF_LOWER_UP | linux.IFF_DORMANT
		fmt.Fprintf(buf, "%#x\n", iface.Flags&^operFlags)
	case "ifindex", "iflink":
		fmt.Fprintf(buf, "%d\n", f.index)
	case "mtu":
		fmt.Fprintf(buf, "%d\n", iface.MTU)
	case "operstate":
		switch {
		case !up || iface.Flags&linux.IFF_RUNNING == 0:
			buf.WriteString("down\n")
		case iface.Flags&linux.IFF_LOOPBACK != 0 || iface.Kind == "tun":
			// These devices do not report their operational state.
			buf.WriteString("unknown\n")
		default:
			buf.WriteString("up\n")
		}
	case "type":
		fmt.Fprintf(buf, "%d\n", iface.DeviceType)
	case "uevent":
		if iface.Kind == "bridge" {
			buf.WriteString("DEVTYPE=bridge\n")
		}
		fmt.Fprintf(buf, "INTERFACE=%s\nIFINDEX=%d\n", iface.Name, f.index)
	default:
		panic(fmt.Sprintf("unknown network device attribute %q", f.attr))
	}
	return nil
}

// hardwareAddr formats addr like the address and broadcast files.
func hardwareAddr(addr []byte) string {
	var b bytes.Buffer
	for i, c := range addr {
		if i > 0 {
			b.WriteByte(':')
		}
		fmt.Fprintf(&b, "%02x", c)
	}
	b.WriteByte('\n')
	return b.String()
}

// boolAttr formats v like the carrier and dormant files.
func boolAttr(v bool) string {
	if v {
		return "1\n"
	}
	return "0\n"
}
