# tilt/build.Tiltfile — compilation and image building.
#
# load()'d by the root Tiltfile. Exports setup() which resolves the commit
# hash, builds the otelc-native binary, and registers custom_build() for all
# 5 beats.

def setup(cluster):
    """Compiles otelc-native and registers custom_build for every beat.

    Args:
        cluster: the resolved CLUSTERS[cluster_name] dict.
    """

    DEV_REGISTRY = cluster["registry"]

    # COMMIT is resolved once at Tiltfile load (not per-build), so it reflects
    # the last commit, not uncommitted dirty-tree edits, and only refreshes on
    # Tiltfile reload / the next `tilt up` — an accepted tradeoff for a fast
    # edit loop here. DATE is deliberately left as the Dockerfile's "unknown"
    # default under Tilt — a per-build wall-clock stamp isn't worth the noise.
    COMMIT = str(local("git rev-parse --verify HEAD || echo none")).strip()

    # Fast host-native compilation for local dev loop (Option 2).
    #
    # `go tool otelc go build` (not a bare `go build`), same entry point
    # Taskfile.yaml's `build` task uses — the only supported one. otelc's own
    # instrumentation packages never live in go.mod (upstream issue #585,
    # .gitignore's comment, Taskfile.yaml's otelc:check-gomod); `otelc go
    # build` pins them in transiently, builds, and unpins in one invocation. A
    # standalone `otelc setup` beforehand (this file's prior approach) writes
    # cmd/*/otelc.runtime.go without ever pinning go.mod to match it, so a
    # plain `go build` right after can't resolve the packages it just
    # generated imports for — reproduced live 2026-08-10, see
    # thump-running-notes.
    #
    # `go tool otelc` itself, NOT `./otelc-native` below, is what `task build`
    # runs (Taskfile.yaml) — that path never cross-compiles, so it never hits
    # the bug this local()+direct-invocation dance works around.
    #
    # `go tool <name>` resolves and EXECUTES a binary honoring the ambient
    # GOOS/GOARCH, same as `go run` — and just as broken cross-OS: GOOS=linux
    # is hardcoded below for every profile here (containers are always Linux,
    # whatever the host is), so `go tool otelc` tries to build ITSELF as a
    # Linux binary and then exec it on this Darwin host, failing "exec format
    # error". Reproduced live 2026-08-11 on the dev profile, but the bug is
    # universal. Building otelc once here, under the host's own native
    # GOOS/GOARCH (no override), and invoking that binary directly per beat —
    # bypassing `go tool`'s dispatch entirely — sidesteps it: the binary itself
    # always runs host-native, and only the `go build` it execs internally (a
    # plain subprocess inheriting the per-beat env below) ever sees GOOS=linux.
    OTELC_NATIVE = "bin/dev/otelc-native"
    local(
        "mkdir -p bin/dev && go build -o " + OTELC_NATIVE + " go.opentelemetry.io/otelc/tool/cmd/otelc",
        quiet = True,
        echo_off = True,
    )

    if cluster["platform"] == "linux/amd64":
        target_arch = "amd64"
        platform_flag = "--platform linux/amd64 "
    else:
        target_arch = "arm64"
        platform_flag = ""

    for beat in ["rattle", "clank", "hiss", "thump", "bootstrap"]:
        # Narrowed to what this beat actually imports, not every beat's own
        # unrelated internal/ subtree — phase AS 0B.2. Before this, editing
        # rattle's config seam retriggered clank/hiss/thump's custom_build too
        # (all 5 shared the same deps = ["cmd/" + beat, "internal"]). `go list
        # -deps` is resolved once per beat at Tiltfile load, same one-time
        # cost as COMMIT above; it does not re-run mid-session.
        deps = _beat_deps(beat)

        cmd = (
            "mkdir -p bin/dev && "
            + "CGO_ENABLED=0 GOOS=linux GOARCH=" + target_arch + " "
            + "./" + OTELC_NATIVE + " go build -ldflags '-s -w -X main.version=dev -X main.commit=" + COMMIT + "' "
            + "-o bin/dev/" + beat + " ./cmd/" + beat + " && "
            + "docker build " + platform_flag + "-f Dockerfile.dev --build-arg BEAT=" + beat + " -t $EXPECTED_REF bin/dev"
        )
        custom_build(
            DEV_REGISTRY + "/thump-" + beat,
            command = cmd,
            # .otelc-build is NOT a dep, deliberately: it's otelc's own
            # transient build-state directory (.gitignore's comment), written
            # by the `go build` step above as output — not read as a source
            # input by anything. Listing it here (an earlier version of this
            # loop did) makes custom_build watch its own output: every build
            # writes to .otelc-build, which Tilt sees as a changed dep and
            # re-triggers immediately — an infinite self-rebuild loop, observed
            # live 2026-08-11 as thump-bootstrap rebuilding continuously. The
            # fix is to never list a build's own output directory as one of its
            # deps.
            #
            # go.mod/go.sum are ALSO not deps, for the same reason, one level
            # deeper: `otelc go build` (this loop's `cmd`, above) pins its
            # instrumentation packages into the module root's go.mod/go.sum,
            # builds, then unpins — a real write-then-restore on disk, not
            # metadata. Confirmed live 2026-08-11 by reading otelc v1.0.1's
            # own source (tool/internal/setup/state.go's getBackupFiles,
            # pin.go's AutoPin): every `otelc go build` backs up and restores
            # go.mod and go.sum around the build, unconditionally. Watching
            # them here means every beat's build re-triggers itself — and since
            # all 5 beats share one module root, one beat's build re-triggers
            # all 5, compounding the loop instead of just repeating it. A real
            # go.mod edit (an actual `go get`) won't auto-rebuild under Tilt
            # anymore; `tilt trigger` after one is the manual escape hatch,
            # worth it to not live-loop the build on every single compile.
            deps = deps,
        )


def _beat_deps(beat):
    """Returns cmd/<beat> plus the internal/ packages it actually imports.

    Replaces the blanket ["cmd/" + beat, "internal"] every beat shared before
    phase AS 0B.2, which retriggered all 5 beats' custom_build on any one
    beat's internal/ edit. `go list -deps` walks the real import graph, so
    rattle's build only watches internal/rattle, internal/beat, internal/poll,
    etc — not internal/hiss or internal/thump.
    """
    raw = str(local(
        "go list -deps ./cmd/" + beat + " | grep '^github.com/ianeff/thump/internal/' || true",
        quiet = True,
        echo_off = True,
    )).strip()
    pkgs = [p.replace("github.com/ianeff/thump/", "", 1) for p in raw.split("\n") if p]
    return ["cmd/" + beat] + pkgs
