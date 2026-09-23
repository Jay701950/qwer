# Render Safe Proxy

프론트 화면에 원하는 링크를 붙여넣으면 같은 화면의 프록시 경유 iframe에서 열어주는 Render용 범용 웹 프록시입니다.

## 사용 방법

배포 후 서비스 루트 주소를 열고 주소 입력창에 링크를 넣습니다.

```text
https://YOUR-SERVICE.onrender.com/
```

입력한 URL은 `/view?url=...`로 열리고, 페이지 안의 일반적인 링크·이미지·폼·스타일시트 경로도 프록시 경로로 재작성됩니다. 따라서 페이지 안에서 링크를 클릭해도 프록시 화면이 유지됩니다.

## 엔드포인트

- `GET /`: 링크 입력 UI
- `GET /view?url=https://example.com/path`: UI가 사용하는 브라우저용 중계 경로
- `GET /proxy?url=https://example.com/path`: API 클라이언트용 중계 경로; `X-API-Key` 또는 Bearer 인증 필요
- `GET /health`: 상태 확인

## Render 배포

1. 이 폴더를 GitHub 저장소에 push합니다.
2. Render에서 **New > Blueprint**로 저장소를 연결합니다.
3. `PROXY_API_KEY`는 Render가 생성한 값을 사용합니다.
4. 범용 모드는 `ALLOW_ANY_DOMAIN=true`로 설정되어 있습니다.
5. 특정 사이트만 허용하려면 `ALLOW_ANY_DOMAIN=false`로 바꾸고 `ALLOWED_DOMAINS`에 도메인을 쉼표로 입력하세요.

## 보안 및 운영 제한

- `http`와 `https`만 허용합니다.
- loopback, 사설 IPv4/IPv6, link-local, 예약 주소, 클라우드 메타데이터 호스트를 차단합니다.
- 리다이렉트도 매 단계 재검증하며 최대 5회까지만 따라갑니다.
- 요청별 20초 타임아웃, 응답 본문 10 MiB 제한, 기본 분당 60회 IP별 rate limit을 적용합니다.
- DNS 조회를 5분간 캐시하고, 이미지·폰트·JS·동영상 등 비HTML 응답은 전체를 메모리에 모으지 않고 즉시 스트리밍합니다. upstream 연결을 재사용하고 정적 응답에는 브라우저 캐시 헤더를 적용합니다.
- `/view`는 프론트 사용성을 위해 별도 API 키 없이 제공되지만 rate limit과 SSRF 차단은 적용됩니다. 외부에 공개할 경우 Render 인증, 도메인 allowlist, 더 낮은 rate limit을 권장합니다.
- WebSocket, SSE, POST 업로드, JavaScript 코드 내부 URL, 서비스 워커, 복잡한 로그인 세션은 완전히 지원하지 않습니다.

이 구현은 일반적인 공개 웹페이지 열람용입니다. 완전한 브라우저 엔진 기반의 VPN·익명화 도구는 아닙니다.

## 속도 관련

Render 무료 플랜의 유휴 슬립은 코드로 제거할 수 없습니다. 첫 요청이 느리면 콜드 스타트일 가능성이 크고, 이번 구조에서는 큰 리소스가 다운로드 완료될 때까지 기다리지 않고 첫 바이트부터 브라우저로 전달합니다. 배포 후 코드 변경이 반영되려면 새 커밋을 push하고 Render에서 최신 커밋을 배포하세요.
