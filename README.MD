# Kronex Engine

Kronex 거래소의 원장 및 호가창을 관리하는 Go 기반 엔진입니다.

## Table of Contents

- [Stack](#stack)
- [Queue Configuration](#queue-configuration)
- [Architecture](#architecture)
- [Folder Architecture](#folder-architecture)
- [Flow](#flow)
  - [Components](#components)
  - [Main Engine](#main-engine)
  - [Event Publisher](#event-publisher)
  - [Snapshot](#snapshot)
  - [Recover](#recover)
  - [Error Process Rules](#error-process-rules)
- [Performance](#performance)
- [Getting Started](#getting-started)
- [License](#license)

## Stack

| Category        | Technology       |
| --------------- | ---------------- |
| Language        | Go               |
| Message Queue   | RabbitMQ         |
| Database        | MySQL            |
| Query Codegen   | sqlc             |
| Write-Ahead Log | tidwall/wal      |
| Serialization   | Protocol Buffers |

## Queue Configuration

| Queue         | Purpose                                      |
| ------------- | -------------------------------------------- |
| `data_queue`  | 계좌 개설·주문 요청 등의 일반 클라이언트 요청 |
| `admin_queue` | 종목 상장·원장 조정 요청 등의 어드민 요청     |
| `event_queue` | 엔진 처리 결과 이벤트                        |

## Architecture

<img width="1002" height="991" alt="Architecture" src="https://github.com/user-attachments/assets/2184b0ca-1e62-4830-8bb6-733742140ec5" />

## Folder Architecture

```text
.
├── cmd
│   └── engine              # 엔진 실행 진입점
├── internal
│   ├── config              # 환경변수 기반 설정 로드
│   ├── core                # 메시지 라우팅, WAL 기록, dedup, 주문 검증·매칭, 어드민 요청 처리
│   ├── domain              # 엔진에서 사용하는 도메인 타입
│   ├── ledger              # 메모리 원장, 호가창, 스냅샷 직렬화/복구
│   ├── messaging           # RabbitMQ consumer/publisher 구현
│   ├── publisher           # Output WAL → DB 조회 테이블 → 이벤트 큐 발행
│   ├── storage             # MySQL 연결, 스냅샷 저장, 이벤트 반영
│   └── wal                 # tidwall/wal 래퍼
├── db
│   ├── migrations          # sqlc codegen용 schema 참조
│   └── queries             # sqlc 쿼리 정의
├── proto                   # 스냅샷용 Protocol Buffers 정의
├── Makefile
└── Dockerfile
```

## Flow

API Server가 RabbitMQ에 요청을 적재하고, 엔진이 이를 소비해 요청을 처리합니다.

처리 결과는 Output WAL에 먼저 기록한 뒤, DB Projector와 Event Publisher가 독립적으로 소비합니다.

```text
RabbitMQ request
  -> Input WAL
  -> In-memory ledger / order book
  -> Output WAL
     -> DB Projector -> DB Update
     -> Event Publisher -> RabbitMQ event
```

### Components

엔진은 아래와 같은 주요 컴포넌트로 이루어져 있습니다.

| Component       | Role                                                                      |
| --------------- | ------------------------------------------------------------------------- |
| Main Engine     | 큐 요청을 소비하고 Input WAL, 메모리 원장, Output WAL을 갱신합니다.       |
| DB Projector    | Output WAL을 순서대로 읽어 DB 조회 모델에 반영합니다.                |
| Event Publisher | Output WAL을 순서대로 읽어 이벤트 큐에 이벤트를 발행합니다.           |
| Snapshot Saver  | 5분 주기로 생성된 엔진 스냅샷을 DB에 저장합니다.                          |

Output WAL의 각 레코드는 하나의 입력 요청에서 생성된 체결, 주문 상태, 잔고 변경 이벤트를 `OutputEnvelope`로 묶습니다. 봉투에는 원본 Input WAL 인덱스인 `inputSeq`와 해당 Output WAL 인덱스인 `outputSeq`가 함께 기록됩니다.

### Main Engine

Main Engine은 요청 큐를 구독하고 요청을 순차적으로 처리합니다.

- 요청 ID는 종류별 `dedupWindow` ring buffer로 관리해 RabbitMQ 재전달로 인한 중복 처리를 방지합니다.
- 신규 요청은 Input WAL에 기록한 뒤 주문 검증, 매칭, 잔고 갱신을 수행합니다.
- 처리 결과는 Output WAL에 기록합니다.

#### Matching rule

| Rule             | Description                                         |
| ---------------- | --------------------------------------------------- |
| Price priority   | 더 유리한 가격의 주문을 먼저 체결합니다.            |
| FIFO             | 같은 가격에서는 먼저 들어온 주문을 먼저 처리합니다. |
| Limit remainder  | 지정가 미체결 잔량은 호가창에 남깁니다.             |
| Market remainder | 시장가 미체결 잔량은 취소 처리합니다.               |

### DB Projector / Event Publisher

DB Projector와 Event Publisher는 각각 독립 cursor로 Output WAL을 순서대로 읽습니다.

- DB Projector는 DB 반영과 `DB_APPLIED_OUTPUT_SEQ` 저장을 같은 transaction으로 처리합니다.
- Event Publisher는 RabbitMQ publisher confirm 성공 뒤 `MQ_PUBLISHED_OUTPUT_SEQ`를 전진합니다.
- 한 worker의 실패 또는 지연은 다른 worker의 cursor를 막지 않습니다.
- 새 Output WAL 레코드는 `outputSignal` fan-out으로 감지하며, 신호 유실에 대비해 각 worker가 1초 주기 backstop으로도 확인합니다.

### Snapshot

- 5분 간격으로 메모리 원장과 호가창을 Protocol Buffers로 직렬화합니다.
- 스냅샷은 직렬화 시점의 Input WAL index와 함께 DB에 저장됩니다.

### Recover

- 엔진은 최신 스냅샷을 복원한 뒤 스냅샷 이후의 Input WAL을 replay합니다.
- 마지막 Output WAL의 `InputSeq`를 복구 워터마크로 사용해 replay 중 Output WAL 중복 생성을 방지합니다.
- 최근 Input WAL을 읽어 dedup window를 복원합니다.
- Event Publisher는 DB의 `EVENT` cursor 이후부터 Output WAL 처리를 재개합니다.

### Error Process Rules

| Scenario                   | Handling                                      |
| -------------------------- | --------------------------------------------- |
| Input·Output WAL 쓰기 실패 | Throw                                         |
| 소비자 ACK 실패            | 에러 반환. 재전달 시 dedup window로 중복 처리 |
| DB 반영·MQ 발행 실패       | Cursor를 전진하지 않고 재시도                 |
| 유효하지 않은 주문         | `order.rejected` 이벤트 기록 후 ACK           |

## Performance

RabbitMQ, MySQL과 같은 외부 API 호출을 제외한 순수 엔진 성능만을 측정합니다.

### Test Run

```bash
go test -run '^$' \
  -bench '^BenchmarkEngineHandleOrder$' \
  -benchmem \
  -benchtime=5s \
  -count=5 \
  ./internal/core
```

### Benchmark environment

| Item           | Value                                                          |
| -------------- | -------------------------------------------------------------- |
| OS / Arch      | Linux / amd64                                                  |
| CPU            | Intel Core Ultra 7 255H                                        |
| Go package     | `github.com/KRONEX-Stock-Exchange/kronex-engine/internal/core` |
| Benchmark time | 5s x 5 runs                                                    |

### Results

| Scenario                 | Avg Latency/op |  Latency Range | Input Throughput |  Match Throughput | Memory/op | Allocs/op |
| ------------------------ | -------------: | -------------: | ---------------: | ----------------: | --------: | --------: |
| Limit order, no match    |       ~13.0 us | 12.75-13.23 us |  ~77K orders/sec |                 - |   ~9.0 KB |        88 |
| Match 1 resting order    |       ~18.4 us | 18.24-18.65 us |  ~54K orders/sec |  ~54K matches/sec |  ~15.3 KB |       107 |
| Sweep 100 resting orders |        ~334 us |     291-421 us | ~3.0K orders/sec | ~300K matches/sec |   ~315 KB |      1302 |

## Getting Started

### Prerequisites

- Go 1.26+
- MySQL
- RabbitMQ
- sqlc
- protoc

### 1. Configure environment

```bash
cp .env.example .env
```

`.env`의 MySQL, RabbitMQ 접속 정보를 로컬 환경에 맞게 수정합니다.

```env
MYSQL_HOST=127.0.0.1
MYSQL_PORT=3306
MYSQL_USER=kronex
MYSQL_PASSWORD=kronex
MYSQL_DATABASE=kronex

RABBITMQ_HOST=127.0.0.1
RABBITMQ_PORT=5672
RABBITMQ_USER=kronex
RABBITMQ_PASSWORD=kronex

WAL_DIR=./data/wal
```

`.env`에서 다른 데이터베이스 이름을 사용한다면 예시의 `kronex`를 해당 이름으로 바꿉니다.

### 2. Prepare database

MySQL schema와 migration은 **Server repository**에서 관리합니다.

엔진을 실행하기 전에 Server repository의 migration 가이드를 참고해 DB를 준비합니다.
엔진은 시작 시 `internal/storage`에서 사용하는 DB table들이 이미 생성되어 있다고 가정합니다.

### 3. Prepare RabbitMQ queues

RabbitMQ에는 [Queue Configuration](#queue-configuration)에 정의된 큐가 존재해야 합니다.

### 4. Generate code

쿼리나 protobuf 정의를 변경한 경우에만 생성 코드를 갱신합니다.

```bash
make sqlc
make proto
```

### 5. Run checks

```bash
make fmt
make vet
make test
```

### 6. Run engine

```bash
make run
```

또는 바이너리로 빌드 후 실행합니다.

```bash
make build
./bin/engine
```

## License

This project is licensed under the [Apache License 2.0](LICENSE).
