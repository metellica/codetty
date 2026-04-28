package main

import (
	"flag"
	"log"
	"net/http"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/gorilla/websocket"

	"codetty/hub"
)

var upgrader = websocket.Upgrader{
	CheckOrigin: func(r *http.Request) bool { return true },
}

func main() {
	addr := flag.String("addr", ":8080", "listen address")
	flag.Parse()

	h := hub.New()
	model := hub.NewModel(h)

	// Start TUI
	p := tea.NewProgram(model, tea.WithAltScreen(), tea.WithMouseCellMotion())
	h.SetProgram(p)

	// Start WebSocket server in background
	http.HandleFunc("/ws", func(w http.ResponseWriter, r *http.Request) {
		conn, err := upgrader.Upgrade(w, r, nil)
		if err != nil {
			log.Printf("upgrade error: %v", err)
			return
		}
		go h.HandleConn(conn)
	})

	go func() {
		log.Printf("codetty-hub listening on %s", *addr)
		if err := http.ListenAndServe(*addr, nil); err != nil {
			log.Fatalf("http server: %v", err)
		}
	}()

	// Run TUI (blocks until quit)
	if _, err := p.Run(); err != nil {
		log.Fatalf("TUI error: %v", err)
	}
}
