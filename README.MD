# Kronex Engine

Kronex 거래소의 Go 기반 엔진입니다.

## Table of Contents

- [Stack](#stack)
- [Architecture](#architecture)
- [Folder Architecture](#folder-architecture)
- [Flow](#flow)
  - [Overview](#overview)
  - [Main Engine](#main-engine)
  - [DB Publisher](#db-publisher)
  - [MQ Publisher](#mq-publisher)
  - [Snapshot](#snapshot)
  - [Recover](#recover)
  - [Error Process Rules](#error-process-rules)
- [Performance](#performance)
- [Getting Started](#getting-started)
- [License](#license)

## Stack

| Category      | Technology |
| ------------- | ---------- |
| Language      | Go         |
| Message Queue | RabbitMQ   |
| Database      | MySQL      |
| Query Codegen | sqlc       |

## Architecture

<img width="557" height="669" alt="architecture diagram" src="https://github.com/user-attachments/assets/ac8f5343-bdd9-4142-bd3c-3c66e5d3f2c6" />

## Folder Architecture

## Flow

### Overview

엔진이 원장과 호가창을 모두 관리합니다. API Server에서 MQ에 요청(주문, 원장 수정 등)을 실어 보내며, 주문 접수나 송금 요청이 이에 해당합니다.

> 병목 발생 시 종목별 매칭 고루틴과 개별 원장 프로세스로 분리할 예정

엔진은 다음 세 종류의 주요 고루틴으로 구성됩니다.

| Goroutine                      | Role                                                     |
| ------------------------------ | -------------------------------------------------------- |
| Main Engine (Order / Transfer) | Input 요청을 받아 매칭·송금을 처리하고 Output WAL을 작성 |
| DB Publisher                   | Output WAL을 projection DB에 반영                        |
| MQ Publisher                   | Output WAL에 대한 이벤트를 MQ로 발행                     |

공통 WAL 처리 원칙:

- Input WAL를 작성 후 `fsync`로 디스크에 저장한뒤 처리를 진행합니다.
- Replay시 Output WAL이 이미 있으면 건너뜁니다. (중복 방지)
- MQ 소비자 ACK 응답은 fire-and-forget 방식을 사용합니다.

### Main Engine

1. **중복 처리 체크** — `orderId <= lastProcessedOrderId` 또는 `transferId <= lastProcessedTransferId` 이면 이미 처리된 요청이므로 ACK만 하고 건너뜀 (ACK 실패로 인한 재전송 요청 처리)
2. Input WAL 작성 + `fsync`
3. MQ ACK (fire-and-forget)
4. 요청 커맨드 처리
5. Output WAL 작성
6. `lastProcessedOrderId` 또는 `lastProcessedTransferId`, `lastEventId(Output WAL Index)` 갱신

### DB Publisher

- 마지막 DB Cursor + 1 부터 Output WAL를 순서대로 읽습니다.
- projection DB SQL과 Cursor 업데이트를 같은 트랜잭션으로 커밋합니다. (성공시 Cursor 전진)
- Replay시 Cursor 테이블의 마지막 DB Cursor + 1 부터 실행합니다.

### MQ Publisher

- 마지막 MQ Cursor + 1 부터 Output WAL를 순서대로 읽습니다.
- MQ 전송 후 발행을 확인하고, 성공시 Cursor를 전진 합니다.
- Replay시 Cursor 테이블의 마지막 MQ Cursor + 1 부터 실행합니다.

### Snapshot

- 5분 간격으로 엔진 고루틴 처리 사이에 상태를 직렬화 하고, 데이터를 별도 고루틴이 받아 DB에 기록합니다.
- 스냅샷에는 엔진 상태(`호가창 및 원장` + `lastProcessedOrderId` + `lastProcessedTransferId` + `lastEventId` + `Input WAL Index`)를 저장합니다.
- 로그 무한 증식 방지를 위해 이미 스냅샷에 반영된 로그는 Truncate합니다.
  - Input WAL: Input WAL Index 까지 Truncate
  - Output WAL: DB Cursor와 MQ Cursor 중 더 뒤처진 쪽, 즉 min(DBCursor, MQCursor)까지 Truncate

### Recover

1. **Main Engine** — 최신 스냅샷 로드 → Input WAL을 `Input WAL Index + 1`부터 재생
   (재생 중 Output WAL은 있으면 패스, 크래시로 빠진 건 작성하여 완성)
2. **DB Publisher** — Cursor 테이블의 DB Cursor + 1 부터 Output WAL를 읽어 재개
3. **MQ Publisher** — Cursor 테이블의 MQ Cursor + 1 부터 Output WAL를 읽어 재개

### Error Process Rules

| 상황                       | 처리                                                 |
| -------------------------- | ---------------------------------------------------- |
| Input·Output WAL 쓰기 실패 | Throw                                                |
| 매칭 중 Panic              | Throw                                                |
| 소비자 ACK 실패            | 추후 재연결 시 중복 처리 체크 (Main Engine 1번 참조) |
| DB/MQ 발행 실패            | Cursor를 전진하지 않고 재시도                        |

## Performance

## Getting Started

## License

This project is licensed under the [Apache License 2.0](LICENSE).
