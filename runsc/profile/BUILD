load("//tools:defs.bzl", "go_library")

package(
    default_applicable_licenses = ["//:license"],
    licenses = ["notice"],
)

go_library(
    name = "profile",
    srcs = ["profile.go"],
    visibility = [
        "//runsc:__subpackages__",
    ],
    deps = [
        "//pkg/control/api",
        "//pkg/log",
        "//runsc/config",
        "//runsc/donation",
        "//runsc/flag",
    ],
)
