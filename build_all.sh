#!/bin/bash

# 定义颜色
RED='\033[0;31m'
GREEN='\033[0;32m'
WHITE='\033[0;37m'
NC='\033[0m' # 无颜色

# 定义操作系统和架构列表
OS_LIST=("windows" "linux" "darwin" "freebsd")
ARCH_LIST=("amd64" "arm64" "386" "arm" "loong64")

# 创建构建目录
mkdir -p ./build

# 获取当前版本：同时支持上游纯 SemVer tag（1.5.11 / v1.5.11）与我方 Fork 发版 tag
#（v1.5.11-fork.1 / 1.5.11-fork.1）。纯数字短 SHA（如 0123456）在 SemVer 预发布段
# 属非法前导零，故 SHA 前缀固定加 "g"；Fork 发版 tag 用 +g<sha> 构建元数据溯源。
SHORT_SHA=$(git rev-parse --short HEAD 2>/dev/null || true)
if [ -z "$SHORT_SHA" ]; then
  SHORT_SHA="nogit"
fi
RE_FORK_TAG='^[0-9]+\.[0-9]+\.[0-9]+-fork\.[0-9]+$'
RE_SEMVER='^[0-9]+\.[0-9]+\.[0-9]+$'

EXACT_TAG=$(git describe --tags --exact-match HEAD 2>/dev/null || true)
if [ -n "$EXACT_TAG" ]; then
  CANDIDATE="${EXACT_TAG#v}"
  CANDIDATE="${CANDIDATE#V}"
  if echo "$CANDIDATE" | grep -qE "$RE_FORK_TAG"; then
    # 我方 Fork 发版 tag：完整展示 1.5.11-fork.1 并附 +g<sha> 构建溯源
    VERSION="${CANDIDATE}+g${SHORT_SHA}"
  elif echo "$CANDIDATE" | grep -qE "$RE_SEMVER"; then
    # 上游纯 SemVer tag
    VERSION="${CANDIDATE}-fork.g${SHORT_SHA}"
  else
    VERSION="0.0.1-fork.g${SHORT_SHA}"
  fi
else
  TAG=$(git describe --tags --abbrev=0 2>/dev/null || true)
  if [ -n "$TAG" ]; then
    CANDIDATE="${TAG#v}"
    CANDIDATE="${CANDIDATE#V}"
    if echo "$CANDIDATE" | grep -qE "$RE_FORK_TAG"; then
      VERSION="${CANDIDATE}.dev.g${SHORT_SHA}"
    elif echo "$CANDIDATE" | grep -qE "$RE_SEMVER"; then
      VERSION="${CANDIDATE}-fork.g${SHORT_SHA}"
    else
      VERSION="0.0.1-fork.g${SHORT_SHA}"
    fi
  else
    VERSION="0.0.1-fork.g${SHORT_SHA}"
  fi
fi

# 初始化失败列表
FAILED_BUILDS=()

# 遍历操作系统和架构组合
for GOOS in "${OS_LIST[@]}"; do
  for GOARCH in "${ARCH_LIST[@]}"; do
    # 排除仅由 Linux 支持的 loong64，以及 windows/arm、darwin/386 和 darwin/arm
    if { [ "$GOOS" = "windows" ] && [ "$GOARCH" = "arm" ]; } || \
       { [ "$GOOS" = "darwin" ] && { [ "$GOARCH" = "386" ] || [ "$GOARCH" = "arm" ]; }; } || \
       { [ "$GOOS" != "linux" ] && [ "$GOARCH" = "loong64" ]; }; then
      continue
    fi

    echo -e "Building for $GOOS/$GOARCH..."

    # 设置输出二进制文件名
    BINARY_NAME="komari-agent-${GOOS}-${GOARCH}"
    if [ "$GOOS" = "windows" ]; then
      BINARY_NAME="${BINARY_NAME}.exe"
    fi

    # 构建二进制文件
    env GOOS=$GOOS GOARCH=$GOARCH CGO_ENABLED=0 go build -trimpath -ldflags="-X github.com/komari-monitor/komari-agent/update.CurrentVersion=${VERSION}" -o "./build/$BINARY_NAME"

    if [ $? -ne 0 ]; then
      echo -e "${RED}Failed to build for $GOOS/$GOARCH${NC}"
      FAILED_BUILDS+=("$GOOS/$GOARCH")
    else
      echo -e "${GREEN}Successfully built $BINARY_NAME${NC}"
    fi
  done
done

# 输出失败的构建
if [ ${#FAILED_BUILDS[@]} -ne 0 ]; then
  echo -e "\n${RED}The following builds failed:${NC}"
  for BUILD in "${FAILED_BUILDS[@]}"; do
    echo -e "${RED}- $BUILD${NC}"
  done
else
  echo -e "\n${GREEN}All builds completed successfully.${NC}"
fi

# 提示构建完成
echo -e "\nBinaries are in the ./build directory."
