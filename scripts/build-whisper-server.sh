#!/usr/bin/env bash
# Builds whisper.cpp's whisper-server for one platform and copies it, plus the
# shared libraries it loads, into an output directory (by default the
# electron-builder resources dir, app/resources/<os>/<arch>/).
#
#   scripts/build-whisper-server.sh [--os mac|linux|win] [--arch arm64|x64] [--out DIR]
#   scripts/build-whisper-server.sh --print-version
#
# --os/--arch default to the host. Only native builds are supported, except
# macOS x64, which is cross-built on arm64.
#
# Per platform:
#   mac    one static executable: Metal backend with the shader library
#          embedded, CPU backend, Apple Accelerate; links only system frameworks.
#   linux  whisper-server + libwhisper/libggml/libggml-base + dynamically loaded
#          backends (libggml-vulkan.so, libggml-cpu-<variant>.so), found through
#          an $ORIGIN RPATH. Built inside a manylinux_2_28 container (glibc 2.28)
#          unless --native is given or it already runs inside one.
#   win    whisper-server.exe + whisper/ggml/ggml-base DLLs + backend DLLs
#          (ggml-vulkan.dll, ggml-cpu-*.dll), found by same-directory DLL lookup;
#          static MSVC runtime. Needs an MSVC developer environment (cl/link on
#          PATH, e.g. from vcvarsall.bat); arm64 builds with clang-cl.
# The Vulkan loader itself (libvulkan.so.1 / vulkan-1.dll) comes with the GPU
# driver and is not shipped; without it ggml skips the Vulkan backend and runs
# on the CPU.
#
# Environment:
#   WHISPER_WORK_DIR   scratch dir for sources and build trees
#                      (default: $TMPDIR/loop-whisper-build)
#   WHISPER_JOBS       parallel build jobs (default: CPU count)
set -euo pipefail

# The pinned versions. The CI cache key hashes this file, so changing any of
# them rebuilds.
WHISPER_CPP_VERSION=v1.9.5
WHISPER_CPP_SHA256=ff1a9053feb509ff9d7729703355541ae9690073a6b1c40eb692c962e0dc1720
# Build-time Vulkan tooling (glslc + headers); nothing from these is shipped.
SHADERC_VERSION=v2026.4
VULKAN_SDK_VERSION=1.4.363.1
MANYLINUX_IMAGE=quay.io/pypa/manylinux_2_28
MACOS_DEPLOYMENT_TARGET=12.0

REPO_ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"

die() { echo "build-whisper-server: $*" >&2; exit 1; }
log() { echo "==> $*" >&2; }

host_os() {
	case "$(uname -s)" in
		Darwin) echo mac ;;
		Linux) echo linux ;;
		MINGW* | MSYS* | CYGWIN*) echo win ;;
		*) die "unsupported host OS $(uname -s)" ;;
	esac
}

host_arch() {
	local m
	m="$(uname -m)"
	# Git Bash on Windows arm64 can report the x64 emulation layer.
	if [ "$(host_os)" = win ] && [ "${PROCESSOR_ARCHITECTURE:-}" = ARM64 ]; then
		m=arm64
	fi
	case "$m" in
		arm64 | aarch64) echo arm64 ;;
		x86_64 | amd64) echo x64 ;;
		*) die "unsupported host arch $m" ;;
	esac
}

jobs() {
	if [ -n "${WHISPER_JOBS:-}" ]; then echo "$WHISPER_JOBS"; return; fi
	getconf _NPROCESSORS_ONLN 2>/dev/null || sysctl -n hw.ncpu 2>/dev/null || echo "${NUMBER_OF_PROCESSORS:-4}"
}

sha256() {
	if command -v sha256sum >/dev/null 2>&1; then sha256sum "$1" | cut -d' ' -f1; else shasum -a 256 "$1" | cut -d' ' -f1; fi
}

# fetch_whisper extracts the pinned, checksum-verified whisper.cpp source and
# prints its directory.
fetch_whisper() {
	local tarball="$WORK/whisper.cpp-$WHISPER_CPP_VERSION.tar.gz"
	local src="$WORK/whisper.cpp-${WHISPER_CPP_VERSION#v}"
	if [ ! -f "$src/CMakeLists.txt" ]; then
		if [ ! -f "$tarball" ] || [ "$(sha256 "$tarball")" != "$WHISPER_CPP_SHA256" ]; then
			log "downloading whisper.cpp $WHISPER_CPP_VERSION"
			curl -fsSL --retry 5 --retry-delay 5 -o "$tarball" \
				"https://github.com/ggml-org/whisper.cpp/archive/refs/tags/$WHISPER_CPP_VERSION.tar.gz"
		fi
		[ "$(sha256 "$tarball")" = "$WHISPER_CPP_SHA256" ] || die "checksum mismatch for $tarball"
		tar -xzf "$tarball" -C "$WORK"
	fi
	echo "$src"
}

# Options shared by every target: release build of the server only, nothing
# that needs extra runtime libraries (no OpenMP, curl, SDL), no native -march.
common_flags() {
	echo -DCMAKE_BUILD_TYPE=Release \
		-DWHISPER_BUILD_TESTS=OFF \
		-DWHISPER_BUILD_EXAMPLES=ON \
		-DWHISPER_BUILD_SERVER=ON \
		-DWHISPER_SDL2=OFF \
		-DWHISPER_CURL=OFF \
		-DWHISPER_BUILD_IS_DEV=OFF \
		-DGGML_NATIVE=OFF \
		-DGGML_OPENMP=OFF \
		-DGGML_CCACHE=OFF
}

build_mac() {
	local src build osx_arch
	src="$(fetch_whisper)"
	case "$ARCH" in arm64) osx_arch=arm64 ;; x64) osx_arch=x86_64 ;; esac
	build="$WORK/build-mac-$ARCH"
	log "configuring macOS $osx_arch"
	# shellcheck disable=SC2046
	cmake -S "$src" -B "$build" $(common_flags) \
		-DBUILD_SHARED_LIBS=OFF \
		-DGGML_METAL=ON \
		-DGGML_METAL_EMBED_LIBRARY=ON \
		-DGGML_BLAS=ON \
		-DGGML_BLAS_VENDOR=Apple \
		-DCMAKE_OSX_ARCHITECTURES="$osx_arch" \
		-DCMAKE_OSX_DEPLOYMENT_TARGET="$MACOS_DEPLOYMENT_TARGET"
	cmake --build "$build" --config Release --target whisper-server -j "$(jobs)"
	mkdir -p "$OUT"
	rm -f "$OUT"/whisper-server
	cp "$build/bin/whisper-server" "$OUT/whisper-server"
	strip -x "$OUT/whisper-server"
}

# install_linux_deps installs the Vulkan build tooling inside the manylinux
# container: the loader to link against (from the distro), and glslc plus
# SPIR-V headers built from shaderc (the distro has no glslc).
install_linux_deps() {
	local prefix="$WORK/vulkan-tools-$SHADERC_VERSION"
	VULKAN_PREFIX="$prefix"
	if ! rpm -q vulkan-loader-devel vulkan-headers >/dev/null 2>&1; then
		log "installing vulkan-loader-devel"
		dnf install -y -q vulkan-loader-devel vulkan-headers >&2
	fi
	if [ -x "$prefix/bin/glslc" ] && [ -f "$prefix/share/cmake/SPIRV-Headers/SPIRV-HeadersConfig.cmake" ]; then
		return
	fi
	log "building glslc (shaderc $SHADERC_VERSION)"
	local src="$WORK/shaderc-$SHADERC_VERSION"
	if [ ! -d "$src/.git" ]; then
		rm -rf "$src"
		git clone -q --depth 1 --branch "$SHADERC_VERSION" https://github.com/google/shaderc.git "$src"
	fi
	python3 "$src/utils/git-sync-deps" >&2
	cmake -S "$src" -B "$src/build" -DCMAKE_BUILD_TYPE=Release \
		-DCMAKE_INSTALL_PREFIX="$prefix" \
		-DSHADERC_SKIP_TESTS=ON -DSHADERC_SKIP_EXAMPLES=ON -DSHADERC_SKIP_COPYRIGHT_CHECK=ON \
		-DSPIRV_SKIP_TESTS=ON -DSPIRV_SKIP_EXECUTABLES=ON \
		-DENABLE_GLSLANG_BINARIES=OFF -DSPIRV_HEADERS_SKIP_EXAMPLES=ON >&2
	cmake --build "$src/build" --target glslc_exe -j "$(jobs)" >&2
	mkdir -p "$prefix/bin"
	cp "$src/build/glslc/glslc" "$prefix/bin/glslc"
	cmake -S "$src/third_party/spirv-headers" -B "$src/build-spirv-headers" \
		-DCMAKE_INSTALL_PREFIX="$prefix" -DSPIRV_HEADERS_ENABLE_TESTS=OFF >&2
	cmake --install "$src/build-spirv-headers" >&2
}

build_linux() {
	if [ "$NATIVE" = 0 ] && [ -z "${AUDITWHEEL_PLAT:-}" ]; then
		build_linux_in_container
		return
	fi
	if [ -n "${WHISPER_HOST_CA:-}" ] && [ -f "$WHISPER_HOST_CA" ]; then
		cat /etc/pki/tls/certs/ca-bundle.crt "$WHISPER_HOST_CA" >/tmp/ca-bundle.pem
		export CURL_CA_BUNDLE=/tmp/ca-bundle.pem SSL_CERT_FILE=/tmp/ca-bundle.pem GIT_SSL_CAINFO=/tmp/ca-bundle.pem
	fi
	local src build
	src="$(fetch_whisper)"
	local vulkan_flags=()
	if [ "$NATIVE" = 0 ]; then
		install_linux_deps
		# ggml-vulkan includes <spirv/unified1/spirv.hpp> without linking the
		# SPIRV-Headers target, so its include directory is added explicitly.
		vulkan_flags=(-DVulkan_GLSLC_EXECUTABLE="$VULKAN_PREFIX/bin/glslc" -DCMAKE_PREFIX_PATH="$VULKAN_PREFIX"
			-DCMAKE_CXX_FLAGS="-I$VULKAN_PREFIX/include")
	fi
	build="$WORK/build-linux-$ARCH"
	log "configuring Linux $ARCH"
	# shellcheck disable=SC2046,SC2016
	cmake -S "$src" -B "$build" $(common_flags) ${vulkan_flags[@]+"${vulkan_flags[@]}"} \
		-DBUILD_SHARED_LIBS=ON \
		-DGGML_BACKEND_DL=ON \
		-DGGML_CPU_ALL_VARIANTS=ON \
		-DGGML_VULKAN=ON \
		-DCMAKE_INSTALL_RPATH='$ORIGIN' \
		-DCMAKE_BUILD_WITH_INSTALL_RPATH=ON
	# The backends are MODULE libraries the executable does not link, so build
	# them explicitly alongside it.
	cmake --build "$build" --config Release --target whisper-server ggml -j "$(jobs)"
	mkdir -p "$OUT"
	rm -f "$OUT"/whisper-server "$OUT"/lib*.so*
	cp "$build/bin/whisper-server" "$OUT/"
	# Each shared library is copied once, under the name the dynamic linker
	# looks for (its SONAME), so no symlinks are needed in the app bundle.
	local f name
	for f in "$build"/bin/lib*.so*; do
		[ -L "$f" ] && continue
		name="$(objdump -p "$f" | awk '$1 == "SONAME" { print $2 }')"
		cp "$f" "$OUT/${name:-$(basename "$f")}"
	done
	strip --strip-unneeded "$OUT"/whisper-server "$OUT"/lib*.so*
}

# build_linux_in_container reruns this script inside the manylinux image for
# the host arch, so the result runs on glibc 2.28+ distros.
build_linux_in_container() {
	local image arch_suffix
	case "$ARCH" in arm64) arch_suffix=aarch64 ;; x64) arch_suffix=x86_64 ;; esac
	image="${MANYLINUX_IMAGE}_${arch_suffix}"
	mkdir -p "$OUT" "$WORK"
	# Hosts behind a TLS-intercepting proxy trust its CA in the system keychain
	# (macOS) or a bundle named by WHISPER_CA_BUNDLE; hand it to the container
	# so its downloads verify.
	local ca_args=() ca_tmp=""
	if [ -n "${WHISPER_CA_BUNDLE:-}" ]; then
		ca_args=(-v "$WHISPER_CA_BUNDLE:/host-ca.pem:ro" -e WHISPER_HOST_CA=/host-ca.pem)
	elif [ "$(uname -s)" = Darwin ]; then
		ca_tmp="$(mktemp -t loop-whisper-ca)"
		security find-certificate -a -p /Library/Keychains/System.keychain >>"$ca_tmp" 2>/dev/null || true
		security find-certificate -a -p /System/Library/Keychains/SystemRootCertificates.keychain >>"$ca_tmp" 2>/dev/null || true
		if [ -s "$ca_tmp" ]; then
			ca_args=(-v "$ca_tmp:/host-ca.pem:ro" -e WHISPER_HOST_CA=/host-ca.pem)
		fi
	fi
	log "building in $image"
	local rc=0
	docker run --rm ${ca_args[@]+"${ca_args[@]}"} \
		-v "$REPO_ROOT/scripts:/scripts:ro" \
		-v "$WORK:/work" \
		-v "$(cd "$OUT" && pwd):/out" \
		-e WHISPER_WORK_DIR=/work \
		-e WHISPER_JOBS="$(jobs)" \
		"$image" bash /scripts/build-whisper-server.sh --os linux --arch "$ARCH" --out /out || rc=$?
	[ -n "$ca_tmp" ] && rm -f "$ca_tmp"
	return $rc
}

# install_vulkan_sdk installs the LunarG Vulkan SDK (headers, glslc, SPIR-V
# headers) into the work dir unless VULKAN_SDK already points at one.
install_vulkan_sdk() {
	if [ -n "${VULKAN_SDK:-}" ] && [ -d "$VULKAN_SDK/Include/vulkan" ]; then
		return
	fi
	local dir="$WORK/VulkanSDK-$VULKAN_SDK_VERSION-$ARCH" platform file
	case "$ARCH" in
		x64) platform=windows; file="vulkansdk-windows-X64-$VULKAN_SDK_VERSION.exe" ;;
		arm64) platform=warm; file="vulkansdk-windows-ARM64-$VULKAN_SDK_VERSION.exe" ;;
	esac
	if [ ! -d "$dir/Include/vulkan" ]; then
		log "installing Vulkan SDK $VULKAN_SDK_VERSION"
		curl -fsSL --retry 5 --retry-delay 5 -o "$WORK/$file" \
			"https://sdk.lunarg.com/sdk/download/$VULKAN_SDK_VERSION/$platform/$file"
		rm -rf "$dir"
		"$WORK/$file" --root "$(cygpath -w "$dir")" --accept-licenses --default-answer --confirm-command install >&2
	fi
	# CMake reads VULKAN_SDK as a native Windows path.
	VULKAN_SDK="$(cygpath -w "$dir")"
	export VULKAN_SDK
	export PATH="$dir/Bin:$PATH"
}

build_win() {
	command -v cl >/dev/null 2>&1 || die "no cl.exe on PATH; run from an MSVC developer environment (vcvarsall.bat)"
	command -v ninja >/dev/null 2>&1 || die "no ninja on PATH"
	local src build
	src="$(fetch_whisper)"
	install_vulkan_sdk
	build="$WORK/build-win-$ARCH"
	local arch_flags=()
	if [ "$ARCH" = x64 ]; then
		arch_flags=(-DGGML_CPU_ALL_VARIANTS=ON)
	else
		# MSVC cannot target an ARM feature level without probing the build
		# host, and ggml's per-CPU variants need Linux or macOS on ARM, so
		# arm64 builds one CPU backend with clang (MSVC ABI) for the baseline
		# every Windows arm64 device (Cortex-A76 class and newer) meets.
		command -v clang >/dev/null 2>&1 || die "no clang on PATH (LLVM) for the arm64 build"
		arch_flags=(-DCMAKE_C_COMPILER=clang -DCMAKE_CXX_COMPILER=clang++
			-DGGML_CPU_ARM_ARCH=armv8.2-a+dotprod+fp16)
	fi
	log "configuring Windows $ARCH"
	# shellcheck disable=SC2046
	cmake -S "$src" -B "$build" -G Ninja $(common_flags) "${arch_flags[@]}" \
		-DBUILD_SHARED_LIBS=ON \
		-DGGML_BACKEND_DL=ON \
		-DGGML_VULKAN=ON \
		-DCMAKE_POLICY_DEFAULT_CMP0091=NEW \
		-DCMAKE_MSVC_RUNTIME_LIBRARY=MultiThreaded
	cmake --build "$build" --config Release --target whisper-server ggml -j "$(jobs)"
	mkdir -p "$OUT"
	rm -f "$OUT"/whisper-server.exe "$OUT"/*.dll
	cp "$build/bin/whisper-server.exe" "$build"/bin/*.dll "$OUT/"
}

OS="" ARCH="" OUT="" NATIVE=0
while [ $# -gt 0 ]; do
	case "$1" in
		--os) OS="$2"; shift 2 ;;
		--arch) ARCH="$2"; shift 2 ;;
		--out) OUT="$2"; shift 2 ;;
		--native) NATIVE=1; shift ;;
		--print-version) echo "$WHISPER_CPP_VERSION"; exit 0 ;;
		-h | --help) sed -n '2,/^set -euo/p' "$0" | sed '$d; s/^# \{0,1\}//'; exit 0 ;;
		*) die "unknown argument $1" ;;
	esac
done
OS="${OS:-$(host_os)}"
ARCH="${ARCH:-$(host_arch)}"
OUT="${OUT:-$REPO_ROOT/app/resources/$OS/$ARCH}"
WORK="${WHISPER_WORK_DIR:-${TMPDIR:-/tmp}/loop-whisper-build}"
mkdir -p "$WORK"
WORK="$(cd "$WORK" && pwd)"

case "$OS/$ARCH" in
	mac/arm64 | mac/x64 | linux/arm64 | linux/x64 | win/arm64 | win/x64) ;;
	*) die "unsupported target $OS/$ARCH" ;;
esac
# Linux builds run in Docker by default, so any host with Docker can make
# them (for its own arch).
if [ "$OS" != "$(host_os)" ] && { [ "$OS" != linux ] || [ "$NATIVE" = 1 ]; }; then
	die "cannot build $OS on $(host_os)"
fi
if [ "$OS" != mac ] && [ "$ARCH" != "$(host_arch)" ]; then
	die "cross-building $OS/$ARCH on $(host_arch) is not supported"
fi

"build_$OS"
log "whisper-server $WHISPER_CPP_VERSION for $OS/$ARCH in $OUT"
ls -l "$OUT" >&2
