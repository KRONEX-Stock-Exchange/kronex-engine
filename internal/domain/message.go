package domain

type Message struct {
	RoutingKey string
	Payload    []byte
}
