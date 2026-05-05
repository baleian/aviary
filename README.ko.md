# Aviary

**AI 에이전트를 만들고 운영하고 오케스트레이션하는 셀프 호스팅 플랫폼.**

[English](./README.md)

Aviary는 팀이 자체 AI 에이전트를 직접 만들고 운영할 수 있게 해주는 오픈소스 멀티테넌트 플랫폼입니다. 웹 UI에서 에이전트에게 instruction과 모델, 도구([MCP](https://modelcontextprotocol.io/) 서버 포함)를 지정한 뒤 바로 대화하거나, 여러 에이전트를 워크플로우로 엮어 실행할 수 있습니다. 모든 에이전트는 샌드박스 런타임 안에서 동작하기 때문에, 코드·데이터·인터넷을 다루게 해도 안전합니다.

Aviary는 기존 조직 환경에 그대로 얹혀 쓰도록 설계되었습니다 — 사내 OIDC IdP, HashiCorp Vault, 모델 공급자(Anthropic / AWS Bedrock / 자체 호스팅 Ollama·vLLM)와 직접 연동됩니다.

## 핵심 기능

- **전체 라이프사이클을 한 웹 UI에서** — 에이전트 생성·설정·대화·삭제, 워크플로우 조합, 실행 이력 조회.
- **원하는 모델 선택** — 모델 이름만 바꾸면 Anthropic / AWS Bedrock / 자체 호스팅 Ollama·vLLM 사이에서 자유롭게 전환.
- **1급 MCP 도구 통합** — MCP 서버를 등록하고 에이전트별로 사용 도구를 선택, 사용자별 자격증명을 Vault에서 주입.
- **에이전트 간 호출 (A2A)** — `@멘션`으로 에이전트가 다른 에이전트를 서브 도구처럼 호출, 서브 에이전트의 실행 내용이 부모 대화에 인라인 렌더링.
- **워크플로우** — [Temporal](https://temporal.io/) 위에서 에이전트와 결정적 스텝을 DAG로 엮어 실행; UI에서 재개·재생·조회.
- **기본적으로 안전** — 매 에이전트 턴이 [bubblewrap](https://github.com/containers/bubblewrap) 샌드박스 안에서 실행.
- **멀티테넌트·유저 단위 격리** — OIDC 로그인, 사용자별 API 키·도구 자격증명을 Vault에 저장, 세션별 독립 워크스페이스.

## 아키텍처

```
        ┌──────────────────────────────────────────────────────┐
        │                       Web UI                         │
        │     에이전트 · 워크플로우 · 채팅 · 실행 이력 · 관리      │
        └──────────────────────────┬───────────────────────────┘
                                   │ single origin
                                   │ (REST + WebSocket)
                  ┌────────────────▼────────────────┐
                  │       Edge proxy (nginx)        │
                  │   /api/* → API ·  /* → Web      │
                  └──────┬──────────────────┬───────┘
                         │                  │
        ┌────────────────▼───────┐  ┌───────▼─────────────────┐
        │        API 서버         │  │       Admin 콘솔        │
        │  인증 · CRUD · 채팅     │  │  에이전트 / 워크플로우    │
        │                         │  │  정의 관리              │
        └──────┬─────────────┬───┘  └─────────────────────────┘
               │             │
               │    ┌────────▼─────────────────────────────────┐
               │    │             플랫폼 서비스                 │
               │    │   LiteLLM Gateway                        │
               │    │    ├─ LLM 라우팅 (Anthropic / Bedrock /  │
               │    │    │   Ollama / vLLM …)                  │
               │    │    └─ MCP 통합 엔드포인트                 │
               │    │   Vault · Keycloak · Postgres · Redis    │
               │    │   Temporal · Prometheus · Grafana        │
               │    └──────────────────┬───────────────────────┘
               │                       │
        ┌──────▼───────────────────┐   │
        │    Agent Supervisor      │   │
        │  SSE 프록시 · abort ·    │   │
        │  메트릭                  │   │
        └──────────────┬───────────┘   │
                       │ HTTP          │
        ┌──────────────▼───────────────▼───────────────────────┐
        │              Agent Runtime                           │
        │   in-compose `runtime` 컨테이너 — agent-agnostic.     │
        │   격리는 요청 단위의 bubblewrap + 세션별 경로로        │
        │   이루어집니다.                                        │
        └──────────────────────────────────────────────────────┘
```

## 컴포넌트

| 컴포넌트 | 역할 |
|----------|------|
| **Web UI** ([web/](web/)) | 사용자·운영자용 Next.js 프론트엔드. |
| **API 서버** ([api/](api/)) | OIDC 인증 기반 에이전트·세션·메시지·워크플로우 REST + WebSocket API. |
| **Admin 콘솔** ([admin/](admin/)) | 로컬 전용 운영자 UI (인증 없음). 에이전트·워크플로우 정의 관리. |
| **Agent Supervisor** ([agent-supervisor/](agent-supervisor/)) | 런타임에서 오는 출력을 스트리밍하고 Redis로 팬아웃, 사용자별 자격증명 주입, abort 처리. |
| **Workflow Worker** ([workflow-worker/](workflow-worker/)) | 워크플로우를 실행하는 Temporal worker. |
| **Agent Runtime** ([runtime/](runtime/)) | Node.js + [claude-agent-sdk](https://github.com/anthropics/claude-agent-sdk-typescript) 기반 컨테이너로 실제 에이전트를 실행. |
| **LiteLLM Gateway** ([infra/config/litellm/](infra/config/litellm/)) | LLM 추론과 MCP 도구 호출의 단일 진입점. 모델 이름으로 라우팅하고 사용자별 시크릿 주입. |
| **공유 Python 패키지** ([shared/](shared/)) | API + Admin + Supervisor가 함께 쓰는 SQLAlchemy 모델·마이그레이션·OIDC 헬퍼. |

## 기술 스택

| 레이어 | 기술 |
|--------|------|
| Web UI | Next.js, TypeScript, Tailwind CSS, shadcn/ui |
| API · Admin · Supervisor | Python, FastAPI, SQLAlchemy 2.0 (async), Pydantic v2 |
| Agent Runtime | Node.js, [claude-agent-sdk](https://github.com/anthropics/claude-agent-sdk-typescript), Claude Code CLI |
| 워크플로우 | [Temporal](https://temporal.io/) |
| LLM · MCP 게이트웨이 | [LiteLLM](https://github.com/BerriAI/litellm) |
| 인증 | OIDC (어떤 IdP든 가능; 로컬은 [Keycloak](https://www.keycloak.org/) 기본 제공) |
| 시크릿 | [HashiCorp Vault](https://www.vaultproject.io/) |
| 데이터 | PostgreSQL, Redis |
| 관측 | OpenTelemetry, Prometheus, Grafana |
| 샌드박스 | [bubblewrap](https://github.com/containers/bubblewrap) |

## 설치

### 사전 요구 사항

- `docker compose v2`가 포함된 Docker (Docker Desktop, OrbStack, Rancher Desktop 등)
- 이미지·볼륨용 디스크 공간 ~10 GB
- Linux / macOS / WSL2

### 저장소 구조 — 단일 프로젝트, 두 개의 compose 파일

하나의 Compose 프로젝트(`aviary`)를 두 파일로 나눕니다(둘 다 `name: aviary` 선언 → 네트워크·볼륨·.env 공유):

| 파일 | 포함 내용 |
|------|-----------|
| [compose.yml](compose.yml) | App 서비스 — web, api, admin, supervisor, workflow-worker, runtime, proxy |
| [compose.infra.yml](compose.infra.yml) | 필수 인프라 — postgres, redis, temporal, temporal-ui, db-migrate, keycloak, vault, litellm, prometheus, grafana, otel-collector, 예제 MCP 서버 |
| [compose.override.yml](compose.override.yml) | App 서비스용 dev 전용 오버라이드 (바인드 마운트, `--reload`, web `target: dev`) |

App 서비스는 Docker service DNS(`postgres:5432`, `redis:6379`, `temporal:7233`, `keycloak:8080`, `vault:8200`, `litellm:4000`)로 인프라에 접속.

### 한 번에 기동

```bash
git clone <repository-url>
cd aviary
cp .env.example .env                  # 필요한 값 오버라이드
./scripts/setup-dev.sh                # dev all — build + up + 핫 리로드
```

### `setup-dev.sh` — 단일 진입점

```
setup-dev.sh [SUBCMD] [SCOPE]
  SUBCMD: dev (기본) | build | deploy | run | down | clean | logs | ps
  SCOPE : all (기본) | app | infra
```

| 명령 | 동작 |
|------|------|
| `./scripts/setup-dev.sh` | dev all — build + up + 핫 리로드 (override 자동 적용) |
| `./scripts/setup-dev.sh dev app` | dev app만 (infra는 이미 떠 있다고 가정) |
| `./scripts/setup-dev.sh build [scope]` | 이미지 빌드 |
| `./scripts/setup-dev.sh deploy [scope]` | prod 모드 up (override 미적용 → web은 `next start`) |
| `./scripts/setup-dev.sh run [scope]` | build + deploy |
| `./scripts/setup-dev.sh run app` | **infra 그대로, app만 재빌드+재배포** |
| `./scripts/setup-dev.sh down [scope]` | 컨테이너 정지+제거 (볼륨 보존) |
| `./scripts/setup-dev.sh clean [scope]` | down + 볼륨 삭제 |
| `./scripts/setup-dev.sh logs [scope] [svc…]` | 로그 tail |
| `./scripts/setup-dev.sh ps [scope]` | 컨테이너 상태 |

`api/`, `admin/`, `web/`, `agent-supervisor/`, `workflow-worker/` 소스는 `compose.override.yml`에서 바인드 마운트되어 대부분 `--reload` / `npm run dev`로 핫 리로드됩니다.

세밀한 반복 작업은 compose를 직접 다루세요:

```bash
docker compose up -d --build api                              # app 한 개만 빌드+재시작 (override 자동 적용)
docker compose restart supervisor                             # 빌드 없이 재시작
docker compose -f compose.infra.yml restart litellm           # LiteLLM 패치/설정 변경 후
```

## 사용

```bash
cp .env.example .env                        # 필요한 값 오버라이드
./scripts/setup-dev.sh                      # dev all
```

Web UI를 열어 Keycloak로 로그인 → `/settings?tab=credentials`에서 Anthropic API 키 저장 → 에이전트 생성 → 대화 시작. 엔드포인트는 아래 [서비스 엔드포인트](#서비스-엔드포인트) 참고.

테스트 사용자 `user1@test.com` / `user2@test.com` (비밀번호 `password`)이 Keycloak realm에 시드되어 있습니다.

### 일시 정지 / 재개 / 초기화

```bash
./scripts/setup-dev.sh down                # 모든 컨테이너 정지+제거; 볼륨 보존
./scripts/setup-dev.sh                     # 다시 실행 (빌드 적용됨)
./scripts/setup-dev.sh clean app           # app + runtime-workspace만 초기화, infra Vault/Keycloak/DB는 유지
./scripts/setup-dev.sh clean               # 전부 초기화
```

## 서비스 엔드포인트

`setup-dev.sh` 완료 후 호스트에서 접근 가능한 URL입니다.

### `service` 그룹

| 서비스 | URL | 용도 |
|--------|-----|------|
| 브라우저 진입 (nginx proxy) | http://localhost:3000 | `/api/*` → API · `/` → Web — 단일 same-origin |
| Admin 콘솔 | http://localhost:8001 | 운영자 UI (인증 없음, 로컬 전용) |

API와 Supervisor는 compose 내부 DNS(`api:8000` / `supervisor:9000`)로만 접근 가능합니다 — 호스트에 노출되지 않으므로 `docker compose exec`나 nginx proxy를 통해 접근하세요.

### `infra` 그룹

| 서비스 | URL | 로그인 / 용도 |
|--------|-----|---------------|
| Postgres | localhost:5432 | App + LiteLLM + Keycloak + Temporal DB |
| Redis | localhost:6379 | Pub/sub, unread 카운터 |
| Temporal | localhost:7233 | gRPC (worker가 여기에 연결) |
| Temporal UI | http://localhost:8233 | 워크플로우 인스펙터 |
| Keycloak | http://localhost:8080 | `admin` / `admin` |
| Vault | http://localhost:8200 | Token: `dev-root-token` |
| LiteLLM Proxy | http://localhost:8090 | Master key `sk-aviary-dev` |
| LiteLLM UI | http://localhost:8090/ui | `admin` / `admin` |
| LiteLLM MCP 엔드포인트 | http://localhost:8090/mcp | 통합 MCP |
| Grafana | http://localhost:3001 | 익명 admin; Aviary Supervisor 대시보드 자동 프로비저닝 |
| Prometheus | http://localhost:9090 | — |
| OTel Collector | localhost:4317 (gRPC) / 4318 (HTTP) | OTLP 수신기 |

테스트 계정 (Keycloak `aviary` realm): `user1@test.com`, `user2@test.com`, 비밀번호 `password`.

## 설정

- **단일 .env** — 프로젝트 루트의 [.env](.env.example)를 Compose가 자동 로드 (심볼릭 링크 불필요). `OIDC_ISSUER`, `LLM_GATEWAY_URL` / `MCP_GATEWAY_URL`, `VAULT_ADDR` / `VAULT_TOKEN`은 필수이며 미설정 시 부팅이 즉시 실패합니다.
- **LiteLLM** — 모델 라우팅과 플랫폼 공용 MCP 서버는 [infra/config/litellm/config.yaml](infra/config/litellm/config.yaml); 서버별 Vault 키 매핑은 [mcp-secret-injection.yaml](infra/config/litellm/mcp-secret-injection.yaml).
- **시크릿** — 사용자별 자격증명은 Vault의 `secret/aviary/credentials/{user_sub}/{namespace}/{key}`에 저장.

## 테스트

```bash
docker compose exec api pytest tests/ -v
docker compose exec admin pytest tests/ -v
cd agent-supervisor && uv run pytest tests/ -v
```

API/Admin 테스트는 별도의 `aviary_test` Postgres 데이터베이스에서 `NullPool`로 실행 (lifespan 없음).

## 라이선스

[LICENSE](./LICENSE) 참조.
