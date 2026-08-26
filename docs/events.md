# KRONEX Engine 이벤트 명세

이 문서는 KRONEX Engine이 `event_queue`로 발행할 수 있는 모든 출력 이벤트를 정리한다.

## 공통 발행 형식

엔진은 하나의 입력 요청에서 발생한 이벤트들을 `events` 배열로 묶어 발행한다. 한 주문이 여러 주문과 체결되면 `trade.executed`, 주문 상태, 잔고 및 호가 변경 이벤트가 하나의 envelope에 함께 들어갈 수 있다.

```json
{
  "inputSeq": 471409,
  "outputSeq": 471409,
  "createdAt": "2026-07-15T01:34:27.420065136Z",
  "events": [
    {
      "pattern": "trade.executed",
      "data": {}
    }
  ]
}
```

| 필드        | 형식            | 설명                                   |
| ----------- | --------------- | -------------------------------------- |
| `inputSeq`  | JSON number     | 결과를 생성한 Input WAL 인덱스         |
| `outputSeq` | JSON number     | 발행 데이터가 저장된 Output WAL 인덱스 |
| `createdAt` | RFC 3339 문자열 | Output WAL 기록 시각(UTC)              |
| `events`    | 배열            | 한 요청에서 발생한 이벤트 목록         |

ID, 가격, 수량, 금액처럼 Go 구조체에 `json:",string"`이 적용된 정수 필드는 JSON 문자열로 발행된다. 반면 `inputSeq`와 `outputSeq`는 JSON number다.

## 이벤트 목록

| Pattern             | 발생 의미                                                |
| ------------------- | -------------------------------------------------------- |
| `trade.executed`    | 매수·매도 주문 체결                                      |
| `order.open`        | 지정가 주문의 미체결 또는 부분 체결 잔량이 호가창에 존재 |
| `order.filled`      | 주문 수량 전량 체결                                      |
| `order.canceled`    | 취소 요청 또는 시장가 주문의 미체결 잔량 취소            |
| `order.replaced`    | 기존 주문이 정정 주문으로 대체됨                         |
| `order.completed`   | 취소 요청 주문의 처리가 완료됨                           |
| `order.rejected`    | 주문 유효성 검사 실패                                    |
| `account.activated` | 계좌 활성화 완료                                         |
| `account.updated`   | 계좌 잔액 변경                                           |
| `holding.updated`   | 보유 종목 변경                                           |
| `stock.listed`      | 종목 상장 완료                                           |
| `stock.updated`     | 체결로 종목 현재가 변경                                  |
| `orderbook.updated` | 영향을 받은 호가 가격대의 최종 잔량 변경                 |
| `transfer.completed` | 계좌 간 송금 완료                                       |
| `transfer.rejected` | 송금 유효성 검사 실패                                    |

## `trade.executed`

주문이 체결될 때 체결 건마다 발생한다. `tradingType`은 신규 주문인 taker의 방향이다.

```json
{
  "pattern": "trade.executed",
  "data": {
    "id": "448022",
    "stockId": "900003",
    "price": "49600",
    "quantity": "1",
    "makerOrderId": "10000446759",
    "takerOrderId": "10000447956",
    "tradingType": "BUY",
    "executedAt": "2026-07-15T01:34:27.420065136Z"
  }
}
```

- `makerOrderId`: 호가창에 먼저 존재하던 주문 ID
- `takerOrderId`: 체결을 일으킨 신규 주문 ID
- `tradingType`: taker 기준 `BUY` 또는 `SELL`
- taker가 `BUY`이면 maker는 `SELL`이고, taker가 `SELL`이면 maker는 `BUY`다.

## 주문 상태 이벤트

`order.open`, `order.filled`, `order.canceled`, `order.replaced`, `order.completed`는 같은 데이터 형식을 사용한다.

```json
{
  "pattern": "order.open",
  "data": {
    "id": "10000446759",
    "targetId": "0",
    "accountId": "2",
    "stockId": "900003",
    "price": "49600",
    "tradingType": "SELL",
    "quantity": "140",
    "filledQuantity": "27"
  }
}
```

| 필드             | 설명                                        |
| ---------------- | ------------------------------------------- |
| `id`             | 상태가 변경된 주문 또는 요청 주문 ID        |
| `targetId`       | 정정·취소 대상 주문 ID. 대상이 없으면 `"0"` |
| `accountId`      | 주문 계좌 ID                                |
| `stockId`        | 종목 ID                                     |
| `price`          | 주문 가격                                   |
| `tradingType`    | `BUY`, `SELL`, `EDIT`, `CANCEL`             |
| `quantity`       | 주문 총수량                                 |
| `filledQuantity` | 누적 체결 수량                              |

### `order.open`

지정가 주문이 호가창에 남아 있음을 나타낸다. 신규 taker뿐 아니라 일부 체결 후 호가창에 남은 maker에도 발생한다.

```json
{
  "pattern": "order.open",
  "data": {
    "id": "10000446759",
    "targetId": "0",
    "accountId": "2",
    "stockId": "900003",
    "price": "49600",
    "tradingType": "SELL",
    "quantity": "140",
    "filledQuantity": "27"
  }
}
```

### `order.filled`

주문이 전량 체결됐음을 나타낸다.

```json
{
  "pattern": "order.filled",
  "data": {
    "id": "10000447956",
    "targetId": "10000447948",
    "accountId": "900003",
    "stockId": "900003",
    "price": "49700",
    "tradingType": "BUY",
    "quantity": "1",
    "filledQuantity": "1"
  }
}
```

### `order.canceled`

다음 경우에 발생한다.

- 활성 주문에 대한 취소 요청이 성공해 대상 주문이 취소됨
- 시장가 주문에 미체결 잔량이 남아 호가창에 등록되지 않고 취소됨

```json
{
  "pattern": "order.canceled",
  "data": {
    "id": "10000446759",
    "targetId": "0",
    "accountId": "2",
    "stockId": "900003",
    "price": "49600",
    "tradingType": "SELL",
    "quantity": "140",
    "filledQuantity": "27"
  }
}
```

### `order.replaced`

가격 정정으로 기존 활성 주문이 새 주문으로 대체됐음을 나타낸다. `id`는 대체된 기존 주문 ID다.

```json
{
  "pattern": "order.replaced",
  "data": {
    "id": "10000447948",
    "targetId": "10000447947",
    "accountId": "900003",
    "stockId": "900003",
    "price": "49400",
    "tradingType": "BUY",
    "quantity": "1",
    "filledQuantity": "0"
  }
}
```

### `order.completed`

정정·취소 요청 자체의 처리가 완료됐음을 나타낸다. 현재 구현에서는 취소 요청 성공 시 취소 요청 주문에 대해 발생한다.

```json
{
  "pattern": "order.completed",
  "data": {
    "id": "10000448001",
    "targetId": "10000446759",
    "accountId": "2",
    "stockId": "900003",
    "price": "0",
    "tradingType": "CANCEL",
    "quantity": "0",
    "filledQuantity": "0"
  }
}
```

## `order.rejected`

주문 검증에 실패해 처리되지 않았음을 나타낸다.

```json
{
  "pattern": "order.rejected",
  "data": {
    "orderId": "10000448002",
    "reason": "INSUFFICIENT_BALANCE"
  }
}
```

현재 발생 가능한 `reason`은 다음과 같다.

| Reason                 | 의미                                               |
| ---------------------- | -------------------------------------------------- |
| `INVALID_ORDER`        | 잘못된 ID, 가격, 수량 또는 지원하지 않는 주문 형식 |
| `INSUFFICIENT_BALANCE` | 매수 가능 잔액 부족                                |
| `INSUFFICIENT_STOCK`   | 매도 가능 보유수량 부족                            |
| `STOCK_NOT_TRADABLE`   | 종목이 거래 가능한 상태가 아님                     |
| `ORDER_NOT_ACTIVE`     | 정정·취소 대상 주문이 활성 상태가 아님             |

## `account.activated`

계좌 등록 요청을 처리해 계좌가 활성화됐음을 나타낸다.

```json
{
  "pattern": "account.activated",
  "data": {
    "id": "900003",
    "balance": "1000000",
    "availableBalance": "1000000"
  }
}
```

## `account.updated`

체결, 주문 취소·정정 또는 어드민 조정으로 계좌 잔액이 변경됐음을 나타낸다.

```json
{
  "pattern": "account.updated",
  "data": {
    "id": "900003",
    "balance": "669662",
    "availableBalance": "610947"
  }
}
```

- `balance`: 총 잔액
- `availableBalance`: 신규 주문에 사용할 수 있는 가용 잔액

## `holding.updated`

체결, 주문 취소·정정 또는 어드민 조정으로 보유 종목이 변경됐음을 나타낸다.

```json
{
  "pattern": "holding.updated",
  "data": {
    "accountId": "900003",
    "stockId": "900003",
    "quantity": "2",
    "availableQuantity": "2",
    "average": "49600",
    "totalBuyAmount": "99200"
  }
}
```

- `quantity`: 총 보유수량
- `availableQuantity`: 신규 매도 주문에 사용할 수 있는 수량
- `average`: 평균 매수가
- `totalBuyAmount`: 총 매수금액
- `quantity`와 `availableQuantity`가 모두 0이면 DB projector는 해당 보유 레코드를 삭제한다.

## `stock.listed`

종목 상장 요청 처리가 완료됐음을 나타낸다.

```json
{
  "pattern": "stock.listed",
  "data": {
    "id": "900003",
    "price": "50000",
    "status": "LISTED"
  }
}
```

## `stock.updated`

체결 발생으로 종목의 현재가가 마지막 체결가로 변경됐음을 나타낸다.

```json
{
  "pattern": "stock.updated",
  "data": {
    "id": "900003",
    "price": "49600",
    "status": "LISTED"
  }
}
```

종목 상태 값은 `LISTED`, `SUSPENDED`, `DELISTED`, `PENDING` 중 하나다.

## `orderbook.updated`

주문 처리로 영향을 받은 호가 가격대의 최종 잔량을 전달한다. 하나의 주문이 여러 가격대와 체결되면 `levels`에 여러 항목이 들어갈 수 있다.

```json
{
  "pattern": "orderbook.updated",
  "data": {
    "stockId": "900003",
    "levels": [
      {
        "side": "SELL",
        "price": "49600",
        "quantity": "113"
      },
      {
        "side": "BUY",
        "price": "49400",
        "quantity": "0"
      }
    ]
  }
}
```

- `side`: `BUY` 또는 `SELL`
- `quantity`: 해당 가격대 전체 주문의 최종 잔량
- `quantity`가 `"0"`이면 해당 가격대가 호가창에서 사라졌음을 의미한다.

## `transfer.completed`

계좌 간 송금이 정상 처리됐음을 나타낸다.

```json
{
  "pattern": "transfer.completed",
  "data": {
    "id": "77",
    "senderAccountId": "2",
    "recipientAccountId": "900003",
    "amount": "300",
    "completedAt": "2026-08-25T03:04:05.120065136Z"
  }
}
```

| 필드                 | 설명                    |
| -------------------- | ----------------------- |
| `id`                 | 송금 요청 ID            |
| `senderAccountId`    | 보내는 계좌 ID          |
| `recipientAccountId` | 받는 계좌 ID            |
| `amount`             | 송금 금액               |
| `completedAt`        | 엔진의 처리 완료 시각   |

송금이 성공하면 발신·수신 계좌의 `account.updated` 2건과 함께 **한 envelope에 이벤트 3개**가 발행된다.

```json
{
  "inputSeq": 471410,
  "outputSeq": 471410,
  "createdAt": "2026-08-25T03:04:05.120065136Z",
  "events": [
    {
      "pattern": "account.updated",
      "data": {
        "id": "2",
        "balance": "700",
        "availableBalance": "500"
      }
    },
    {
      "pattern": "account.updated",
      "data": {
        "id": "900003",
        "balance": "800",
        "availableBalance": "800"
      }
    },
    {
      "pattern": "transfer.completed",
      "data": {
        "id": "77",
        "senderAccountId": "2",
        "recipientAccountId": "900003",
        "amount": "300",
        "completedAt": "2026-08-25T03:04:05.120065136Z"
      }
    }
  ]
}
```

첫 번째 `account.updated`가 발신 계좌, 두 번째가 수신 계좌다. 송금은 `balance`와 `availableBalance`를 함께 옮긴다.

## `transfer.rejected`

송금 검증에 실패해 잔액이 이동하지 않았음을 나타낸다.

```json
{
  "pattern": "transfer.rejected",
  "data": {
    "id": "78",
    "senderAccountId": "2",
    "recipientAccountId": "900004",
    "amount": "400",
    "reason": "INVALID_RECIPIENT",
    "completedAt": "2026-08-25T03:04:06.120065136Z"
  }
}
```

거부 시에는 잔액이 바뀌지 않으므로 `account.updated`가 함께 발행되지 않는다. **한 envelope에 `transfer.rejected` 1건만** 들어간다.

현재 발생 가능한 `reason`은 다음과 같다.

| Reason                 | 의미                                        |
| ---------------------- | ------------------------------------------- |
| `INSUFFICIENT_BALANCE` | 발신 계좌의 가용 잔액 부족                  |
| `INVALID_RECIPIENT`    | 받는 계좌가 없거나 계좌 ID가 올바르지 않음  |
| `INVALID_SENDER`       | 보내는 계좌가 없거나 계좌 ID가 올바르지 않음 |
| `SENDER_NOT_ACTIVE`    | 보내는 계좌가 활성 상태가 아님              |
| `RECIPIENT_NOT_ACTIVE` | 받는 계좌가 활성 상태가 아님                |
| `SELF_TRANSFER`        | 보내는 계좌와 받는 계좌가 같음              |
| `INVALID_REQUEST`      | 요청 ID 또는 금액이 올바르지 않음           |

`SENDER_NOT_ACTIVE`와 `RECIPIENT_NOT_ACTIVE`는 계좌에 상태 필드가 추가되기 전까지는 발생하지 않는다. 그때까지 계좌를 찾을 수 없는 경우는 각각 `INVALID_SENDER`, `INVALID_RECIPIENT`로 거부된다.

## 주문 처리 시 이벤트 조합 예시

하나의 매수 정정 주문이 기존 매도 주문과 체결된 경우의 전체 발행 예시다. 실제 잔액과 호가 레벨 수는 처리 결과에 따라 달라진다.

```json
{
  "inputSeq": 471409,
  "outputSeq": 471409,
  "createdAt": "2026-07-15T01:34:27.420065136Z",
  "events": [
    {
      "pattern": "order.replaced",
      "data": {
        "id": "10000447948",
        "targetId": "10000447947",
        "accountId": "900003",
        "stockId": "900003",
        "price": "49400",
        "tradingType": "BUY",
        "quantity": "1",
        "filledQuantity": "0"
      }
    },
    {
      "pattern": "trade.executed",
      "data": {
        "id": "448022",
        "stockId": "900003",
        "price": "49600",
        "quantity": "1",
        "makerOrderId": "10000446759",
        "takerOrderId": "10000447956",
        "tradingType": "BUY",
        "executedAt": "2026-07-15T01:34:27.420065136Z"
      }
    },
    {
      "pattern": "order.open",
      "data": {
        "id": "10000446759",
        "targetId": "0",
        "accountId": "2",
        "stockId": "900003",
        "price": "49600",
        "tradingType": "SELL",
        "quantity": "140",
        "filledQuantity": "27"
      }
    },
    {
      "pattern": "order.filled",
      "data": {
        "id": "10000447956",
        "targetId": "10000447948",
        "accountId": "900003",
        "stockId": "900003",
        "price": "49700",
        "tradingType": "BUY",
        "quantity": "1",
        "filledQuantity": "1"
      }
    },
    {
      "pattern": "orderbook.updated",
      "data": {
        "stockId": "900003",
        "levels": [
          {
            "side": "SELL",
            "price": "49600",
            "quantity": "113"
          }
        ]
      }
    },
    {
      "pattern": "account.updated",
      "data": {
        "id": "900003",
        "balance": "669662",
        "availableBalance": "610947"
      }
    },
    {
      "pattern": "holding.updated",
      "data": {
        "accountId": "900003",
        "stockId": "900003",
        "quantity": "2",
        "availableQuantity": "2",
        "average": "49600",
        "totalBuyAmount": "99200"
      }
    },
    {
      "pattern": "stock.updated",
      "data": {
        "id": "900003",
        "price": "49600",
        "status": "LISTED"
      }
    }
  ]
}
```
