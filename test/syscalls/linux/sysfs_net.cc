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

#include <dirent.h>
#include <fcntl.h>
#include <sched.h>
#include <sys/ioctl.h>
#include <sys/socket.h>
#include <unistd.h>

// Linux network headers require <net/if.h> first.
// clang-format off
#include <net/if.h>
// clang-format on

#include <linux/capability.h>
#include <linux/if_arp.h>
#include <linux/if_link.h>
#include <linux/if_tun.h>
#include <linux/netlink.h>
#include <linux/rtnetlink.h>

#include <climits>
#include <cstdlib>
#include <cstring>
#include <set>
#include <string>
#include <vector>

#include "gmock/gmock.h"
#include "gtest/gtest.h"
#include "absl/strings/match.h"
#include "absl/strings/numbers.h"
#include "absl/strings/str_cat.h"
#include "absl/strings/str_format.h"
#include "absl/strings/str_join.h"
#include "absl/strings/strip.h"
#include "test/syscalls/linux/socket_netlink_route_util.h"
#include "test/syscalls/linux/socket_netlink_util.h"
#include "test/util/capability_util.h"
#include "test/util/cleanup.h"
#include "test/util/file_descriptor.h"
#include "test/util/fs_util.h"
#include "test/util/logging.h"
#include "test/util/mount_util.h"
#include "test/util/posix_error.h"
#include "test/util/save_util.h"
#include "test/util/socket_util.h"
#include "test/util/temp_path.h"
#include "test/util/test_util.h"

namespace gvisor {
namespace testing {

namespace {

constexpr char kClassNet[] = "/sys/class/net";

// Returns the names of the entries of dir, other than "." and "..".
std::set<std::string> DirEntries(const std::string& dir) {
  std::set<std::string> entries;
  DIR* d = opendir(dir.c_str());
  TEST_PCHECK(d != nullptr);
  while (struct dirent* de = readdir(d)) {
    std::string name = de->d_name;
    if (name != "." && name != "..") {
      entries.insert(name);
    }
  }
  closedir(d);
  return entries;
}

// Returns the names of the network interfaces from if_nameindex(3).
std::set<std::string> InterfaceNames() {
  std::set<std::string> names;
  struct if_nameindex* ifs = if_nameindex();
  if (ifs == nullptr) {
    return names;
  }
  for (struct if_nameindex* i = ifs; i->if_index != 0; i++) {
    names.insert(i->if_name);
  }
  if_freenameindex(ifs);
  return names;
}

// Returns the contents of attribute attr of the network interface name.
PosixErrorOr<std::string> Attr(const std::string& name,
                               const std::string& attr) {
  return GetContents(absl::StrCat(kClassNet, "/", name, "/", attr));
}

// Returns the result of ioctl(req) on the network interface name.
PosixErrorOr<struct ifreq> InterfaceIoctl(const std::string& name,
                                          unsigned long req) {
  ASSIGN_OR_RETURN_ERRNO(FileDescriptor sock, Socket(AF_INET, SOCK_DGRAM, 0));
  struct ifreq ifr = {};
  strncpy(ifr.ifr_name, name.c_str(), IFNAMSIZ - 1);
  RETURN_ERROR_IF_SYSCALL_FAIL(ioctl(sock.get(), req, &ifr));
  return ifr;
}

TEST(SysfsNetTest, ListsInterfaces) {
  std::set<std::string> entries;
  DIR* dir = opendir(kClassNet);
  ASSERT_NE(dir, nullptr) << "opendir: " << strerror(errno);
  while (struct dirent* de = readdir(dir)) {
    std::string name = de->d_name;
    if (name == "." || name == "..") {
      continue;
    }
    EXPECT_EQ(de->d_type, DT_LNK) << name;
    entries.insert(name);
  }
  closedir(dir);

  EXPECT_EQ(entries, InterfaceNames());
  EXPECT_THAT(entries, ::testing::Contains("lo"));
}

TEST(SysfsNetTest, Symlinks) {
  for (const std::string& name : InterfaceNames()) {
    SCOPED_TRACE(name);
    std::string link =
        ASSERT_NO_ERRNO_AND_VALUE(ReadLink(absl::StrCat(kClassNet, "/", name)));
    EXPECT_TRUE(absl::StartsWith(link, "../../devices/")) << link;
    EXPECT_TRUE(absl::EndsWith(link, absl::StrCat("/net/", name))) << link;
    char subsystem[PATH_MAX];
    ASSERT_NE(realpath(absl::StrCat(kClassNet, "/", name, "/subsystem").c_str(),
                       subsystem),
              nullptr);
    EXPECT_STREQ(subsystem, kClassNet);
  }
}

TEST(SysfsNetTest, Attributes) {
  for (const std::string& name : InterfaceNames()) {
    SCOPED_TRACE(name);
    unsigned int index = if_nametoindex(name.c_str());
    ASSERT_NE(index, 0);
    EXPECT_THAT(Attr(name, "ifindex"),
                IsPosixErrorOkAndHolds(absl::StrCat(index, "\n")));
    // Some kinds of devices, such as bridges, add a DEVTYPE line first.
    EXPECT_THAT(Attr(name, "uevent"),
                IsPosixErrorOkAndHolds(::testing::EndsWith(absl::StrCat(
                    "INTERFACE=", name, "\nIFINDEX=", index, "\n"))));

    struct ifreq ifr =
        ASSERT_NO_ERRNO_AND_VALUE(InterfaceIoctl(name, SIOCGIFMTU));
    EXPECT_THAT(Attr(name, "mtu"),
                IsPosixErrorOkAndHolds(absl::StrCat(ifr.ifr_mtu, "\n")));

    // The flags file does not report the operational state flags. Unlike
    // SIOCGIFFLAGS, it reports IFF_PROMISC and IFF_ALLMULTI when the kernel
    // sets them, for example on bridge ports.
    ifr = ASSERT_NO_ERRNO_AND_VALUE(InterfaceIoctl(name, SIOCGIFFLAGS));
    unsigned int flags = static_cast<unsigned short>(ifr.ifr_flags);
    std::string flags_attr = ASSERT_NO_ERRNO_AND_VALUE(Attr(name, "flags"));
    unsigned int attr_flags;
    ASSERT_TRUE(
        absl::SimpleHexAtoi(absl::StripSuffix(flags_attr, "\n"), &attr_flags))
        << flags_attr;
    constexpr unsigned int kPromisc = IFF_PROMISC | IFF_ALLMULTI;
    EXPECT_EQ(attr_flags & ~kPromisc, flags & ~(IFF_RUNNING | kPromisc));
    if ((flags & IFF_UP) && (flags & IFF_RUNNING)) {
      EXPECT_THAT(Attr(name, "carrier"), IsPosixErrorOkAndHolds("1\n"));
      EXPECT_THAT(Attr(name, "dormant"), IsPosixErrorOkAndHolds("0\n"));
      // Some devices, such as loopback, do not report their operational
      // state, which is then unknown.
      if (flags & IFF_LOOPBACK) {
        EXPECT_THAT(Attr(name, "operstate"),
                    IsPosixErrorOkAndHolds("unknown\n"));
      } else {
        EXPECT_THAT(
            Attr(name, "operstate"),
            IsPosixErrorOkAndHolds(::testing::AnyOf("up\n", "unknown\n")));
      }
    }

    ifr = ASSERT_NO_ERRNO_AND_VALUE(InterfaceIoctl(name, SIOCGIFHWADDR));
    int type = ifr.ifr_hwaddr.sa_family;
    EXPECT_THAT(Attr(name, "type"),
                IsPosixErrorOkAndHolds(absl::StrCat(type, "\n")));
    if (type == ARPHRD_ETHER || type == ARPHRD_LOOPBACK) {
      std::vector<std::string> bytes;
      for (int i = 0; i < 6; i++) {
        bytes.push_back(absl::StrFormat(
            "%02x", static_cast<unsigned char>(ifr.ifr_hwaddr.sa_data[i])));
      }
      EXPECT_THAT(Attr(name, "addr_len"), IsPosixErrorOkAndHolds("6\n"));
      EXPECT_THAT(Attr(name, "address"), IsPosixErrorOkAndHolds(absl::StrCat(
                                             absl::StrJoin(bytes, ":"), "\n")));
      EXPECT_THAT(
          Attr(name, "broadcast"),
          IsPosixErrorOkAndHolds(type == ARPHRD_ETHER ? "ff:ff:ff:ff:ff:ff\n"
                                                      : "00:00:00:00:00:00\n"));
    }
  }
}

TEST(SysfsNetTest, SeekEnd) {
  FileDescriptor fd =
      ASSERT_NO_ERRNO_AND_VALUE(Open(kClassNet, O_RDONLY | O_DIRECTORY));
  EXPECT_THAT(lseek(fd.get(), 0, SEEK_END), SyscallSucceedsWithValue(0));
}

TEST(SysfsNetTest, NonexistentInterface) {
  EXPECT_THAT(Attr("nonexistent0", "ifindex"), PosixErrorIs(ENOENT));
}

struct LinkRequest {
  struct nlmsghdr hdr;
  struct ifinfomsg ifm;
  char buf[256];
};

void AddAttr(LinkRequest* req, int type, const void* data, int len) {
  struct rtattr* rta = reinterpret_cast<struct rtattr*>(
      reinterpret_cast<char*>(req) + NLMSG_ALIGN(req->hdr.nlmsg_len));
  TEST_CHECK(NLMSG_ALIGN(req->hdr.nlmsg_len) + RTA_SPACE(len) <= sizeof(*req));
  rta->rta_type = type;
  rta->rta_len = RTA_LENGTH(len);
  memcpy(RTA_DATA(rta), data, len);
  req->hdr.nlmsg_len = NLMSG_ALIGN(req->hdr.nlmsg_len) + RTA_SPACE(len);
}

// Creates the bridge name.
PosixError NewBridge(const std::string& name) {
  ASSIGN_OR_RETURN_ERRNO(FileDescriptor fd, NetlinkBoundSocket(NETLINK_ROUTE));
  LinkRequest req = {};
  req.hdr.nlmsg_len = NLMSG_LENGTH(sizeof(req.ifm));
  req.hdr.nlmsg_type = RTM_NEWLINK;
  req.hdr.nlmsg_flags = NLM_F_REQUEST | NLM_F_ACK | NLM_F_CREATE | NLM_F_EXCL;
  req.hdr.nlmsg_seq = 1;
  AddAttr(&req, IFLA_IFNAME, name.c_str(), name.size());
  struct rtattr* linkinfo = reinterpret_cast<struct rtattr*>(
      reinterpret_cast<char*>(&req) + NLMSG_ALIGN(req.hdr.nlmsg_len));
  AddAttr(&req, IFLA_LINKINFO, nullptr, 0);
  AddAttr(&req, IFLA_INFO_KIND, "bridge", strlen("bridge"));
  linkinfo->rta_len = reinterpret_cast<char*>(&req) + req.hdr.nlmsg_len -
                      reinterpret_cast<char*>(linkinfo);
  return NetlinkRequestAckOrError(fd, req.hdr.nlmsg_seq, &req,
                                  req.hdr.nlmsg_len);
}

// Deletes the network interface with the given index.
PosixError DelLink(int index) {
  ASSIGN_OR_RETURN_ERRNO(FileDescriptor fd, NetlinkBoundSocket(NETLINK_ROUTE));
  LinkRequest req = {};
  req.hdr.nlmsg_len = NLMSG_LENGTH(sizeof(req.ifm));
  req.hdr.nlmsg_type = RTM_DELLINK;
  req.hdr.nlmsg_flags = NLM_F_REQUEST | NLM_F_ACK;
  req.hdr.nlmsg_seq = 2;
  req.ifm.ifi_index = index;
  return NetlinkRequestAckOrError(fd, req.hdr.nlmsg_seq, &req,
                                  req.hdr.nlmsg_len);
}

// A sysfs mount shows the network interfaces of the network namespace of the
// task that mounted it.
TEST(SysfsNetTest, NetworkNamespace) {
  SKIP_IF(!ASSERT_NO_ERRNO_AND_VALUE(HaveCapability(CAP_NET_ADMIN)));
  SKIP_IF(!ASSERT_NO_ERRNO_AND_VALUE(HaveCapability(CAP_SYS_ADMIN)));
  SKIP_IF(IsRunningWithHostinet());
  // Don't do cooperative save/restore because netstack state is not restored.
  // TODO(gvisor.dev/issue/4595): enable cooperative save tests.
  const DisableSave ds;

  FileDescriptor netns =
      ASSERT_NO_ERRNO_AND_VALUE(Open("/proc/thread-self/ns/net", O_RDONLY));
  ASSERT_THAT(unshare(CLONE_NEWNET), SyscallSucceeds());
  Cleanup restore_netns([&netns] {
    EXPECT_THAT(setns(netns.get(), CLONE_NEWNET), SyscallSucceeds());
  });
  TempPath dir = ASSERT_NO_ERRNO_AND_VALUE(TempPath::CreateDir());
  Cleanup mount =
      ASSERT_NO_ERRNO_AND_VALUE(Mount("sysfs", dir.path(), "sysfs", 0, "", 0));
  const std::string class_net = JoinPath(dir.path(), "class/net");
  EXPECT_EQ(DirEntries(class_net), std::set<std::string>{"lo"});

  PosixError err = NewBridge("br0");
  // Native Linux kernels may not be built with CONFIG_BRIDGE.
  SKIP_IF(!IsRunningOnGvisor() && err.errno_value() == EOPNOTSUPP);
  ASSERT_NO_ERRNO(err);
  const int index = if_nametoindex("br0");
  ASSERT_NE(index, 0);
  EXPECT_EQ(DirEntries(class_net), (std::set<std::string>{"br0", "lo"}));
  EXPECT_THAT(DirEntries(kClassNet),
              ::testing::Not(::testing::Contains("br0")));
  EXPECT_THAT(GetContents(JoinPath(class_net, "br0/uevent")),
              IsPosixErrorOkAndHolds(absl::StrCat(
                  "DEVTYPE=bridge\nINTERFACE=br0\nIFINDEX=", index, "\n")));

  ASSERT_NO_ERRNO(LinkChangeFlags(index, 0, IFF_UP));
  EXPECT_THAT(GetContents(JoinPath(class_net, "br0/carrier")),
              PosixErrorIs(EINVAL));
  EXPECT_THAT(GetContents(JoinPath(class_net, "br0/operstate")),
              IsPosixErrorOkAndHolds("down\n"));

  ASSERT_NO_ERRNO(DelLink(index));
  EXPECT_EQ(DirEntries(class_net), std::set<std::string>{"lo"});
  EXPECT_THAT(GetContents(JoinPath(class_net, "br0/ifindex")),
              PosixErrorIs(ENOENT));

  // A TUN device does not report its operational state.
  FileDescriptor tun = ASSERT_NO_ERRNO_AND_VALUE(Open("/dev/net/tun", O_RDWR));
  struct ifreq ifr = {};
  strncpy(ifr.ifr_name, "tun0", IFNAMSIZ - 1);
  ifr.ifr_flags = IFF_TUN | IFF_NO_PI;
  ASSERT_THAT(ioctl(tun.get(), TUNSETIFF, &ifr), SyscallSucceeds());
  const int tun_index = if_nametoindex("tun0");
  ASSERT_NE(tun_index, 0);
  ASSERT_NO_ERRNO(LinkChangeFlags(tun_index, IFF_UP, IFF_UP));
  EXPECT_THAT(GetContents(JoinPath(class_net, "tun0/type")),
              IsPosixErrorOkAndHolds(absl::StrCat(ARPHRD_NONE, "\n")));
  EXPECT_THAT(GetContents(JoinPath(class_net, "tun0/operstate")),
              IsPosixErrorOkAndHolds("unknown\n"));
}

}  // namespace

}  // namespace testing
}  // namespace gvisor
