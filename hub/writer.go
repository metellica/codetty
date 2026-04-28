package hub

import (
	"log"

	"github.com/gorilla/websocket"
)

const writeChanSize = 256

// ConnWriter serializes WebSocket write operations through a channel.
// gorilla/websocket does not support concurrent writes; all writes for
// a connection must go through a single ConnWriter.
type ConnWriter struct {
	conn    *websocket.Conn
	writeCh chan []byte
	done    chan struct{}
}

func NewConnWriter(conn *websocket.Conn) *ConnWriter {
	w := &ConnWriter{
		conn:    conn,
		writeCh: make(chan []byte, writeChanSize),
		done:    make(chan struct{}),
	}
	go w.writeLoop()
	return w
}

func (w *ConnWriter) writeLoop() {
	defer close(w.done)
	for msg := range w.writeCh {
		if err := w.conn.WriteMessage(websocket.BinaryMessage, msg); err != nil {
			log.Printf("ws write error: %v", err)
			return
		}
	}
}

// Send enqueues a message for writing. Non-blocking; drops the message
// if the channel is full (slow client protection).
func (w *ConnWriter) Send(data []byte) {
	select {
	case w.writeCh <- data:
	default:
		log.Printf("write channel full, dropping message (%d bytes)", len(data))
	}
}

// Close stops the write loop and waits for it to finish.
func (w *ConnWriter) Close() {
	close(w.writeCh)
	<-w.done
}
