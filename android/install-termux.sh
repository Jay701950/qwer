#!/data/data/com.termux/files/usr/bin/bash
set -euo pipefail

pkg update -y
pkg install -y golang git x11-repo
pkg install -y chromium

mkdir -p "$HOME/browser-relay"
cd "$HOME/browser-relay"
if [ ! -f go.mod ]; then
  echo "프로젝트 파일을 이 디렉터리에 복사한 뒤 다시 실행하세요."
  exit 1
fi

go mod download
go build -o browser-relay ./cmd/server
chmod +x browser-relay android/start-termux.sh 2>/dev/null || true
echo "설치 완료. ./android/start-termux.sh 로 실행하세요."
