#!/usr/bin/env bats
#
# spec-42 §6 / §11 R5: workspace-local auth-helpers must shadow image-baked
# helpers of the same name. This test starts the post-Phase-2 image with both
# directories populated and asserts `command -v` resolves to the workspace
# copy.
#
# Run: bats tests/container/path-precedence.bats
#
# Requires: docker, the locally-built fracta image tagged `fracta-test`.
# Build with: docker build -t fracta-test .

IMAGE="${FRACTA_TEST_IMAGE:-fracta-test}"

setup() {
    WORKDIR="$(mktemp -d)"
    mkdir -p "${WORKDIR}/.fracta/auth-helpers"
    cat > "${WORKDIR}/.fracta/auth-helpers/foo" <<'EOF'
#!/bin/sh
echo workspace-foo
EOF
    chmod 0755 "${WORKDIR}/.fracta/auth-helpers/foo"
}

teardown() {
    rm -rf "${WORKDIR}"
}

@test "workspace .fracta/auth-helpers shadows /opt/fracta/auth-helpers on PATH" {
    # Bake an image-side helper named `foo` into a derived image so we can test
    # shadowing without modifying the production image.
    DERIVED="$(mktemp -d)"
    cat > "${DERIVED}/Dockerfile" <<EOF
FROM ${IMAGE}
RUN printf '#!/bin/sh\necho image-foo\n' > /opt/fracta/auth-helpers/foo \
 && chmod 0755 /opt/fracta/auth-helpers/foo
EOF
    docker build -t fracta-test-pp "${DERIVED}" >/dev/null
    rm -rf "${DERIVED}"

    # Run with the workspace mounted at /workspace (matches the production
    # WORKDIR). The entrypoint sets PATH from $PWD, so /workspace becomes the
    # workspace-local prefix.
    run docker run --rm -v "${WORKDIR}:/workspace" --entrypoint /bin/sh fracta-test-pp \
        -c '. /usr/local/bin/entrypoint.sh </dev/null >/dev/null 2>&1 || true; foo'
    [ "$status" -eq 0 ]
    [[ "$output" == *workspace-foo* ]]
}

@test "without workspace helper, image-baked /opt/fracta/auth-helpers/foo wins" {
    DERIVED="$(mktemp -d)"
    cat > "${DERIVED}/Dockerfile" <<EOF
FROM ${IMAGE}
RUN printf '#!/bin/sh\necho image-foo\n' > /opt/fracta/auth-helpers/foo \
 && chmod 0755 /opt/fracta/auth-helpers/foo
EOF
    docker build -t fracta-test-pp "${DERIVED}" >/dev/null
    rm -rf "${DERIVED}"

    EMPTY="$(mktemp -d)"
    run docker run --rm -v "${EMPTY}:/workspace" --entrypoint /bin/sh fracta-test-pp \
        -c '. /usr/local/bin/entrypoint.sh </dev/null >/dev/null 2>&1 || true; foo'
    rm -rf "${EMPTY}"
    [ "$status" -eq 0 ]
    [[ "$output" == *image-foo* ]]
}

@test "image ships no fetch-bedrock-token (BC2 regression guard)" {
    run docker run --rm --entrypoint /bin/sh "${IMAGE}" -c 'command -v fetch-bedrock-token'
    [ "$status" -ne 0 ]
}

@test "image ships an empty /opt/fracta/auth-helpers (A1 regression guard)" {
    run docker run --rm --entrypoint /bin/sh "${IMAGE}" -c 'ls -A /opt/fracta/auth-helpers'
    [ "$status" -eq 0 ]
    [ -z "$output" ]
}
