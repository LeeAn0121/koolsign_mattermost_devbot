# Mattermost DevBot 운영 가이드

## 아키텍처

```
인터넷
  │
  ▼
[Nginx :443] ─── m.koolsign.net
  │                 /            → mm-app:80   (Mattermost)
  │                 /webhook/*   → mm-bot:9000
  │                 /slash       → mm-bot:9000
  │                 /bot-action  → mm-bot:9000
  │
  ├── mm-cs.koolsign.net     → 172.17.0.1:8066 (외부 서비스)
  └── mm-report.koolsign.net → 172.17.0.1:8067 (외부 서비스)

[mm-bot :9000]
  ├── WebSocket → mm-app (멘션 수신)
  ├── /webhook/gitlab  (GitLab Webhook)
  ├── /webhook/gitea   (Gitea Webhook)
  ├── /slash           (슬래시 커맨드)
  ├── /bot-action      (배포 확인 버튼)
  └── /health          (헬스체크)

[mm-postgres :5432] ← mm-app
```

---

## 컨테이너 구성

| 컨테이너 | 이미지 | 역할 |
|---|---|---|
| `mm-nginx` | nginx:latest | 리버스 프록시, SSL 종단 |
| `mm-app` | mattermost/mattermost-team-edition:latest | Mattermost 본체 |
| `mm-postgres` | postgres:14 | DB (포트 55432 외부 노출) |
| `mm-bot` | golang:1.22-alpine | DevBot (go run .) |

```bash
# 전체 기동
docker compose up -d

# 로그 확인
docker logs -f mm-bot
docker logs -f mm-app

# 재시작
docker compose restart mm-bot
```

---

## 환경변수 (.env)

파일 위치: `./mm-bot/.env`

| 변수 | 설명 | 상태 |
|---|---|---|
| `MM_SERVER_URL` | Mattermost URL | ✅ 설정됨 |
| `MM_BOT_TOKEN` | Bot 계정 토큰 | ✅ 설정됨 |
| `MM_WEBHOOK_CODE` | #code-review 채널 Webhook | ✅ 설정됨 |
| `MM_WEBHOOK_DEPLOY` | #deploy-log 채널 Webhook | ✅ 설정됨 |
| `MM_WEBHOOK_ALERT` | #alert 채널 Webhook | ✅ 설정됨 |
| `GITLAB_SECRET` | GitLab Webhook 시크릿 | ✅ 설정됨 |
| `GITEA1_SECRET` | Gitea-1 HMAC 시크릿 | ✅ 설정됨 |
| `GITEA2_SECRET` | Gitea-2 HMAC 시크릿 | ✅ 설정됨 |
| `KMA_API_KEY` | 기상청 API Hub 키 | ✅ 설정됨 |
| `WEATHER_DEFAULT_CITY` | 기본 날씨 도시 (서울) | ✅ 설정됨 |
| `DB_DSN` | MySQL 읽기전용 DSN | ⚠️ 미설정 (`/dbquery` 비활성) |
| `BOT_ACTION_URL` | 배포 버튼 콜백 URL | ✅ 설정됨 |

---

## 슬래시 커맨드

Mattermost → 통합 → 슬래시 커맨드에서 등록. 요청 URL: `https://m.koolsign.net/slash`

| 커맨드 | 토큰 변수 | 기능 |
|---|---|---|
| `/serverstatus` | `SLASH_TOKEN_STATUS` | 컨테이너·디스크·메모리 상태 |
| `/log [컨테이너] [줄수]` | `SLASH_TOKEN_LOG` | 컨테이너 로그 조회 |
| `/deploy [staging\|prod]` | `SLASH_TOKEN_DEPLOY` | 배포 실행 |
| `/schedule HH:MM 메시지` | `SLASH_TOKEN_SCHEDULE` | 예약 메시지 등록 |
| `/schedule list` | | 예약 목록 |
| `/schedule cancel [ID]` | | 예약 취소 |
| `/weather [도시명]` | `SLASH_TOKEN_WEATHER` | 기상청 현재 날씨 |
| `/announce 메시지` | `SLASH_TOKEN_ANNOUNCE` | 즉시 공지 |
| `/announce ~채널 메시지` | | 특정 채널 공지 |
| `/announce HH:MM 메시지` | | 예약 공지 |
| `/commands` | `SLASH_TOKEN_HELP` | 전체 명령어 목록 |
| `/dbquery SELECT ...` | `SLASH_TOKEN_DBQUERY` | DB 조회 (DB_DSN 필요) |

---

## Bot 멘션 커맨드

Bot 사용자명으로 멘션하면 동작. 예: `@devbot 상태`

| 멘션 | 기능 |
|---|---|
| `@devbot 도움말` / `help` | 명령어 목록 |
| `@devbot 상태` / `status` | 서버 상태 |
| `@devbot 컨테이너` / `ps` | 컨테이너 목록 |
| `@devbot 로그 [컨테이너] [줄수]` | 컨테이너 로그 |
| `@devbot 배포 [staging\|prod]` | 배포 (확인 버튼 포함) |
| `@devbot 예약 HH:MM 메시지` | 예약 메시지 |
| `@devbot 예약목록` | 예약 목록 |
| `@devbot 예약취소 [ID]` | 예약 취소 |
| `@devbot 날씨 [도시명]` | 날씨 조회 |
| `@devbot 공지 메시지` | 즉시 공지 |
| `@devbot 공지 ~채널 메시지` | 특정 채널 공지 |
| `@devbot 공지 HH:MM 메시지` | 예약 공지 |

---

## Webhook 연동

### GitLab

- Webhook URL: `https://m.koolsign.net/webhook/gitlab`
- Secret Token: `GITLAB_SECRET` 값 (`.env` 참고)
- 지원 이벤트: Push, Merge Request, Issue, Pipeline, Tag Push
- System Hook도 지원 (Push/MR/Issue/Pipeline/Tag 자동 분기)

### Gitea

- Gitea-1 URL: `https://m.koolsign.net/webhook/gitea?src=gitea1`
- Gitea-2 URL: `https://m.koolsign.net/webhook/gitea?src=gitea2`
- Secret: HMAC-SHA256 방식 (`X-Gitea-Signature` 헤더)
- 지원 이벤트: push, pull_request, issues, release

**채널 라우팅**

| 이벤트 | 전송 채널 |
|---|---|
| Push, MR/PR, Issue | `MM_WEBHOOK_CODE` |
| Pipeline, Tag, Release, 배포 | `MM_WEBHOOK_DEPLOY` |
| 디스크 90% 초과, 프로덕션 배포 실패 | `MM_WEBHOOK_ALERT` |

---

## 배포 스크립트

스크립트 위치: `./mm-bot/scripts/` (컨테이너 내 `/app/scripts/`)

**동작 순서:**

1. `docker pull` — 최신 이미지 받기
2. `docker restart` — 컨테이너 재시작
3. 헬스체크 루프 (staging: 30초, prod: 60초)
4. 성공/실패 결과 → `#deploy-log` 채널 전송
5. prod 실패 시 → `#alert` 채널 추가 전송

> 실제 git pull 기반 배포가 필요하면 스크립트에 직접 추가.

---

## 날씨 지원 도시

서울, 인천, 수원, 성남, 의정부, 춘천, 원주, 강릉, 대전, 세종, 청주, 천안, 전주, 군산, 광주, 목포, 여수, 순천, 대구, 포항, 경주, 안동, 구미, 울산, 부산, 창원, 진주, 제주, 서귀포

---

## 운영 주의사항

- **예약 메시지는 메모리 저장** — mm-bot 재시작 시 전체 소멸
- **디스크 감시** — 1시간마다 체크, 90% 초과 시 `#alert` 알림
- **DB 쿼리** — SELECT만 허용, INSERT/UPDATE/DELETE/DROP 등 차단
- **인증서 경로** — `/opt/mattermost/nginx/cert/fullchain.pem`, `privkey.pem`
- **포트 55432** — Postgres 외부 노출됨, 방화벽으로 접근 제한 권장
