load("//tools:defs.bzl", "go_library")

package(
    default_applicable_licenses = ["//:license"],
    licenses = ["notice"],
)

go_library(
    name = "bootapi",
    srcs = [
        "controller.go",
        "events.go",
        "network.go",
        "seccheck.go",
    ],
    visibility = [
        "//pkg/test:__subpackages__",
        "//runsc:__subpackages__",
        "//test:__subpackages__",
    ],
    deps = [
        "//pkg/sentry/checkpoint",
        "//pkg/sentry/seccheck",
        "//pkg/sentry/seccheck/sinks/null",
        "//pkg/sentry/seccheck/sinks/remote",
        "//pkg/urpc",
        "//runsc/config",
        "//runsc/specutils",
        "@com_github_opencontainers_runtime_spec//specs-go:go_default_library",
    ],
)
