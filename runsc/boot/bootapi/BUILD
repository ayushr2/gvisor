load("//tools:defs.bzl", "go_library", "go_test")

package(
    default_applicable_licenses = ["//:license"],
    licenses = ["notice"],
)

go_library(
    name = "bootapi",
    srcs = [
        "checkpoint.go",
        "controller.go",
        "events.go",
        "fs.go",
        "mount_hints.go",
        "network.go",
        "seccheck.go",
    ],
    visibility = [
        "//pkg/test:__subpackages__",
        "//runsc:__subpackages__",
        "//test:__subpackages__",
    ],
    deps = [
        "//pkg/fsutil",
        "//pkg/log",
        "//pkg/sentry/checkpoint",
        "//pkg/sentry/fscheckpoint",
        "//pkg/sentry/seccheck",
        "//pkg/sentry/seccheck/sinks/null",
        "//pkg/sentry/seccheck/sinks/remote",
        "//pkg/state/statefile",
        "//pkg/urpc",
        "//runsc/config",
        "//runsc/specutils",
        "@com_github_opencontainers_runtime_spec//specs-go:go_default_library",
    ],
)

go_test(
    name = "bootapi_test",
    size = "small",
    srcs = [
        "checkpoint_test.go",
        "mount_hints_test.go",
        "seccheck_test.go",
    ],
    library = ":bootapi",
    deps = [
        "//pkg/sentry/seccheck",
        "//runsc/config",
        "@com_github_opencontainers_runtime_spec//specs-go:go_default_library",
    ],
)
