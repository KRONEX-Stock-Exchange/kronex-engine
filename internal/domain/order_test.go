// 테스트 항목:
// - 주문 거래 유형을 BUY·SELL·EDIT·CANCEL 문자열로 직렬화
// - 문자열 거래 유형을 내부 TradingType 값으로 역직렬화
package domain

import (
	"encoding/json"
	"testing"
)

func TestTradingTypeJSONRoundTrip(t *testing.T) {
	tests := []struct {
		name  string
		value TradingType
		json  string
	}{
		{name: "buy", value: TRADING_BUY, json: `"BUY"`},
		{name: "sell", value: TRADING_SELL, json: `"SELL"`},
		{name: "edit", value: TRADING_EDIT, json: `"EDIT"`},
		{name: "cancel", value: TRADING_CANCEL, json: `"CANCEL"`},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			encoded, err := json.Marshal(tt.value)
			if err != nil {
				t.Fatalf("marshal trading type: %v", err)
			}
			if string(encoded) != tt.json {
				t.Fatalf("encoded trading type = %s, want %s", encoded, tt.json)
			}

			var decoded TradingType
			if err := json.Unmarshal(encoded, &decoded); err != nil {
				t.Fatalf("unmarshal trading type: %v", err)
			}
			if decoded != tt.value {
				t.Errorf("decoded trading type = %d, want %d", decoded, tt.value)
			}
		})
	}
}
