# Fast Web Proxy Browser — Go + Chromium

링크를 입력하면 서버에서 Chromium을 실행하고, 브라우저 화면을 JPEG screencast로 웹 클라이언트에 전달하는 구조입니다.

## Render 배포

최상위의 `render.yaml`을 Render **New > Blueprint**로 연결하면 Docker 이미지가 빌드됩니다. 서버는 Render가 제공하는 `PORT`를 자동으로 사용하고 `/healthz`를 상태 확인 경로로 제공합니다. 프로필은 `/tmp/profiles`에 저장되므로 재배포나 인스턴스 교체 시 로그인 상태가 사라질 수 있습니다.

## 들어간 것
- Go control server
- Chromium persistent profile
- YouTube / Discord / Instagram HTTPS allowlist
- CDP Page screencast
- JPEG 화면 스트리밍
- WebSocket transport
- 링크 이동
- 뒤로/앞으로/새로고침
- 마우스/키보드 이벤트 transport 기반
- 세션별 Chromium profile
- 로그인 쿠키/LocalStorage 등이 profile에 지속될 기반
- 모바일 터치 UI
- 자동재생 허용 옵션
- Facebook 및 OAuth 이동 허용

## 중요한 제한
YouTube/Discord/Instagram의 실제 서비스는 영상/음성/웹RTC/DRM/Canvas/WebGL 등 매우 복잡합니다.
이 프로젝트는 화면 원격 브라우저의 기반 구현이며, 완전한 상용 원격 브라우저 수준의 오디오/비디오/입력 호환성을 보장하지 않습니다.

특히 브라우저의 탭 오디오/마이크를 사용자 기기로 보내려면 별도 WebRTC media pipeline이 필요합니다.
단순 JPEG screencast에는 오디오가 포함되지 않습니다.

## 실행
Chromium이 설치된 Linux 환경에서:
```bash
go mod tidy
go run ./cmd/server
```

 Docker 환경에서는 포함된 `Dockerfile`을 사용합니다.

## 보안
현재는 allowlist가 YouTube/Discord/Instagram hostname으로 제한되어 있습니다.
인터넷 공개 전:
- TLS
- 사용자 인증
- rate limit
- WebSocket Origin 검증
- 세션 만료
- 프로필 저장소 접근통제
를 추가하세요.

CAPTCHA, 패스키, DRM 또는 서비스 보안 장치를 우회하는 기능은 포함하지 않습니다.
