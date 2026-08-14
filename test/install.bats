#!/usr/bin/env bats

SCRIPT="${BATS_TEST_DIRNAME}/../install.sh"

setup() {
  MOCK_BIN="$(mktemp -d)"
  export MOCK_BIN
  export PATH="${MOCK_BIN}:${PATH}"
  WORK="$(mktemp -d)"
  export WORK
}

teardown() {
  rm -rf "${MOCK_BIN}" "${WORK}"
}

mock_uname() {
  printf '#!/bin/sh\necho %s\n' "$1" > "${MOCK_BIN}/uname"
  chmod +x "${MOCK_BIN}/uname"
}

# Linux runners have sha256sum, macOS has shasum; pick whichever exists so the
# same suite runs on both.
sum_tool() {
  if command -v sha256sum >/dev/null 2>&1; then echo "sha256sum"; else echo "shasum"; fi
}

digest_of() {
  case "$(sum_tool)" in
    sha256sum) sha256sum "$1" | awk '{print $1}' ;;
    shasum)    shasum -a 256 "$1" | awk '{print $1}' ;;
  esac
}

# curl replacement that ignores its arguments and prints a fixed body, so
# api_fetch's output can be controlled without a network call.
mock_curl() {
  cat > "${MOCK_BIN}/curl" <<EOF
#!/bin/sh
cat <<'BODY'
$1
BODY
EOF
  chmod +x "${MOCK_BIN}/curl"
}

# --- shebang ---

@test "install.sh requires bash: shebang is #!/usr/bin/env bash" {
  [ "$(head -n 1 "${SCRIPT}")" = "#!/usr/bin/env bash" ]
}

# --- detect_os ---

@test "detect_os: Linux -> linux" {
  mock_uname Linux
  run bash -c "source '${SCRIPT}' && detect_os"
  [ "$status" -eq 0 ]
  [ "$output" = "linux" ]
}

@test "detect_os: Darwin -> darwin" {
  mock_uname Darwin
  run bash -c "source '${SCRIPT}' && detect_os"
  [ "$status" -eq 0 ]
  [ "$output" = "darwin" ]
}

@test "detect_os: unsupported OS fails" {
  mock_uname MINGW64_NT-10.0
  run bash -c "source '${SCRIPT}' && detect_os"
  [ "$status" -ne 0 ]
  [[ "$output" == *"Unsupported OS"* ]]
}

# --- detect_arch ---

@test "detect_arch: x86_64 -> amd64" {
  mock_uname x86_64
  run bash -c "source '${SCRIPT}' && detect_arch"
  [ "$status" -eq 0 ]
  [ "$output" = "amd64" ]
}

@test "detect_arch: aarch64 -> arm64" {
  mock_uname aarch64
  run bash -c "source '${SCRIPT}' && detect_arch"
  [ "$status" -eq 0 ]
  [ "$output" = "arm64" ]
}

@test "detect_arch: arm64 -> arm64" {
  mock_uname arm64
  run bash -c "source '${SCRIPT}' && detect_arch"
  [ "$status" -eq 0 ]
  [ "$output" = "arm64" ]
}

@test "detect_arch: unsupported arch fails" {
  mock_uname riscv64
  run bash -c "source '${SCRIPT}' && detect_arch"
  [ "$status" -ne 0 ]
  [[ "$output" == *"Unsupported architecture"* ]]
}

# --- archive_name ---

@test "archive_name: stable tag" {
  run bash -c "source '${SCRIPT}' && archive_name v1.2.3 linux amd64"
  [ "$status" -eq 0 ]
  [ "$output" = "nem_v1.2.3_linux_amd64.tar.gz" ]
}

@test "archive_name: unstable channel" {
  run bash -c "source '${SCRIPT}' && archive_name unstable darwin arm64"
  [ "$status" -eq 0 ]
  [ "$output" = "nem_unstable_darwin_arm64.tar.gz" ]
}

# --- verify_checksum ---

@test "verify_checksum: matching digest passes" {
  archive="${WORK}/nem_unstable_linux_amd64.tar.gz"
  echo "payload" > "${archive}"
  printf '%s  nem_unstable_linux_amd64.tar.gz\n' "$(digest_of "${archive}")" > "${WORK}/checksums.txt"
  run bash -c "source '${SCRIPT}' && verify_checksum '${archive}' '${WORK}/checksums.txt' '$(sum_tool)'"
  [ "$status" -eq 0 ]
}

@test "verify_checksum: mismatched digest fails" {
  archive="${WORK}/nem_unstable_linux_amd64.tar.gz"
  echo "payload" > "${archive}"
  printf '%064d  nem_unstable_linux_amd64.tar.gz\n' 0 > "${WORK}/checksums.txt"
  run bash -c "source '${SCRIPT}' && verify_checksum '${archive}' '${WORK}/checksums.txt' '$(sum_tool)'"
  [ "$status" -ne 0 ]
  [[ "$output" == *"Checksum mismatch"* ]]
}

@test "verify_checksum: missing entry fails" {
  archive="${WORK}/nem_unstable_linux_amd64.tar.gz"
  echo "payload" > "${archive}"
  echo "deadbeef  some_other_file.tar.gz" > "${WORK}/checksums.txt"
  run bash -c "source '${SCRIPT}' && verify_checksum '${archive}' '${WORK}/checksums.txt' '$(sum_tool)'"
  [ "$status" -ne 0 ]
  [[ "$output" == *"No checksum entry"* ]]
}

# --- resolve_version ---
#
# The script does not set `shopt -s inherit_errexit`, so a command
# substitution subshell does not abort on the internal grep/sed pipeline
# failure. main() calls resolve_version as `version="$(resolve_version ...)"`,
# so these tests use the same command-substitution form: calling the
# function directly (without $(...)) would let `set -euo pipefail` abort the
# pipeline before the "Failed to resolve version" guard runs.

@test "resolve_version: empty API response fails with message" {
  mock_curl ""
  run bash -c "source '${SCRIPT}' && version=\"\$(resolve_version curl)\""
  [ "$status" -ne 0 ]
  [[ "$output" == *"Failed to resolve version"* ]]
}

@test "resolve_version: rate-limited API response fails with message" {
  mock_curl '{"message":"API rate limit exceeded"}'
  run bash -c "source '${SCRIPT}' && version=\"\$(resolve_version curl)\""
  [ "$status" -ne 0 ]
  [[ "$output" == *"Failed to resolve version"* ]]
}
