package core

type Engine struct {
	con Consumer
}

func NewEngine(con Consumer) *Engine {
	return &Engine{con: con}
}
