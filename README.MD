# Kronex Engine

Kronex 거래소의 Go 기반 엔진입니다.

## Table of Contents

- [Stack](#stack)
- [Architecture](#architecture)
- [Folder Architecture](#folder-architecture)
- [Flow](#flow)
  - [Overview](#overview)
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

## Architecture

<img width="557" height="669" alt="architecture diagram" src="https://github.com/user-attachments/assets/ac8f5343-bdd9-4142-bd3c-3c66e5d3f2c6" />

## Folder Architecture

## Flow

### Overview

엔진이 원장과 호가창을 모두 관리합니다. API Server에서 MQ에 요청(주문, 원장 수정 등)을 실어 보내며, 주문 접수나 송금 요청이 이에 해당합니다.

엔진은 다음 두 종류의 주요 고루틴으로 구성됩니다.

| Goroutine       | Role                                                                   |
| --------------- | ---------------------------------------------------------------------- |
| Main Engine     | Input 요청을 받아 매칭·송금을 처리하고 Output WAL을 작성               |
| Event Publisher | Output WAL을 projection DB에 반영하고 동일 레코드를 이벤트로 MQ에 발행 |

공통 WAL 처리 원칙:

- Input WAL를 작성 후 `fsync`로 디스크에 저장한뒤 처리를 진행합니다.
- 하나의 입력 주문이 만든 이벤트(체결들 + 주문/계좌/보유 상태)는 `OutputEnvelope` 한 레코드로 묶여 Output WAL에 기록됩니다.
- Replay시 Output WAL이 이미 있으면 건너뜁니다. (중복 방지)
- MQ 소비자 ACK 응답은 fire-and-forget 방식을 사용합니다.

### Main Engine

1. **중복 처리 체크** — `orderId <= lastProcessedOrderId` 또는 `transferId <= lastProcessedTransferId` 이면 이미 처리된 요청이므로 ACK만 하고 건너뜀 (ACK 실패로 인한 재전송 요청 처리)
2. Input WAL 작성 + `fsync`
3. MQ ACK (fire-and-forget)
4. 요청 커맨드 처리
5. Output WAL 작성 (`OutputEnvelope` 단위)
6. `lastProcessedOrderId` 또는 `lastProcessedTransferId` 갱신

생성되는 Output 이벤트 종류:

| Pattern           | 설명                             |
| ----------------- | -------------------------------- |
| `trade.executed`  | 체결 1건                         |
| `order.open`      | 지정가 잔여 수량으로 호가창 등재 |
| `order.filled`    | 주문 전량 체결                   |
| `order.canceled`  | 미체결 주문 취소                 |
| `account.updated` | 체결로 인한 계좌 잔고 변경       |
| `holding.updated` | 체결로 인한 보유 종목 변경       |

### Event Publisher

단일 `EVENT` 커서로 Output WAL 진척을 관리합니다.

- 마지막 `EVENT` Cursor + 1 부터 Output WAL를 순서대로 읽습니다.
- 각 레코드(`OutputEnvelope`)에 대해 다음 순서로 처리합니다.
  1. DB 반영
  2. MQ 발행
  3. 두 단계 모두 성공하면 Cursor를 전진합니다.
- 새 레코드는 시그널(cap 1)로 감지하며, 1초 주기 backstop으로도 확인합니다.
- Replay시 Cursor 테이블의 마지막 `EVENT` Cursor + 1 부터 재개합니다.

### Snapshot

- 5분 간격으로 엔진 고루틴 처리 사이에 상태를 직렬화 하고, 데이터를 별도 고루틴이 받아 DB에 기록합니다.
- 스냅샷에는 엔진 상태(`호가창 및 원장`)와 직렬화 시점의 `Input WAL Index`를 Protocol Buffers로 저장합니다.

### Recover

1. **Main Engine** — 최신 스냅샷 로드 → Input WAL을 `Input WAL Index + 1`부터 재생
   (재생 중 Output WAL은 있으면 패스, 크래시로 빠진 건 작성하여 완성)
2. **Event Publisher** — Cursor 테이블의 `EVENT` Cursor + 1 부터 Output WAL를 읽어 DB 반영·MQ 발행을 재개

### Error Process Rules

| Scenario                   | Handling                                             |
| -------------------------- | ---------------------------------------------------- |
| Input·Output WAL 쓰기 실패 | Throw                                                |
| 매칭 중 Panic              | Throw                                                |
| 소비자 ACK 실패            | 추후 재연결 시 중복 처리 체크 (Main Engine 1번 참조) |
| DB 반영·MQ 발행 실패       | Cursor를 전진하지 않고 재시도                        |

## Performance

## Getting Started

## License

This project is licensed under the [Apache License 2.0](LICENSE).
